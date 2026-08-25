package server

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/data/packetid"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

func TestEnforceSecureProfileRejectsMissingKey(t *testing.T) {
	for _, test := range []struct {
		version         string
		wantUnsupported bool
	}{
		{version: "1.19"},
		{version: "1.21.1", wantUnsupported: true},
	} {
		t.Run(test.version, func(t *testing.T) {
			profile := protocol.MustByName(test.version)
			serverConn, clientConn := pipeMC(t)
			handler := MojangLoginHandler{EnforceSecureProfile: true, Threshold: -1}
			result := make(chan error, 1)
			go func() {
				_, _, _, _, err := handler.AcceptLogin(serverConn, profile.Key().Protocol)
				result <- err
			}()
			if err := clientConn.WritePacket(loginStartFixture(profile)); err != nil {
				t.Fatalf("write Login Start: %v", err)
			}

			select {
			case err := <-result:
				if test.wantUnsupported {
					var unsupported protocol.UnsupportedCapabilityError
					if !errors.As(err, &unsupported) {
						t.Fatalf("AcceptLogin() error = %v, want UnsupportedCapabilityError", err)
					}
				} else {
					var rejected LoginFailErr
					if !errors.As(err, &rejected) {
						t.Fatalf("AcceptLogin() error = %v, want LoginFailErr", err)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("AcceptLogin() did not reject the missing key")
			}
		})
	}
}

func TestReadLoginStartRejectsMalformedLengthAndTrailingData(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	negativeName := pk.Marshal(packetid.ServerboundLoginHello, pk.VarInt(-1))
	if _, _, _, err := readLoginStart(profile, negativeName); err == nil {
		t.Fatal("readLoginStart() accepted a negative name length")
	}

	trailing := pk.Marshal(
		packetid.ServerboundLoginHello,
		pk.String("ProfileTest"),
		pk.UUID(uuid.Nil),
		pk.Byte(1),
	)
	if _, _, _, err := readLoginStart(profile, trailing); err == nil {
		t.Fatal("readLoginStart() accepted trailing data")
	}
}

func TestWriteLoginDisconnectEncodingBoundary(t *testing.T) {
	reason := chat.Text("version boundary").Append(chat.Text(" child"))
	for _, version := range []string{"1.20.2", "1.20.3"} {
		t.Run(version, func(t *testing.T) {
			profile := protocol.MustByName(version)
			serverConn, clientConn := pipeMC(t)
			result := make(chan error, 1)
			go func() { result <- writeLoginDisconnect(serverConn, profile.Key().Protocol, reason) }()

			var packet pk.Packet
			if err := clientConn.ReadPacket(&packet); err != nil {
				t.Fatalf("read disconnect: %v", err)
			}
			if packet.ID != int32(packetid.ClientboundLoginLoginDisconnect) {
				t.Fatalf("packet ID = %d", packet.ID)
			}
			r := bytes.NewReader(packet.Data)
			var decoded chat.JsonMessage
			if _, err := decoded.ReadFrom(r); err != nil {
				t.Fatalf("read JSON disconnect: %v", err)
			}
			if r.Len() != 0 {
				t.Fatalf("disconnect left %d bytes", r.Len())
			}
			if err := <-result; err != nil {
				t.Fatalf("writeLoginDisconnect() error = %v", err)
			}
		})
	}
}

func TestAcceptConnRejectsLegacyTransportBeforeLogin(t *testing.T) {
	profile := protocol.MustByName("1.6.4")
	serverConn, clientConn := pipeMC(t)
	loginCalled := make(chan struct{}, 1)
	server := Server{
		LoginHandler: loginHandlerFunc(func(*mcnet.Conn, int32) (string, uuid.UUID, *user.PublicKey, []user.Property, error) {
			loginCalled <- struct{}{}
			return "", uuid.Nil, nil, nil, nil
		}),
		GamePlay: gamePlayFunc(func(string, uuid.UUID, *user.PublicKey, []user.Property, int32, *mcnet.Conn) {}),
	}
	done := make(chan struct{})
	go func() {
		server.AcceptConn(serverConn)
		close(done)
	}()
	if err := clientConn.WritePacket(pk.Marshal(
		0,
		pk.VarInt(profile.Key().Protocol),
		pk.String("localhost"),
		pk.UnsignedShort(25565),
		pk.VarInt(2),
	)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AcceptConn() did not reject legacy transport")
	}
	select {
	case <-loginCalled:
		t.Fatal("legacy transport reached Netty LoginHandler")
	default:
	}
}
