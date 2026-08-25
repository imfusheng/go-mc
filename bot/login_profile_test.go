package bot

import (
	"bytes"
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
