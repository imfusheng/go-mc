package bot

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/net"
	"github.com/imfusheng/go-mc/net/CFB8"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

type LoginErr struct {
	Stage string
	Err   error
}

func (l LoginErr) Error() string {
	return "bot: login error: [" + l.Stage + "] " + l.Err.Error()
}

func (l LoginErr) Unwrap() error {
	return l.Err
}

func (c *Client) joinLogin(conn *net.Conn, profile *protocol.Profile, options JoinOptions) error {
	var err error
	if profile == nil {
		return LoginErr{"select protocol", fmt.Errorf("nil protocol profile")}
	}
	if err := validateLoginName(c.Auth.Name); err != nil {
		return LoginErr{"login start", err}
	}
	c.UUID = uuid.Nil
	c.SessionID = uuid.Nil
	if c.Auth.UUID != "" {
		c.UUID, err = uuid.Parse(c.Auth.UUID)
		if err != nil {
			return LoginErr{"login start", err}
		}
	}
	loginStart, err := c.loginStartPacket(profile, options)
	if err != nil {
		return LoginErr{"login start", err}
	}
	err = conn.WritePacket(loginStart)
	if err != nil {
		return LoginErr{"login start", err}
	}
	receiving := "encrypt start"
	for {
		// Receive Packet
		var p pk.Packet
		if err = conn.ReadPacket(&p); err != nil {
			return LoginErr{receiving, err}
		}

		kind, resolveErr := protocol.RequirePacketKind(profile, protocol.StateLogin, protocol.Clientbound, p.ID)
		if resolveErr != nil {
			return LoginErr{receiving, resolveErr}
		}

		// Handle Packet
		switch kind {
		case protocol.PacketLoginDisconnect:
			var reason chat.Message
			reason, err = scanLoginDisconnect(profile, p)
			if err != nil {
				return LoginErr{"disconnect", err}
			}
			return LoginErr{"disconnect", DisconnectErr(reason)}

		case protocol.PacketLoginEncryptionRequest:
			if err := handleEncryptionRequest(options.Context, conn, c, profile, p); err != nil {
				return LoginErr{"encryption", err}
			}
			receiving = "set compression"

		case protocol.PacketLoginSuccess:
			err := c.scanLoginSuccess(profile, p)
			if err != nil {
				return LoginErr{"login success", err}
			}
			if profile.HasLoginAcknowledgement() {
				ackID, resolveErr := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginAcknowledged)
				if resolveErr != nil {
					return LoginErr{"login success", resolveErr}
				}
				err = conn.WritePacket(pk.Marshal(ackID))
				if err != nil {
					return LoginErr{"login success", err}
				}
			}
			return nil

		case protocol.PacketLoginSetCompression:
			var threshold pk.VarInt
			if err := p.Scan(&threshold); err != nil {
				return LoginErr{"compression", err}
			}
			if threshold < 0 || threshold > pk.MaxDataLength {
				return LoginErr{"compression", fmt.Errorf("invalid compression threshold %d", threshold)}
			}
			conn.SetThreshold(int(threshold))
			receiving = "login success"

		case protocol.PacketLoginPluginRequest:
			var (
				msgid   pk.VarInt
				channel pk.Identifier
				data    pk.PluginMessageData
			)
			if err := p.Scan(&msgid, &channel, &data); err != nil {
				return LoginErr{"Login Plugin", err}
			}

			var PluginMessageData pk.Option[pk.PluginMessageData, *pk.PluginMessageData]
			if handler, ok := c.LoginPlugin[string(channel)]; ok {
				PluginMessageData.Has = true
				PluginMessageData.Val, err = handler(data)
				if err != nil {
					return LoginErr{"Login Plugin", err}
				}
			}

			responseID, resolveErr := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginPluginResponse)
			if resolveErr != nil {
				return LoginErr{"login Plugin", resolveErr}
			}
			if err := conn.WritePacket(pk.Marshal(
				responseID,
				msgid, PluginMessageData,
			)); err != nil {
				return LoginErr{"login Plugin", err}
			}

		case protocol.PacketLoginCookieRequest:
			if profile.Key().Protocol < 766 {
				return LoginErr{"cookie request", fmt.Errorf("unexpected cookie request for Minecraft %s", profile.Version().Name)}
			}
			var key pk.Identifier
			err := p.Scan(&key)
			if err != nil {
				return LoginErr{"cookie request", err}
			}
			cookieContent := c.Cookies[string(key)]
			responseID, resolveErr := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginCookieResponse)
			if resolveErr != nil {
				return LoginErr{"cookie response", resolveErr}
			}
			err = conn.WritePacket(pk.Marshal(
				responseID,
				key, pk.OptionEncoder[pk.ByteArray]{
					Has: cookieContent != nil,
					Val: pk.ByteArray(cookieContent),
				},
			))
			if err != nil {
				return LoginErr{"cookie response", err}
			}

		default:
			return LoginErr{receiving, fmt.Errorf("unexpected clientbound login packet %q (%#02x)", kind, p.ID)}
		}
	}
}

