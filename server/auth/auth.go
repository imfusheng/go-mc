package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/net"
	"github.com/imfusheng/go-mc/net/CFB8"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

// Deprecated: Moved to go-mc/yggdrasil/user, because go-mc/bot also needs them
type (
	Texture   = user.Texture
	Property  = user.Property
	PublicKey = user.PublicKey
)

const verifyTokenLen = 16

// Encrypt a connection, with authentication
func Encrypt(conn *net.Conn, name string, serverKey *rsa.PrivateKey) (*Resp, error) {
	return EncryptForProfile(conn, name, serverKey, protocol.MustByName("1.21.1"))
}

// EncryptForProfile authenticates and encrypts a connection using the login
// packet schema selected by profile.
func EncryptForProfile(conn *net.Conn, name string, serverKey *rsa.PrivateKey, profile *protocol.Profile) (*Resp, error) {
	if conn == nil {
		return nil, errors.New("nil connection")
	}
	if serverKey == nil || serverKey.N == nil {
		return nil, errors.New("nil server RSA key")
	}
	if profile == nil {
		_, mappingErr := protocol.RequirePacketID(nil, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginEncryptionRequest)
		return nil, mappingErr
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&serverKey.PublicKey)
	if err != nil {
		return nil, err
	}

	verifyToken := make([]byte, verifyTokenLen)
	_, err = rand.Read(verifyToken)
	if err != nil {
		return nil, err
	}

	// encryption request
	err = encryptionRequest(conn, publicKey, verifyToken, profile)
	if err != nil {
		return nil, err
	}

	// encryption response
	SharedSecret, err := encryptionResponse(conn, serverKey, verifyToken, profile)
	if err != nil {
		return nil, err
	}

	// encryption the connection
	block, err := aes.NewCipher(SharedSecret)
	if err != nil {
		return nil, errors.New("load aes encryption key fail")
	}

	conn.SetCipher( // 启用加密
		CFB8.NewCFB8Encrypt(block, SharedSecret),
		CFB8.NewCFB8Decrypt(block, SharedSecret),
	)
	hash := authDigest("", SharedSecret, publicKey)
	resp, err := authentication(name, hash) // auth
	if err != nil {
		return nil, fmt.Errorf("authenticate session: %w", err)
	}

	return resp, nil
}

func encryptionRequest(conn *net.Conn, publicKey, verifyToken []byte, profile *protocol.Profile) error {
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginEncryptionRequest)
	if err != nil {
		return err
	}
	if profile.LoginByteArraysUseShortLength() {
		return conn.WritePacket(pk.Marshal(
			packetID,
			pk.String(""),
			shortByteArray(publicKey),
			shortByteArray(verifyToken),
		))
	}
	fields := []pk.FieldEncoder{
		pk.String(""),
		pk.ByteArray(publicKey),
		pk.ByteArray(verifyToken),
	}
	if profile.EncryptionRequestHasShouldAuthenticate() {
		fields = append(fields, pk.Boolean(true))
	}
	return conn.WritePacket(pk.Marshal(packetID, fields...))
}

