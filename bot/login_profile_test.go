package bot

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"errors"
	stdnet "net"
	"testing"
	"time"

	"github.com/google/uuid"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

func TestLoginStartSelectsVersionedProfileKeySignature(t *testing.T) {
	profileID := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	var keyPair user.KeyPairResp
	keyPair.KeyPair.PublicKey = string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PUBLIC KEY", Bytes: []byte{0x01, 0x02, 0x03},
	}))
	keyPair.PublicKeySignature = base64.StdEncoding.EncodeToString([]byte{0x19})
	keyPair.PublicKeySignatureV2 = base64.StdEncoding.EncodeToString([]byte{0x20})
	keyPair.ExpiresAt = time.UnixMilli(1234)

	for _, test := range []struct {
		version       string
		wantSignature byte
		wantUUID      bool
	}{
		{version: "1.19", wantSignature: 0x19},
		{version: "1.19.1", wantSignature: 0x20, wantUUID: true},
	} {
		t.Run(test.version, func(t *testing.T) {
			profile := protocol.MustByName(test.version)
			client := NewClient()
			client.Auth.Name = "ProfileTest"
			client.Auth.UUID = profileID.String()
			client.UUID = profileID
			packet, err := client.loginStartPacket(profile, JoinOptions{KeyPair: &keyPair})
			if err != nil {
				t.Fatalf("loginStartPacket: %v", err)
			}
			r := bytes.NewReader(packet.Data)
			var name pk.String
			var hasKey pk.Boolean
			if _, err := (pk.Tuple{&name, &hasKey}).ReadFrom(r); err != nil {
				t.Fatalf("read Login Start prefix: %v", err)
			}
			if name != "ProfileTest" || !hasKey {
				t.Fatalf("Login Start prefix = name %q key %t", name, hasKey)
			}
			var expires pk.Long
			var encodedKey, signature pk.ByteArray
			if _, err := (pk.Tuple{&expires, &encodedKey, &signature}).ReadFrom(r); err != nil {
				t.Fatalf("read profile key: %v", err)
			}
			if expires != 1234 || !bytes.Equal(encodedKey, []byte{1, 2, 3}) ||
				!bytes.Equal(signature, []byte{test.wantSignature}) {
				t.Fatalf("profile key = expiry %d key %x signature %x", expires, encodedKey, signature)
			}
			if test.wantUUID {
				var hasUUID pk.Boolean
				var gotUUID pk.UUID
				if _, err := (pk.Tuple{&hasUUID, &gotUUID}).ReadFrom(r); err != nil {
					t.Fatalf("read profile UUID: %v", err)
				}
				if !hasUUID || uuid.UUID(gotUUID) != profileID {
					t.Fatalf("profile UUID = present %t ID %s", hasUUID, uuid.UUID(gotUUID))
				}
			}
			if r.Len() != 0 {
				t.Fatalf("Login Start left %d trailing bytes", r.Len())
			}
		})
	}
}