func scanLoginDisconnect(profile *protocol.Profile, p pk.Packet) (chat.Message, error) {
	if profile == nil {
		return chat.Message{}, fmt.Errorf("nil protocol profile")
	}
	r := bytes.NewReader(p.Data)
	var wireReason chat.JsonMessage
	if _, err := wireReason.ReadFrom(r); err != nil {
		return chat.Message{}, err
	}
	if r.Len() != 0 {
		return chat.Message{}, fmt.Errorf("login disconnect contains %d trailing bytes", r.Len())
	}
	return chat.Message(wireReason), nil
}

func validateLoginName(name string) error {
	if name == "" {
		return fmt.Errorf("player name is empty")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("player name is not valid UTF-8")
	}
	if utf8.RuneCountInString(name) > 16 {
		return fmt.Errorf("player name is longer than 16 characters")
	}
	return nil
}

func (c *Client) loginStartPacket(profile *protocol.Profile, options JoinOptions) (pk.Packet, error) {
	if c == nil {
		return pk.Packet{}, fmt.Errorf("nil client")
	}
	if profile == nil {
		return pk.Packet{}, fmt.Errorf("nil protocol profile")
	}
	if err := validateLoginName(c.Auth.Name); err != nil {
		return pk.Packet{}, err
	}
	fields := []pk.FieldEncoder{pk.String(c.Auth.Name)}
	hasUUID := c.Auth.UUID != ""
	keyPair := options.KeyPair
	usesProfileKey := profile.LoginStartStyle() == protocol.LoginStartNameAndOptionalSignature ||
		profile.LoginStartStyle() == protocol.LoginStartNameSignatureAndOptionalUUID
	if usesProfileKey && !options.NoPublicKey && keyPair == nil && c.Auth.AsTk != "" {
		fetched, err := user.GetOrFetchKeyPairContext(options.Context, c.Auth.AsTk)
		if err != nil {
			return pk.Packet{}, fmt.Errorf("fetch profile public key: %w", err)
		}
		keyPair = &fetched
	}
	if options.NoPublicKey {
		keyPair = nil
	}

	switch profile.LoginStartStyle() {
	case protocol.LoginStartNameOnly:
	case protocol.LoginStartNameAndOptionalSignature:
		fields = append(fields, pk.Boolean(keyPair != nil))
		if keyPair != nil {
			fields = append(fields, keyPair.Encoder(user.CertificateSignatureV1))
		}
	case protocol.LoginStartNameSignatureAndOptionalUUID:
		fields = append(fields, pk.Boolean(keyPair != nil))
		if keyPair != nil {
			fields = append(fields, keyPair.Encoder(user.CertificateSignatureV2))
		}
		fields = append(fields, pk.Boolean(hasUUID))
		if hasUUID {
			fields = append(fields, pk.UUID(c.UUID))
		}
	case protocol.LoginStartNameAndOptionalUUID:
		fields = append(fields, pk.Boolean(hasUUID))
		if hasUUID {
			fields = append(fields, pk.UUID(c.UUID))
		}
	case protocol.LoginStartNameAndUUID:
		fields = append(fields, pk.UUID(c.UUID))
	default:
		return pk.Packet{}, protocol.UnsupportedCapabilityError{
			Version: profile.Version().Name, Capability: "login start",
		}
	}
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginStart)
	if err != nil {
		return pk.Packet{}, err
	}
	return pk.Marshal(packetID, fields...), nil
}