func encryptionResponse(conn *net.Conn, serverKey *rsa.PrivateKey, verifyToken []byte, profile *protocol.Profile) ([]byte, error) {
	if conn == nil || serverKey == nil || serverKey.N == nil {
		return nil, errors.New("invalid encryption response arguments")
	}
	if profile == nil {
		_, mappingErr := protocol.RequirePacketID(nil, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginEncryptionResponse)
		return nil, mappingErr
	}
	var p pk.Packet
	err := conn.ReadPacket(&p)
	if err != nil {
		return nil, err
	}
	kind, err := protocol.RequirePacketKind(profile, protocol.StateLogin, protocol.Serverbound, p.ID)
	if err != nil {
		return nil, err
	}
	if kind != protocol.PacketLoginEncryptionResponse {
		return nil, fmt.Errorf("packet %q (0x%02X) is not Encryption Response", kind, p.ID)
	}

	var keyBytes, encryptedVerifyToken []byte
	r := bytes.NewReader(p.Data)
	if profile.LoginByteArraysUseShortLength() {
		var key, token shortByteArray
		if _, err = key.ReadFrom(r); err == nil {
			_, err = token.ReadFrom(r)
		}
		keyBytes, encryptedVerifyToken = key, token
	} else if profile.EncryptionResponseUsesVerifyTokenOption() {
		var hasVerifyToken pk.Boolean
		if keyBytes, err = readBoundedAuthByteArray(r, maxEncryptedLoginFieldLength, "shared secret"); err == nil {
			_, err = hasVerifyToken.ReadFrom(r)
		}
		if err == nil && !hasVerifyToken {
			return nil, errors.New("signed nonce encryption response is not supported")
		}
		if err == nil {
			encryptedVerifyToken, err = readBoundedAuthByteArray(r, maxEncryptedLoginFieldLength, "verify token")
		}
	} else {
		keyBytes, err = readBoundedAuthByteArray(r, maxEncryptedLoginFieldLength, "shared secret")
		if err == nil {
			encryptedVerifyToken, err = readBoundedAuthByteArray(r, maxEncryptedLoginFieldLength, "verify token")
		}
	}
	if err != nil {
		return nil, err
	}
	if len(keyBytes) > maxEncryptedLoginFieldLength || len(encryptedVerifyToken) > maxEncryptedLoginFieldLength {
		return nil, errors.New("encrypted login field is too long")
	}
	if len(keyBytes) != serverKey.Size() || len(encryptedVerifyToken) != serverKey.Size() {
		return nil, fmt.Errorf("invalid RSA ciphertext lengths: secret=%d token=%d want=%d", len(keyBytes), len(encryptedVerifyToken), serverKey.Size())
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("encryption response contains %d trailing bytes", r.Len())
	}

	// confirm to verify token
	decryptedVerifyToken, err := rsa.DecryptPKCS1v15(rand.Reader, serverKey, encryptedVerifyToken)
	if err != nil {
		return nil, err
	} else if !bytes.Equal(verifyToken, decryptedVerifyToken) {
		return nil, errors.New("verifyToken not match")
	}

	// get sharedSecret
	sharedSecret, err := rsa.DecryptPKCS1v15(rand.Reader, serverKey, keyBytes)
	if err != nil {
		return nil, err
	}
	if len(sharedSecret) != 16 {
		return nil, fmt.Errorf("invalid shared secret length %d", len(sharedSecret))
	}

	return sharedSecret, nil
}

const maxEncryptedLoginFieldLength = 8192

func readBoundedAuthByteArray(r *bytes.Reader, max int, field string) ([]byte, error) {
	var length pk.VarInt
	if _, err := length.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("read encrypted %s length: %w", field, err)
	}
	if length < 0 || int64(length) > int64(max) {
		return nil, fmt.Errorf("invalid encrypted %s length %d (maximum %d)", field, length, max)
	}
	if int(length) > r.Len() {
		return nil, fmt.Errorf("encrypted %s length %d exceeds remaining packet data %d", field, length, r.Len())
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, err
	}
	return value, nil
}

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

var sessionHTTPClient = &http.Client{Timeout: 15 * time.Second}

func authentication(name, hash string) (*Resp, error) {
	query := url.Values{
		"username": {name},
		"serverId": {hash},
	}
	request, err := http.NewRequest(http.MethodGet, "https://sessionserver.mojang.com/session/minecraft/hasJoined?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := sessionHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("session server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var result Resp
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode session response: %w", err)
	}
	if result.ID == uuid.Nil || result.Name == "" {
		return nil, errors.New("session response is missing profile identity")
	}

	return &result, nil
}

// authDigest computes a special SHA-1 digest required for Minecraft web
// authentication on Premium servers (online-mode=true).
// Source: http://wiki.vg/Protocol_Encryption#Server
//
// Also many, many thanks to SirCmpwn and his wonderful gist (C#):
// https://gist.github.com/SirCmpwn/404223052379e82f91e6
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
	res := strings.TrimLeft(fmt.Sprintf("%x", hash), "0")
	if negative {
		res = "-" + res
	}

	return res
}

// little endian
func twosComplement(p []byte) []byte {
	carry := true
	for i := len(p) - 1; i >= 0; i-- {
		p[i] = byte(^p[i])
		if carry {
			carry = p[i] == 0xff
			p[i]++
		}
	}
	return p
}

// Resp is the response of authentication
type Resp struct {
	Name       string
	ID         uuid.UUID
	Properties []user.Property
}

// Texture unmarshal the base64 encoded texture of Resp
func (r *Resp) Texture() (t user.Texture, err error) {
	if r == nil || len(r.Properties) == 0 {
		return t, errors.New("profile has no texture property")
	}
	var encoded string
	for _, property := range r.Properties {
		if property.Name == "textures" {
			encoded = property.Value
			break
		}
	}
	if encoded == "" {
		return t, errors.New("profile has no texture property")
	}
	var texture []byte
	texture, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return
	}

	err = json.Unmarshal(texture, &t)
	return
}
