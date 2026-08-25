package auth

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	stdnet "net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

func TestResp(t *testing.T) {
	var resp Resp
	err := json.Unmarshal([]byte(`{"id":"853c80ef3c3749fdaa49938b674adae6","name":"jeb_","properties":[{"name":"textures","value":"eyJ0aW1lc3RhbXAiOjE1NTk1NDM5MzMwMjUsInByb2ZpbGVJZCI6Ijg1M2M4MGVmM2MzNzQ5ZmRhYTQ5OTM4YjY3NGFkYWU2IiwicHJvZmlsZU5hbWUiOiJqZWJfIiwidGV4dHVyZXMiOnsiU0tJTiI6eyJ1cmwiOiJodHRwOi8vdGV4dHVyZXMubWluZWNyYWZ0Lm5ldC90ZXh0dXJlLzdmZDliYTQyYTdjODFlZWVhMjJmMTUyNDI3MWFlODVhOGUwNDVjZTBhZjVhNmFlMTZjNjQwNmFlOTE3ZTY4YjUifSwiQ0FQRSI6eyJ1cmwiOiJodHRwOi8vdGV4dHVyZXMubWluZWNyYWZ0Lm5ldC90ZXh0dXJlLzU3ODZmZTk5YmUzNzdkZmI2ODU4ODU5ZjkyNmM0ZGJjOTk1NzUxZTkxY2VlMzczNDY4YzVmYmY0ODY1ZTcxNTEifX19"}]}`), &resp)
	if err != nil {
		panic(err)
	}
	wantID := uuid.Must(uuid.Parse("853c80ef3c3749fdaa49938b674adae6"))

	// check UUID
	if resp.ID != wantID {
		t.Errorf("uuid doesn't match: %v, want %s", resp.ID, wantID)
	}

	// check name
	if resp.Name != "jeb_" {
		t.Errorf("name doesn't match: %s, want %s", resp.Name, "jeb_")
	}

	// check texture
	texture, err := resp.Texture()
	if err != nil {
		t.Fatal(err)
	}

	t.Log(texture.TimeStamp)

	if texture.ID != wantID {
		t.Errorf("uuid doesn't match: %v, want %s", texture.ID, wantID)
	}

	if texture.Name != "jeb_" {
		t.Errorf("name doesn't match: %s, want %s", texture.Name, "jeb_")
	}

	const (
		wantSKIN = "http://textures.minecraft.net/texture/7fd9ba42a7c81eeea22f1524271ae85a8e045ce0af5a6ae16c6406ae917e68b5"
		wantCAPE = "http://textures.minecraft.net/texture/5786fe99be377dfb6858859f926c4dbc995751e91cee373468c5fbf4865e7151"
	)
	if texture.Textures.SKIN.URL != wantSKIN {
		t.Errorf("skin url not match: %s, want %s",
			texture.Textures.SKIN.URL,
			wantSKIN)
	}
	if texture.Textures.CAPE.URL != wantCAPE {
		t.Errorf("cape url not match: %s, want %s",
			texture.Textures.CAPE.URL,
			wantCAPE)
	}
}

func TestRespTextureHandlesMissingAndUnorderedProperties(t *testing.T) {
	if _, err := (*Resp)(nil).Texture(); err == nil {
		t.Fatal("nil Resp.Texture() succeeded")
	}
	if _, err := (&Resp{}).Texture(); err == nil {
		t.Fatal("empty Resp.Texture() succeeded")
	}

	resp := Resp{Properties: []user.Property{
		{Name: "unrelated", Value: "not base64"},
		{Name: "textures", Value: "e30="},
	}}
	if _, err := resp.Texture(); err != nil {
		t.Fatalf("Resp.Texture() error = %v", err)
	}
}

func TestReadBoundedAuthByteArrayRejectsNegativeLength(t *testing.T) {
	data := pk.Marshal(0, pk.VarInt(-1)).Data
	if _, err := readBoundedAuthByteArray(bytes.NewReader(data), 16, "test"); err == nil {
		t.Fatal("readBoundedAuthByteArray() accepted a negative length")
	}
}