func (c *Client) scanLoginSuccess(profile *protocol.Profile, p pk.Packet) error {
	if profile == nil {
		return fmt.Errorf("nil protocol profile")
	}
	r := bytes.NewReader(p.Data)
	var id uuid.UUID
	if profile.LoginSuccessUsesStringUUID() {
		idText, err := readBoundedString(r, 36, "legacy profile UUID")
		if err != nil {
			return err
		}
		id, err = uuid.Parse(idText)
		if err != nil {
			return fmt.Errorf("parse legacy profile UUID %q: %w", idText, err)
		}
	} else {
		var wireID pk.UUID
		if _, err := wireID.ReadFrom(r); err != nil {
			return fmt.Errorf("read profile UUID: %w", err)
		}
		id = uuid.UUID(wireID)
	}

	name, err := readBoundedString(r, 64, "profile name")
	if err != nil {
		return err
	}
	if err := validateLoginName(name); err != nil {
		return fmt.Errorf("invalid profile name: %w", err)
	}

	var properties []user.Property
	if profile.LoginSuccessHasProperties() {
		properties, err = readProfileProperties(r)
		if err != nil {
			return err
		}
	}
	if profile.LoginSuccessHasStrictErrorHandling() {
		var strict pk.Boolean
		if _, err := strict.ReadFrom(r); err != nil {
			return fmt.Errorf("read strict error handling: %w", err)
		}
	}
	var sessionID uuid.UUID
	if profile.LoginSuccessHasSessionID() {
		var wireSessionID pk.UUID
		if _, err := wireSessionID.ReadFrom(r); err != nil {
			return fmt.Errorf("read login session UUID: %w", err)
		}
		sessionID = uuid.UUID(wireSessionID)
	}
	if r.Len() != 0 {
		return fmt.Errorf("login success contains %d trailing bytes", r.Len())
	}

	// Only publish the new profile after the complete packet has validated.
	c.UUID = id
	c.Name = name
	c.SessionID = sessionID
	c.Properties = properties
	return nil
}

const maxLoginProperties = 1024

func readProfileProperties(r *bytes.Reader) ([]user.Property, error) {
	var count pk.VarInt
	if _, err := count.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("read profile property count: %w", err)
	}
	if count < 0 || count > maxLoginProperties {
		return nil, fmt.Errorf("invalid profile property count %d", count)
	}
	properties := make([]user.Property, int(count))
	for i := range properties {
		name, err := readBoundedString(r, 256, "profile property name")
		if err != nil {
			return nil, fmt.Errorf("property %d: %w", i, err)
		}
		value, err := readBoundedString(r, pk.MaxDataLength, "profile property value")
		if err != nil {
			return nil, fmt.Errorf("property %d: %w", i, err)
		}
		var hasSignature pk.Boolean
		if _, err := hasSignature.ReadFrom(r); err != nil {
			return nil, fmt.Errorf("property %d signature flag: %w", i, err)
		}
		var signature string
		if hasSignature {
			signature, err = readBoundedString(r, pk.MaxDataLength, "profile property signature")
			if err != nil {
				return nil, fmt.Errorf("property %d: %w", i, err)
			}
		}
		properties[i] = user.Property{Name: name, Value: value, Signature: signature}
	}
	return properties, nil
}

// Auth includes an account
type Auth struct {
	Name string
	UUID string
	AsTk string
}

func handleEncryptionRequest(ctx context.Context, conn *net.Conn, c *Client, profile *protocol.Profile, p pk.Packet) error {
	// Read EncryptionRequest
	er, err := scanEncryptionRequest(profile, p)
	if err != nil {
		return err
	}
	// 创建AES对称加密密钥
	key, encoStream, decoStream, err := newSymmetricEncryption()
	if err != nil {
		return fmt.Errorf("create shared secret: %w", err)
	}

	if er.ShouldAuthenticate {
		err = loginAuth(ctx, c.Auth, key, er) // 向Mojang验证
		if err != nil {
			return fmt.Errorf("login fail: %w", err)
		}
	}

	// 响应加密请求
	// Write Encryption Key Response
	p, err = genEncryptionKeyResponse(key, er.PublicKey, er.VerifyToken, profile)
	if err != nil {
		return fmt.Errorf("gen encryption key response fail: %v", err)
	}

	err = conn.WritePacket(p)
	if err != nil {
		return err
	}

	// 设置连接加密
	conn.SetCipher(encoStream, decoStream)
	return nil
}

type encryptionRequest struct {
	ServerID           string
	PublicKey          []byte
	VerifyToken        []byte
	ShouldAuthenticate bool
}

