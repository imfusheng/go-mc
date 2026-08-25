package server

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/offline"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/server/auth"
	"github.com/imfusheng/go-mc/yggdrasil/user"

	"github.com/google/uuid"
)

// LoginHandler is used to handle player login process, that is,
// from clientbound "LoginStart" packet to serverbound "LoginSuccess" packet.
type LoginHandler interface {
	AcceptLogin(conn *net.Conn, protocol int32) (name string, id uuid.UUID, profilePubKey *user.PublicKey, properties []user.Property, err error)
}

// LoginChecker is the interface to check if a player is allowed to log in the server.
// The checking could be anything, server player number, protocol version, blacklist or whitelist.
// If a player is not allowed to, the reason should be returned and will be sent to the client by "LoginDisconnect" packet.
type LoginChecker interface {
	CheckPlayer(name string, id uuid.UUID, protocol int32) (ok bool, reason chat.Message)
}

// Make sure MojangLoginHandler implement LoginHandler
var _ LoginHandler = (*MojangLoginHandler)(nil)

// MojangLoginHandler is a standard LoginHandler that implement both online and offline login progress.
// This implementation also supports custom LoginChecker.
// None of Custom login packets (also called LoginPluginRequest/Response) is supported for this implementation.
// To do that, implement your own LoginHandler imitate this code.
type MojangLoginHandler struct {
	// OnlineMode enables to check player's account.
	// And also encrypt the connection after login.
	OnlineMode bool

	// EnforceSecureProfile enforce to check the player's profile public key
	EnforceSecureProfile bool

	// Threshold set the smallest size of raw network payload to compress.
	// Set to 0 to compress all packets. Set to -1 to disable compression.
	Threshold int

	// LoginChecker is used to apply some checks before sending "LoginSuccess" packet
	// (e.g., blacklist or is server full).
	// This is an optional field and can be set to nil.
	LoginChecker

	// PrivateKey is the key used by encrypt the connection.
	privateKey     atomic.Pointer[rsa.PrivateKey]
	lockPrivateKey sync.Mutex
}

func (d *MojangLoginHandler) getPrivateKey() (key *rsa.PrivateKey, err error) {
	key = d.privateKey.Load()
	if key != nil {
		return
	}

	d.lockPrivateKey.Lock()
	defer d.lockPrivateKey.Unlock()

	key = d.privateKey.Load()
	if key == nil {
		key, err = rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			return
		}
		d.privateKey.Store(key)
	}
	return
}

/*
	var verifyToken [verifyTokenLen]byte
	_, err := rand.Read(verifyToken[:])
	if err != nil {
		return nil, err
	}
*/

