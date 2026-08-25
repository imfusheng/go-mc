package server

import (
	"encoding/json"
	stdnet "net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

type profilePingHandler struct{}

func (profilePingHandler) Name() string                      { return "profile-test" }
func (profilePingHandler) Protocol(clientProtocol int32) int { return int(clientProtocol) }
func (profilePingHandler) MaxPlayer() int                    { return 20 }
func (profilePingHandler) OnlinePlayer() int                 { return 0 }
func (profilePingHandler) PlayerSamples() []PlayerSample     { return nil }
func (profilePingHandler) Description() *chat.Message        { value := chat.Text("test"); return &value }
func (profilePingHandler) FavIcon() string                   { return "" }

type profilePingHandlerWithSample struct{ profilePingHandler }

func (profilePingHandlerWithSample) OnlinePlayer() int { return 1 }
func (profilePingHandlerWithSample) PlayerSamples() []PlayerSample {
	return []PlayerSample{{Name: "ProfileTest", ID: uuid.MustParse("12345678-1234-5678-90ab-cdef12345678")}}
}

func TestServerStatusDispatchAcrossProfileBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		protocolNumber int32
		profile        *protocol.Profile
	}{
		{name: "protocol 4 audited mapping", protocolNumber: 4, profile: protocol.MustByName("1.7.5")},
		{name: "current", protocolNumber: 767, profile: protocol.MustByName("1.21.1")},
		{name: "latest", protocolNumber: 776, profile: protocol.MustByName("26.2")},
		{name: "unlisted proxy protocol", protocolNumber: 9999},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverSocket, clientSocket := stdnet.Pipe()
			t.Cleanup(func() {
				_ = serverSocket.Close()
				_ = clientSocket.Close()
			})
			serverConn := mcnet.WrapConn(serverSocket)
			clientConn := mcnet.WrapConn(clientSocket)
			server := Server{ListPingHandler: profilePingHandler{}}
			done := make(chan struct{})
			go func() {
				server.acceptListPing(serverConn, test.protocolNumber)
				close(done)
			}()

			requestID, pingID := int32(0), int32(1)
			responseID, pongID := int32(0), int32(1)
			if test.profile != nil {
				requestID = mustPacketID(t, test.profile, protocol.StateStatus, protocol.Serverbound, protocol.PacketStatusRequest)
				pingID = mustPacketID(t, test.profile, protocol.StateStatus, protocol.Serverbound, protocol.PacketStatusPing)
				responseID = mustPacketID(t, test.profile, protocol.StateStatus, protocol.Clientbound, protocol.PacketStatusResponse)
				pongID = mustPacketID(t, test.profile, protocol.StateStatus, protocol.Clientbound, protocol.PacketStatusPing)
			}

			if err := clientConn.WritePacket(pk.Marshal(requestID)); err != nil {
				t.Fatalf("write status request: %v", err)
			}
			var response pk.Packet
			if err := clientConn.ReadPacket(&response); err != nil {
				t.Fatalf("read status response: %v", err)
			}
			if response.ID != responseID {
				t.Fatalf("status response ID = %d, want %d", response.ID, responseID)
			}

			const nonce = pk.Long(123456789)
			if err := clientConn.WritePacket(pk.Marshal(pingID, nonce)); err != nil {
				t.Fatalf("write ping: %v", err)
			}
			var pong pk.Packet
			if err := clientConn.ReadPacket(&pong); err != nil {
				t.Fatalf("read pong: %v", err)
			}
			if pong.ID != pongID {
				t.Fatalf("pong ID = %d, want %d", pong.ID, pongID)
			}
			var gotNonce pk.Long
			if err := pong.Scan(&gotNonce); err != nil || gotNonce != nonce {
				t.Fatalf("pong nonce = %d, %v; want %d", gotNonce, err, nonce)
			}

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("status handler did not complete")
			}
		})
	}
}

func TestStatusSampleUUIDDashBoundary(t *testing.T) {
	server := Server{ListPingHandler: profilePingHandlerWithSample{}}
	for _, test := range []struct {
		protocol int32
		wantID   string
	}{
		{protocol: 4, wantID: "123456781234567890abcdef12345678"},
		{protocol: 5, wantID: "12345678-1234-5678-90ab-cdef12345678"},
	} {
		data, err := server.listResp(test.protocol)
		if err != nil {
			t.Fatalf("protocol %d listResp: %v", test.protocol, err)
		}
		var response struct {
			Players struct {
				Sample []struct {
					ID string `json:"id"`
				} `json:"sample"`
			} `json:"players"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatalf("protocol %d decode response %s: %v", test.protocol, data, err)
		}
		if len(response.Players.Sample) != 1 || response.Players.Sample[0].ID != test.wantID {
			t.Fatalf("protocol %d sample UUIDs = %+v, want %q", test.protocol, response.Players.Sample, test.wantID)
		}
		if test.protocol == 4 && strings.Contains(response.Players.Sample[0].ID, "-") {
			t.Fatal("protocol 4 status sample UUID contains dashes")
		}
	}
}

func mustPacketID(t *testing.T, profile *protocol.Profile, state protocol.State, direction protocol.Direction, kind protocol.PacketKind) int32 {
	t.Helper()
	id, err := protocol.RequirePacketID(profile, state, direction, kind)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
