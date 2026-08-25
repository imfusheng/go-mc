package server

import (
	"bytes"
	stdnet "net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/offline"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

func TestOfflineServerLoginAcrossEveryNettyProfile(t *testing.T) {
	wantID := offline.NameToUUID("ProfileTest")
	for _, profile := range protocol.Profiles() {
		if profile.Key().Transport != protocol.TransportNetty {
			continue
		}
		profile := profile
		t.Run(profile.Version().Name, func(t *testing.T) {
			t.Parallel()
			serverSocket, clientSocket := stdnet.Pipe()
			t.Cleanup(func() {
				_ = serverSocket.Close()
				_ = clientSocket.Close()
			})
			serverConn := mcnet.WrapConn(serverSocket)
			clientConn := mcnet.WrapConn(clientSocket)
			handler := MojangLoginHandler{Threshold: -1}

			type loginResult struct {
				name       string
				id         uuid.UUID
				key        *user.PublicKey
				properties []user.Property
				err        error
			}
			result := make(chan loginResult, 1)
			go func() {
				name, id, key, properties, err := handler.AcceptLogin(serverConn, profile.Key().Protocol)
				result <- loginResult{name: name, id: id, key: key, properties: properties, err: err}
			}()

			if err := clientConn.WritePacket(loginStartFixture(profile)); err != nil {
				t.Fatalf("write Login Start: %v", err)
			}
			var success pk.Packet
			if err := clientConn.ReadPacket(&success); err != nil {
				t.Fatalf("read Login Success: %v", err)
			}
			successID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSuccess)
			if err != nil {
				t.Fatalf("resolve Login Success: %v", err)
			}
			if success.ID != successID {
				t.Fatalf("Login Success ID = %d, want %d", success.ID, successID)
			}
			assertLoginSuccessFixture(t, profile, success.Data, wantID)

			if profile.HasLoginAcknowledgement() {
				ackID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginAcknowledged)
				if err != nil {
					t.Fatalf("resolve Login Acknowledged: %v", err)
				}
				if err := clientConn.WritePacket(pk.Marshal(ackID)); err != nil {
					t.Fatalf("write Login Acknowledged: %v", err)
				}
			}

			select {
			case got := <-result:
				if got.err != nil {
					t.Fatalf("AcceptLogin: %v", got.err)
				}
				if got.name != "ProfileTest" || got.id != wantID || got.key != nil || len(got.properties) != 0 {
					t.Fatalf("result = name:%q id:%s key:%v properties:%v", got.name, got.id, got.key, got.properties)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("AcceptLogin did not complete")
			}
		})
	}
}

func TestPre18LoginOmitsUnavailableCompressionPacket(t *testing.T) {
	for _, version := range []string{"1.7.5", "1.7.10"} {
		t.Run(version, func(t *testing.T) {
			profile := protocol.MustByName(version)
			serverSocket, clientSocket := stdnet.Pipe()
			t.Cleanup(func() {
				_ = serverSocket.Close()
				_ = clientSocket.Close()
			})
			serverConn := mcnet.WrapConn(serverSocket)
			clientConn := mcnet.WrapConn(clientSocket)
			handler := MojangLoginHandler{Threshold: 0}
			result := make(chan error, 1)
			go func() {
				_, _, _, _, err := handler.AcceptLogin(serverConn, profile.Key().Protocol)
				result <- err
			}()

			if err := clientConn.WritePacket(loginStartFixture(profile)); err != nil {
				t.Fatalf("write Login Start: %v", err)
			}
			var response pk.Packet
			if err := clientConn.ReadPacket(&response); err != nil {
				t.Fatalf("read Login Success: %v", err)
			}
			want := mustPacketID(t, profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSuccess)
			if response.ID != want {
				t.Fatalf("first response ID = %d, want Login Success %d", response.ID, want)
			}
			if err := <-result; err != nil {
				t.Fatalf("AcceptLogin: %v", err)
			}
		})
	}
}

func loginStartFixture(profile *protocol.Profile) pk.Packet {
	fields := []pk.FieldEncoder{pk.String("ProfileTest")}
	switch profile.LoginStartStyle() {
	case protocol.LoginStartNameOnly:
	case protocol.LoginStartNameAndOptionalSignature:
		fields = append(fields, pk.Boolean(false))
	case protocol.LoginStartNameSignatureAndOptionalUUID:
		fields = append(fields, pk.Boolean(false), pk.Boolean(false))
	case protocol.LoginStartNameAndOptionalUUID:
		fields = append(fields, pk.Boolean(false))
	case protocol.LoginStartNameAndUUID:
		fields = append(fields, pk.UUID(uuid.Nil))
	default:
		panic("unsupported Login Start style")
	}
	packetID, err := protocol.RequirePacketID(profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginStart)
	if err != nil {
		panic(err)
	}
	return pk.Marshal(packetID, fields...)
}

func assertLoginSuccessFixture(t *testing.T, profile *protocol.Profile, data []byte, wantID uuid.UUID) {
	t.Helper()
	r := bytes.NewReader(data)
	if profile.LoginSuccessUsesStringUUID() {
		var id pk.String
		if _, err := id.ReadFrom(r); err != nil {
			t.Fatalf("read string UUID: %v", err)
		}
		wantWireID := wantID.String()
		if !profile.LoginSuccessStringUUIDUsesDashes() {
			wantWireID = strings.ReplaceAll(wantWireID, "-", "")
		}
		if string(id) != wantWireID {
			t.Fatalf("wire UUID = %q, want %q", id, wantWireID)
		}
		parsed, err := uuid.Parse(string(id))
		if err != nil || parsed != wantID {
			t.Fatalf("UUID = %q (%v), want %s", id, err, wantID)
		}
	} else {
		var id pk.UUID
		if _, err := id.ReadFrom(r); err != nil {
			t.Fatalf("read UUID: %v", err)
		}
		if uuid.UUID(id) != wantID {
			t.Fatalf("UUID = %s, want %s", uuid.UUID(id), wantID)
		}
	}
	var name pk.String
	if _, err := name.ReadFrom(r); err != nil || name != "ProfileTest" {
		t.Fatalf("name = %q, err %v", name, err)
	}
	if profile.LoginSuccessHasProperties() {
		var properties []user.Property
		if _, err := pk.Array(&properties).ReadFrom(r); err != nil {
			t.Fatalf("read properties: %v", err)
		}
		if len(properties) != 0 {
			t.Fatalf("properties = %v", properties)
		}
	}
	if profile.LoginSuccessHasStrictErrorHandling() {
		var strict pk.Boolean
		if _, err := strict.ReadFrom(r); err != nil {
			t.Fatalf("read strict error handling: %v", err)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("Login Success left %d trailing bytes", r.Len())
	}
}