// AcceptLogin implement LoginHandler for MojangLoginHandler
func (d *MojangLoginHandler) AcceptLogin(conn *net.Conn, protocolNumber int32) (name string, id uuid.UUID, profilePubKey *user.PublicKey, properties []user.Property, err error) {
	if conn == nil {
		err = fmt.Errorf("nil connection")
		return
	}
	wireProfile, ok := protocol.ByProtocol(protocolNumber)
	if !ok || wireProfile.Capabilities().Login == protocol.Unsupported {
		err = fmt.Errorf("unsupported Minecraft protocol %d", protocolNumber)
		return
	}

	// login start
	var p pk.Packet
	err = conn.ReadPacket(&p)
	if err != nil {
		return
	}
	var kind protocol.PacketKind
	kind, err = protocol.RequirePacketKind(wireProfile, protocol.StateLogin, protocol.Serverbound, p.ID)
	if err != nil {
		return
	}
	if kind != protocol.PacketLoginStart {
		var expected int32
		expected, err = protocol.RequirePacketID(wireProfile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginStart)
		if err == nil {
			err = wrongPacketErr{expect: expected, get: p.ID}
		}
		return
	}

	name, id, profilePubKey, err = readLoginStart(wireProfile, p)
	if err != nil {
		return
	}
	if d.EnforceSecureProfile {
		switch wireProfile.LoginStartStyle() {
		case protocol.LoginStartNameAndOptionalSignature:
			if profilePubKey == nil || !profilePubKey.VerifyV1() {
				err = LoginFailErr{reason: chat.Text("A valid, unexpired profile public key is required")}
				return
			}
		case protocol.LoginStartNameSignatureAndOptionalUUID:
			if profilePubKey == nil || id == uuid.Nil || !profilePubKey.VerifyV2(id) {
				err = LoginFailErr{reason: chat.Text("A valid, unexpired profile public key is required")}
				return
			}
		default:
			err = fmt.Errorf("enforce secure profile: %w", protocol.UnsupportedCapabilityError{
				Version: wireProfile.Version().Name, Capability: "login-state secure profile validation",
			})
			return
		}
	}

	// auth
	if d.OnlineMode {
		var serverKey *rsa.PrivateKey
		serverKey, err = d.getPrivateKey()
		if err != nil {
			return
		}
		var resp *auth.Resp
		// Auth, Encrypt
		var profileRSAKey *rsa.PublicKey
		if profilePubKey != nil {
			profileRSAKey = profilePubKey.PubKey
		}
		resp, err = auth.EncryptForProfileWithPublicKey(conn, name, serverKey, wireProfile, profileRSAKey)
		if err != nil {
			return
		}
		// Protocol 760 certificates bind the chat key to a UUID supplied by
		// the client. Revalidate it against the identity authenticated by
		// sessionserver before accepting that client-provided UUID.
		if d.EnforceSecureProfile && wireProfile.LoginStartStyle() == protocol.LoginStartNameSignatureAndOptionalUUID &&
			(profilePubKey == nil || !profilePubKey.VerifyV2(resp.ID)) {
			err = LoginFailErr{reason: chat.Text("Profile public key does not match the authenticated account")}
			return
		}
		name = resp.Name
		id = resp.ID
		properties = resp.Properties
	} else {
		// offline-mode UUID
		id = offline.NameToUUID(name)
	}

	// set compression
	if d.Threshold >= 0 {
		var compressionID int32
		compressionID, err = protocol.RequirePacketID(wireProfile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSetCompression)
		if err != nil {
			// Login compression was introduced with protocol 47 (Minecraft
			// 1.8). Earlier audited profiles omit the optional packet entirely.
			if wireProfile.Key().Transport != protocol.TransportNetty || wireProfile.Key().Protocol >= 47 {
				return
			}
			err = nil
		} else {
			err = conn.WritePacket(pk.Marshal(compressionID, pk.VarInt(d.Threshold)))
			if err != nil {
				return
			}
			conn.SetThreshold(d.Threshold)
		}
	}

	// check if player can join (whitelist, blacklist, server full or something else)
	if d.LoginChecker != nil {
		if ok, result := d.CheckPlayer(name, id, protocolNumber); !ok {
			// player is not allowed to join the server
			err = LoginFailErr{reason: result}
			return
		}
	}
	// send login success
	err = writeLoginSuccess(conn, wireProfile, id, name, properties)
	if err != nil {
		return
	}

	if wireProfile.HasLoginAcknowledgement() {
		// receive login ack
		err = conn.ReadPacket(&p)
		if err == nil {
			kind, err = protocol.RequirePacketKind(wireProfile, protocol.StateLogin, protocol.Serverbound, p.ID)
		}
		if err == nil && kind != protocol.PacketLoginAcknowledged {
			var expected int32
			expected, err = protocol.RequirePacketID(wireProfile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginAcknowledged)
			if err == nil {
				err = wrongPacketErr{expect: expected, get: p.ID}
			}
		}
		if err == nil && len(p.Data) != 0 {
			err = fmt.Errorf("login acknowledgement contains %d trailing bytes", len(p.Data))
		}
	}
	return
}

func readLoginStart(profile *protocol.Profile, p pk.Packet) (name string, id uuid.UUID, profilePubKey *user.PublicKey, err error) {
	if profile == nil {
		return "", uuid.Nil, nil, fmt.Errorf("nil protocol profile")
	}
	r := bytes.NewReader(p.Data)
	name, err = readLoginString(r, 64, "player name")
	if err != nil {
		return "", uuid.Nil, nil, err
	}
	if err = validatePlayerName(name); err != nil {
		return "", uuid.Nil, nil, err
	}
	switch profile.LoginStartStyle() {
	case protocol.LoginStartNameOnly:
	case protocol.LoginStartNameAndOptionalSignature:
		profilePubKey, err = readOptionalProfilePublicKey(r)
	case protocol.LoginStartNameSignatureAndOptionalUUID:
		if profilePubKey, err = readOptionalProfilePublicKey(r); err == nil {
			id, err = readOptionalLoginUUID(r)
		}
	case protocol.LoginStartNameAndOptionalUUID:
		id, err = readOptionalLoginUUID(r)
	case protocol.LoginStartNameAndUUID:
		var playerID pk.UUID
		_, err = playerID.ReadFrom(r)
		id = uuid.UUID(playerID)
	default:
		err = protocol.UnsupportedCapabilityError{Version: profile.Version().Name, Capability: "login start"}
	}
	if err != nil {
		return "", uuid.Nil, nil, err
	}
	if r.Len() != 0 {
		return "", uuid.Nil, nil, fmt.Errorf("login start contains %d trailing bytes", r.Len())
	}
	return name, id, profilePubKey, nil
}