func TestEncryptForProfileValidatesArguments(t *testing.T) {
	if _, err := EncryptForProfile(nil, "ProfileTest", nil, protocol.MustByName("1.21.1")); err == nil {
		t.Fatal("EncryptForProfile() accepted nil connection and key")
	}
}

func TestEncryptionResponseAcceptsProtocol759And760NonceChoices(t *testing.T) {
	serverKey := generateRSAKey(t, 1024)
	profileKey := generateRSAKey(t, 2048)
	verifyToken := []byte("0123456789abcdef")
	sharedSecret := []byte("fedcba9876543210")
	const salt int64 = -0x0102030405060708

	for _, protocolNumber := range []int32{759, 760} {
		profile := profileByProtocol(t, protocolNumber)
		t.Run(profile.Version().Name+"/signed", func(t *testing.T) {
			signature := signNonce(t, profileKey, verifyToken, salt)
			response := signedNonceResponse(t, profile, serverKey, sharedSecret, salt, signature)
			got, err := readEncryptionResponse(t, response, serverKey, verifyToken, profile, &profileKey.PublicKey)
			if err != nil {
				t.Fatalf("encryptionResponse() error = %v", err)
			}
			if !bytes.Equal(got, sharedSecret) {
				t.Fatalf("shared secret = %x, want %x", got, sharedSecret)
			}
		})

		t.Run(profile.Version().Name+"/encrypted-token", func(t *testing.T) {
			response := encryptedTokenResponse(t, profile, serverKey, sharedSecret, verifyToken)
			got, err := readEncryptionResponse(t, response, serverKey, verifyToken, profile, nil)
			if err != nil {
				t.Fatalf("encryptionResponse() error = %v", err)
			}
			if !bytes.Equal(got, sharedSecret) {
				t.Fatalf("shared secret = %x, want %x", got, sharedSecret)
			}
		})
	}
}

func TestEncryptionResponseRejectsSignedNonceOutsideProtocol759And760(t *testing.T) {
	serverKey := generateRSAKey(t, 1024)
	profileKey := generateRSAKey(t, 2048)
	verifyToken := []byte("0123456789abcdef")
	sharedSecret := []byte("fedcba9876543210")
	const salt int64 = 0x0102030405060708
	signature := signNonce(t, profileKey, verifyToken, salt)

	for _, protocolNumber := range []int32{758, 761} {
		profile := profileByProtocol(t, protocolNumber)
		response := signedNonceResponse(t, profile, serverKey, sharedSecret, salt, signature)
		if _, err := readEncryptionResponse(t, response, serverKey, verifyToken, profile, &profileKey.PublicKey); err == nil {
			t.Errorf("protocol %d accepted the protocol 759/760 signed nonce layout", protocolNumber)
		}
	}
}

func TestEncryptionResponseRejectsInvalidSignedNonce(t *testing.T) {
	serverKey := generateRSAKey(t, 1024)
	profileKey := generateRSAKey(t, 2048)
	profile := profileByProtocol(t, 759)
	verifyToken := []byte("0123456789abcdef")
	sharedSecret := []byte("fedcba9876543210")
	const salt int64 = -42
	validSignature := signNonce(t, profileKey, verifyToken, salt)

	tests := []struct {
		name        string
		key         *rsa.PublicKey
		token       []byte
		wireSalt    int64
		signature   []byte
		trailing    bool
		wantErrPart string
	}{
		{
			name:        "missing profile key",
			token:       verifyToken,
			wireSalt:    salt,
			signature:   validSignature,
			wantErrPart: "requires a profile public key",
		},
		{
			name:        "different verify token",
			key:         &profileKey.PublicKey,
			token:       []byte("0123456789abcdeg"),
			wireSalt:    salt,
			signature:   validSignature,
			wantErrPart: "verify signed nonce",
		},
		{
			name:        "different salt",
			key:         &profileKey.PublicKey,
			token:       verifyToken,
			wireSalt:    salt + 1,
			signature:   validSignature,
			wantErrPart: "verify signed nonce",
		},
		{
			name:        "short signature",
			key:         &profileKey.PublicKey,
			token:       verifyToken,
			wireSalt:    salt,
			signature:   validSignature[:len(validSignature)-1],
			wantErrPart: "signature length",
		},
		{
			name:        "trailing data",
			key:         &profileKey.PublicKey,
			token:       verifyToken,
			wireSalt:    salt,
			signature:   validSignature,
			trailing:    true,
			wantErrPart: "trailing bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := signedNonceResponse(t, profile, serverKey, sharedSecret, test.wireSalt, test.signature)
			if test.trailing {
				response.Data = append(response.Data, byte(0x7f))
			}
			_, err := readEncryptionResponse(t, response, serverKey, test.token, profile, test.key)
			if err == nil || !strings.Contains(err.Error(), test.wantErrPart) {
				t.Fatalf("encryptionResponse() error = %v, want error containing %q", err, test.wantErrPart)
			}
		})
	}
}

func generateRSAKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(%d): %v", bits, err)
	}
	return key
}

func profileByProtocol(t *testing.T, protocolNumber int32) *protocol.Profile {
	t.Helper()
	profile, ok := protocol.ByProtocol(protocolNumber)
	if !ok {
		t.Fatalf("missing protocol profile %d", protocolNumber)
	}
	return profile
}

func signNonce(t *testing.T, key *rsa.PrivateKey, verifyToken []byte, salt int64) []byte {
	t.Helper()
	var encodedSalt [8]byte
	binary.BigEndian.PutUint64(encodedSalt[:], uint64(salt))
	digest := sha256.Sum256(append(append([]byte(nil), verifyToken...), encodedSalt[:]...))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign nonce: %v", err)
	}
	return signature
}

func signedNonceResponse(t *testing.T, profile *protocol.Profile, serverKey *rsa.PrivateKey, sharedSecret []byte, salt int64, signature []byte) pk.Packet {
	t.Helper()
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginEncryptionResponse)
	if err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := rsa.EncryptPKCS1v15(rand.Reader, &serverKey.PublicKey, sharedSecret)
	if err != nil {
		t.Fatalf("encrypt shared secret: %v", err)
	}
	return pk.Marshal(
		packetID,
		pk.ByteArray(encryptedSecret),
		pk.Boolean(false),
		pk.Long(salt),
		pk.ByteArray(signature),
	)
}

func encryptedTokenResponse(t *testing.T, profile *protocol.Profile, serverKey *rsa.PrivateKey, sharedSecret, verifyToken []byte) pk.Packet {
	t.Helper()
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginEncryptionResponse)
	if err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := rsa.EncryptPKCS1v15(rand.Reader, &serverKey.PublicKey, sharedSecret)
	if err != nil {
		t.Fatalf("encrypt shared secret: %v", err)
	}
	encryptedToken, err := rsa.EncryptPKCS1v15(rand.Reader, &serverKey.PublicKey, verifyToken)
	if err != nil {
		t.Fatalf("encrypt verify token: %v", err)
	}
	return pk.Marshal(
		packetID,
		pk.ByteArray(encryptedSecret),
		pk.Boolean(true),
		pk.ByteArray(encryptedToken),
	)
}

func readEncryptionResponse(t *testing.T, response pk.Packet, serverKey *rsa.PrivateKey, verifyToken []byte, profile *protocol.Profile, profilePublicKey *rsa.PublicKey) ([]byte, error) {
	t.Helper()
	serverSocket, clientSocket := stdnet.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	if err := serverSocket.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := clientSocket.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- clientConn.WritePacket(response)
	}()
	secret, err := encryptionResponse(serverConn, serverKey, verifyToken, profile, profilePublicKey)
	if writeErr := <-writeDone; writeErr != nil {
		t.Fatalf("write response: %v", writeErr)
	}
	return secret, err
}