func TestLoginPluginResponseFailsOnProtocol485MappingGap(t *testing.T) {
	profile := protocol.MustByName("1.14.2")
	clientSocket, serverSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = clientSocket.Close()
		_ = serverSocket.Close()
	})
	clientConn := mcnet.WrapConn(clientSocket)
	serverConn := mcnet.WrapConn(serverSocket)
	client := NewClient()
	client.Auth.Name = "ProfileTest"

	result := make(chan error, 1)
	go func() {
		result <- client.joinLogin(clientConn, profile, JoinOptions{NoPublicKey: true})
	}()

	var start pk.Packet
	if err := serverConn.ReadPacket(&start); err != nil {
		t.Fatalf("read Login Start: %v", err)
	}
	requestID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginPluginRequest)
	if err != nil {
		t.Fatalf("resolve Login Plugin Request: %v", err)
	}
	if err := serverConn.WritePacket(pk.Marshal(
		requestID,
		pk.VarInt(7),
		pk.Identifier("test:channel"),
		pk.PluginMessageData{0x01, 0x02},
	)); err != nil {
		t.Fatalf("write Login Plugin Request: %v", err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, protocol.ErrPacketMappingUnavailable) {
			t.Fatalf("joinLogin() error = %v; want ErrPacketMappingUnavailable", err)
		}
		var mappingErr *protocol.PacketMappingError
		if !errors.As(err, &mappingErr) || mappingErr.Kind != protocol.PacketLoginPluginResponse {
			t.Fatalf("joinLogin() mapping error = %#v", mappingErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("joinLogin did not report protocol-485 packet-map gap")
	}
}

func TestClientLoginStateAcrossEveryNettyProfile(t *testing.T) {
	wantID := uuid.MustParse("12345678-1234-5678-90ab-cdef12345678")
	for _, profile := range protocol.Profiles() {
		if profile.Key().Transport != protocol.TransportNetty {
			continue
		}
		profile := profile
		t.Run(profile.Version().Name, func(t *testing.T) {
			t.Parallel()
			clientSocket, serverSocket := stdnet.Pipe()
			t.Cleanup(func() {
				_ = clientSocket.Close()
				_ = serverSocket.Close()
			})
			clientConn := mcnet.WrapConn(clientSocket)
			serverConn := mcnet.WrapConn(serverSocket)

			client := NewClient()
			client.Auth.Name = "ProfileTest"
			result := make(chan error, 1)
			go func() {
				result <- client.joinLogin(clientConn, profile, JoinOptions{NoPublicKey: true})
			}()

			var start pk.Packet
			if err := serverConn.ReadPacket(&start); err != nil {
				t.Fatalf("read Login Start: %v", err)
			}
			startID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginStart)
			if err != nil {
				t.Fatalf("resolve Login Start: %v", err)
			}
			if start.ID != startID {
				t.Fatalf("Login Start ID = %d, want %d", start.ID, startID)
			}
			assertLoginStartFixture(t, profile, start.Data)

			fields := make([]pk.FieldEncoder, 0, 4)
			if profile.LoginSuccessUsesStringUUID() {
				fields = append(fields, pk.String(wantID.String()))
			} else {
				fields = append(fields, pk.UUID(wantID))
			}
			fields = append(fields, pk.String("ProfileTest"))
			if profile.LoginSuccessHasProperties() {
				fields = append(fields, pk.Array([]user.Property{}))
			}
			if profile.LoginSuccessHasStrictErrorHandling() {
				fields = append(fields, pk.Boolean(false))
			}
			successID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSuccess)
			if err != nil {
				t.Fatalf("resolve Login Success: %v", err)
			}
			if err := serverConn.WritePacket(pk.Marshal(successID, fields...)); err != nil {
				t.Fatalf("write Login Success: %v", err)
			}

			if profile.HasLoginAcknowledgement() {
				var ack pk.Packet
				if err := serverConn.ReadPacket(&ack); err != nil {
					t.Fatalf("read Login Acknowledged: %v", err)
				}
				ackID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginAcknowledged)
				if err != nil {
					t.Fatalf("resolve Login Acknowledged: %v", err)
				}
				if ack.ID != ackID || len(ack.Data) != 0 {
					t.Fatalf("Login Acknowledged = ID %d data %x", ack.ID, ack.Data)
				}
			}

			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("joinLogin: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("joinLogin did not complete")
			}
			if client.UUID != wantID || client.Name != "ProfileTest" {
				t.Fatalf("profile = %s/%s", client.UUID, client.Name)
			}
		})
	}
}

func assertLoginStartFixture(t *testing.T, profile *protocol.Profile, data []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	var name pk.String
	if _, err := name.ReadFrom(r); err != nil {
		t.Fatalf("read username: %v", err)
	}
	if name != "ProfileTest" {
		t.Fatalf("username = %q", name)
	}

	readAbsent := func(label string) {
		t.Helper()
		var present pk.Boolean
		if _, err := present.ReadFrom(r); err != nil {
			t.Fatalf("read %s presence: %v", label, err)
		}
		if present {
			t.Fatalf("%s unexpectedly present", label)
		}
	}
	switch profile.LoginStartStyle() {
	case protocol.LoginStartNameOnly:
	case protocol.LoginStartNameAndOptionalSignature:
		readAbsent("profile key")
	case protocol.LoginStartNameSignatureAndOptionalUUID:
		readAbsent("profile key")
		readAbsent("UUID")
	case protocol.LoginStartNameAndOptionalUUID:
		readAbsent("UUID")
	case protocol.LoginStartNameAndUUID:
		var id pk.UUID
		if _, err := id.ReadFrom(r); err != nil {
			t.Fatalf("read UUID: %v", err)
		}
		if uuid.UUID(id) != uuid.Nil {
			t.Fatalf("UUID = %s, want nil UUID", uuid.UUID(id))
		}
	default:
		t.Fatalf("unsupported Login Start style %d", profile.LoginStartStyle())
	}
	if r.Len() != 0 {
		t.Fatalf("Login Start left %d trailing bytes", r.Len())
	}
}