const maxProfileKeyFieldLength = 8192

func validatePlayerName(name string) error {
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

func readOptionalProfilePublicKey(r *bytes.Reader) (*user.PublicKey, error) {
	var hasKey pk.Boolean
	if _, err := hasKey.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("read profile public key flag: %w", err)
	}
	if !hasKey {
		return nil, nil
	}
	var expiresAt pk.Long
	if _, err := expiresAt.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("read profile public key expiration: %w", err)
	}
	encoded, err := readLoginByteArray(r, maxProfileKeyFieldLength, "profile public key")
	if err != nil {
		return nil, err
	}
	signature, err := readLoginByteArray(r, maxProfileKeyFieldLength, "profile public key signature")
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKIXPublicKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("parse profile public key: %w", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("profile public key is %T, expected RSA", parsed)
	}
	return &user.PublicKey{
		ExpiresAt: time.UnixMilli(int64(expiresAt)),
		PubKey:    publicKey,
		Signature: signature,
	}, nil
}

func readOptionalLoginUUID(r *bytes.Reader) (uuid.UUID, error) {
	var hasID pk.Boolean
	if _, err := hasID.ReadFrom(r); err != nil {
		return uuid.Nil, fmt.Errorf("read profile UUID flag: %w", err)
	}
	if !hasID {
		return uuid.Nil, nil
	}
	var id pk.UUID
	if _, err := id.ReadFrom(r); err != nil {
		return uuid.Nil, fmt.Errorf("read profile UUID: %w", err)
	}
	return uuid.UUID(id), nil
}

func readLoginString(r *bytes.Reader, max int, field string) (string, error) {
	value, err := readLoginByteArray(r, max, field)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	return string(value), nil
}

func readLoginByteArray(r *bytes.Reader, max int, field string) ([]byte, error) {
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

func writeLoginSuccess(conn *net.Conn, profile *protocol.Profile, id uuid.UUID, name string, properties []user.Property) error {
	if conn == nil {
		return fmt.Errorf("nil connection")
	}
	if profile == nil {
		return fmt.Errorf("nil protocol profile")
	}
	if err := validatePlayerName(name); err != nil {
		return fmt.Errorf("invalid profile name: %w", err)
	}
	if len(properties) > maxLoginSuccessProperties {
		return fmt.Errorf("too many profile properties: %d (maximum %d)", len(properties), maxLoginSuccessProperties)
	}
	fields := make([]pk.FieldEncoder, 0, 4)
	if profile.LoginSuccessUsesStringUUID() {
		wireID := id.String()
		// Protocol 4 predates Mojang's switch to canonical dashed UUID
		// strings in 1.7.6 (protocol 5).
		if !profile.LoginSuccessStringUUIDUsesDashes() {
			wireID = strings.ReplaceAll(wireID, "-", "")
		}
		fields = append(fields, pk.String(wireID))
	} else {
		fields = append(fields, pk.UUID(id))
	}
	fields = append(fields, pk.String(name))
	if profile.LoginSuccessHasProperties() {
		fields = append(fields, pk.Array(properties))
	}
	if profile.LoginSuccessHasStrictErrorHandling() {
		fields = append(fields, pk.Boolean(false))
	}
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSuccess)
	if err != nil {
		return err
	}
	return conn.WritePacket(pk.Marshal(packetID, fields...))
}

const maxLoginSuccessProperties = 1024

func writeLoginDisconnect(conn *net.Conn, protocolNumber int32, reason chat.Message) error {
	if conn == nil {
		return fmt.Errorf("nil connection")
	}
	profile, ok := protocol.ByProtocol(protocolNumber)
	if !ok {
		return &protocol.PacketMappingError{
			Version:   fmt.Sprintf("protocol %d", protocolNumber),
			State:     protocol.StateLogin,
			Direction: protocol.Clientbound,
			Kind:      protocol.PacketLoginDisconnect,
		}
	}
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginDisconnect)
	if err != nil {
		return err
	}
	return conn.WritePacket(pk.Marshal(packetID, chat.JsonMessage(reason)))
}

// networkChatMessage suppresses chat.Message.MarshalNBT for configuration
// packets, whose anonymous NBT field already supplies the network-format root.
type networkChatMessage chat.Message

type GameProfile struct {
	ID   uuid.UUID
	Name string
}

type wrongPacketErr struct {
	expect, get int32
}

func (w wrongPacketErr) Error() string {
	return fmt.Sprintf("wrong packet id: expect %#02X, get %#02X", w.expect, w.get)
}

type LoginFailErr struct {
	reason chat.Message
}

func (l LoginFailErr) Error() string {
	return "login error: " + l.reason.ClearString()
}