func scanEncryptionRequest(profile *protocol.Profile, p pk.Packet) (encryptionRequest, error) {
	var er encryptionRequest
	if profile == nil {
		return er, fmt.Errorf("nil protocol profile")
	}
	r := bytes.NewReader(p.Data)
	serverID, err := readBoundedString(r, 64, "server ID")
	if err != nil {
		return er, err
	}
	er.ServerID = serverID
	if profile.LoginByteArraysUseShortLength() {
		var publicKey, verifyToken shortByteArray
		if _, err = publicKey.ReadFrom(r); err == nil {
			_, err = verifyToken.ReadFrom(r)
		}
		er.PublicKey = publicKey
		er.VerifyToken = verifyToken
	} else {
		er.PublicKey, err = readBoundedByteArray(r, maxEncryptionPublicKeyLength, "encryption public key")
		if err == nil {
			er.VerifyToken, err = readBoundedByteArray(r, maxVerifyTokenLength, "verify token")
		}
	}
	if err != nil {
		return er, err
	}
	if len(er.PublicKey) == 0 {
		return er, fmt.Errorf("encryption public key is empty")
	}
	if len(er.PublicKey) > maxEncryptionPublicKeyLength {
		return er, fmt.Errorf("encryption public key is too long: %d", len(er.PublicKey))
	}
	if len(er.VerifyToken) > maxVerifyTokenLength {
		return er, fmt.Errorf("verify token is too long: %d", len(er.VerifyToken))
	}
	er.ShouldAuthenticate = true
	if profile.EncryptionRequestHasShouldAuthenticate() {
		var shouldAuthenticate pk.Boolean
		if _, err = shouldAuthenticate.ReadFrom(r); err != nil {
			return er, fmt.Errorf("read should authenticate flag: %w", err)
		}
		er.ShouldAuthenticate = bool(shouldAuthenticate)
	}
	if r.Len() != 0 {
		return er, fmt.Errorf("encryption request contains %d trailing bytes", r.Len())
	}
	return er, nil
}

const (
	maxEncryptionPublicKeyLength = 8192
	maxVerifyTokenLength         = 1024
)

type shortByteArray []byte

func (b *shortByteArray) ReadFrom(r io.Reader) (int64, error) {
	var length pk.UnsignedShort
	n, err := length.ReadFrom(r)
	if err != nil {
		return n, err
	}
	*b = make([]byte, int(length))
	nn, err := io.ReadFull(r, *b)
	return n + int64(nn), err
}

func (b shortByteArray) WriteTo(w io.Writer) (int64, error) {
	if len(b) > 1<<16-1 {
		return 0, fmt.Errorf("short byte array is too long: %d", len(b))
	}
	n, err := pk.UnsignedShort(len(b)).WriteTo(w)
	if err != nil {
		return n, err
	}
	nn, err := w.Write(b)
	return n + int64(nn), err
}

func readBoundedString(r *bytes.Reader, max int, field string) (string, error) {
	value, err := readBoundedByteArray(r, max, field)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	return string(value), nil
}

func readBoundedByteArray(r *bytes.Reader, max int, field string) ([]byte, error) {
	var length pk.VarInt
	if _, err := length.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("read %s length: %w", field, err)
	}
	if length < 0 || int64(length) > int64(max) {
		return nil, fmt.Errorf("invalid %s length %d (maximum %d)", field, length, max)
	}
	if int(length) > r.Len() {
		return nil, fmt.Errorf("%s length %d exceeds remaining packet data %d", field, length, r.Len())
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, fmt.Errorf("read %s: %w", field, err)
	}
	return value, nil
}

// authDigest computes a special SHA-1 digest required for Minecraft web
// authentication on Premium servers (online-mode=true).
// Source: http://wiki.vg/Protocol_Encryption#Server
func authDigest(serverID string, sharedSecret, publicKey []byte) string {
	h := sha1.New()
	h.Write([]byte(serverID))
	h.Write(sharedSecret)
	h.Write(publicKey)
	hash := h.Sum(nil)

	// Check for negative hashes
	negative := (hash[0] & 0x80) == 0x80
	if negative {
		hash = twosComplement(hash)
	}

	// Trim away zeroes
	res := strings.TrimLeft(hex.EncodeToString(hash), "0")
	if negative {
		res = "-" + res
	}

	return res
}

// little endian
func twosComplement(p []byte) []byte {
	carry := true
	for i := len(p) - 1; i >= 0; i-- {
		p[i] = ^p[i]
		if carry {
			carry = p[i] == 0xff
			p[i]++
		}
	}
	return p
}

type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type request struct {
	AccessToken     string  `json:"accessToken"`
	SelectedProfile profile `json:"selectedProfile"`
	ServerID        string  `json:"serverId"`
}

func loginAuth(ctx context.Context, auth Auth, shareSecret []byte, er encryptionRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	digest := authDigest(er.ServerID, shareSecret, er.PublicKey)

	requestPacket, err := json.Marshal(
		request{
			AccessToken: auth.AsTk,
			SelectedProfile: profile{
				ID:   auth.UUID,
				Name: auth.Name,
			},
			ServerID: digest,
		},
	)
	if err != nil {
		return fmt.Errorf("create request packet to yggdrasil faile: %v", err)
	}

	PostRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sessionserver.mojang.com/session/minecraft/join",
		bytes.NewReader(requestPacket))
	if err != nil {
		return fmt.Errorf("make request error: %v", err)
	}
	PostRequest.Header.Set("User-agent", "go-mc")
	PostRequest.Header.Set("Connection", "keep-alive")
	PostRequest.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(PostRequest)
	if err != nil {
		return fmt.Errorf("post fail: %v", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return fmt.Errorf("read authentication response: %w", readErr)
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("auth fail: %s", string(body))
	}
	return nil
}

// AES/CFB8 with random key
func newSymmetricEncryption() (key []byte, encoStream, decoStream cipher.Stream, err error) {
	key = make([]byte, 16)
	if _, err = rand.Read(key); err != nil {
		return nil, nil, nil, err
	}

	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, nil, err
	}
	decoStream = CFB8.NewCFB8Decrypt(b, key)
	encoStream = CFB8.NewCFB8Encrypt(b, key)
	return key, encoStream, decoStream, nil
}

func genEncryptionKeyResponse(shareSecret, publicKey, verifyToken []byte, profile *protocol.Profile) (erp pk.Packet, err error) {
	if profile == nil {
		return erp, fmt.Errorf("nil protocol profile")
	}
	if len(publicKey) == 0 || len(publicKey) > maxEncryptionPublicKeyLength {
		return erp, fmt.Errorf("invalid encryption public key length %d", len(publicKey))
	}
	if len(verifyToken) > maxVerifyTokenLength {
		return erp, fmt.Errorf("invalid verify token length %d", len(verifyToken))
	}
	iPK, err := x509.ParsePKIXPublicKey(publicKey) // Decode Public Key
	if err != nil {
		err = fmt.Errorf("decode public key fail: %v", err)
		return
	}
	rsaKey, ok := iPK.(*rsa.PublicKey)
	if !ok {
		return erp, fmt.Errorf("decode public key fail: expected RSA key, got %T", iPK)
	}
	cryptPK, err := rsa.EncryptPKCS1v15(rand.Reader, rsaKey, shareSecret)
	if err != nil {
		err = fmt.Errorf("encryption share secret fail: %v", err)
		return
	}

	verifyT, err := rsa.EncryptPKCS1v15(rand.Reader, rsaKey, verifyToken)
	if err != nil {
		err = fmt.Errorf("encryption verfy tokenfail: %v", err)
		return erp, err
	}
	if profile.LoginByteArraysUseShortLength() {
		packetID, resolveErr := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginEncryptionResponse)
		if resolveErr != nil {
			return erp, resolveErr
		}
		return pk.Marshal(
			packetID,
			shortByteArray(cryptPK),
			shortByteArray(verifyT),
		), nil
	}
	if profile.EncryptionResponseUsesVerifyTokenOption() {
		packetID, resolveErr := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginEncryptionResponse)
		if resolveErr != nil {
			return erp, resolveErr
		}
		return pk.Marshal(
			packetID,
			pk.ByteArray(cryptPK),
			pk.Boolean(true),
			pk.ByteArray(verifyT),
		), nil
	}
	packetID, resolveErr := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginEncryptionResponse)
	if resolveErr != nil {
		return erp, resolveErr
	}
	return pk.Marshal(packetID, pk.ByteArray(cryptPK), pk.ByteArray(verifyT)), nil
}
