package bot

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"unicode/utf16"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

type pingPipeDialer struct {
	conn *mcnet.Conn
	addr string
}

func (d *pingPipeDialer) DialMCContext(_ context.Context, addr string) (*mcnet.Conn, error) {
	d.addr = addr
	return d.conn, nil
}

type pingStatus struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int32  `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description struct {
		Text string `json:"text"`
	} `json:"description"`
}

func TestPingAndListNettyUsesSelectedProtocol(t *testing.T) {
	const response = `{"version":{"name":"1.12.2","protocol":340},"players":{"max":20,"online":3},"description":{"text":"Modern"}}`
	clientSocket, serverSocket := net.Pipe()
	dialer := &pingPipeDialer{conn: mcnet.WrapConn(clientSocket)}
	serverErr := runPingServer(serverSocket, func(conn net.Conn) error {
		mcConn := mcnet.WrapConn(conn)

		var handshake pk.Packet
		if err := mcConn.ReadPacket(&handshake); err != nil {
			return fmt.Errorf("read handshake: %w", err)
		}
		if handshake.ID != 0 {
			return fmt.Errorf("handshake packet ID = %d, want 0", handshake.ID)
		}
		var protocolNumber pk.VarInt
		var host pk.String
		var port pk.UnsignedShort
		var nextState pk.VarInt
		if err := handshake.Scan(&protocolNumber, &host, &port, &nextState); err != nil {
			return fmt.Errorf("scan handshake: %w", err)
		}
		if protocolNumber != 340 || host != "modern.test" || port != 25570 || nextState != 1 {
			return fmt.Errorf(
				"handshake = protocol %d, host %q, port %d, state %d",
				protocolNumber, host, port, nextState,
			)
		}

		var request pk.Packet
		if err := mcConn.ReadPacket(&request); err != nil {
			return fmt.Errorf("read status request: %w", err)
		}
		if request.ID != 0 || len(request.Data) != 0 {
			return fmt.Errorf("status request = ID %d, data %x", request.ID, request.Data)
		}
		if err := mcConn.WritePacket(pk.Marshal(0, pk.String(response))); err != nil {
			return fmt.Errorf("write status response: %w", err)
		}

		if err := mcConn.ReadPacket(&request); err != nil {
			return fmt.Errorf("read ping request: %w", err)
		}
		if request.ID != 1 {
			return fmt.Errorf("ping request ID = %d, want 1", request.ID)
		}
		var nonce pk.Long
		if err := request.Scan(&nonce); err != nil {
			return fmt.Errorf("scan ping nonce: %w", err)
		}
		if err := mcConn.WritePacket(pk.Marshal(1, nonce)); err != nil {
			return fmt.Errorf("write pong response: %w", err)
		}
		return nil
	})

	data, _, clientErr := PingAndListContextWithOptions(context.Background(), "modern.test:25570", PingOptions{
		Version: protocol.Version{
			Name:      "1.12.2",
			Major:     "1.12",
			Protocol:  340,
			Transport: protocol.TransportNetty,
		},
		MCDialer: dialer,
	})
	if err := <-serverErr; err != nil {
		t.Fatalf("fake server: %v", err)
	}
	if clientErr != nil {
		t.Fatalf("PingAndListContextWithOptions: %v", clientErr)
	}
	if dialer.addr != "modern.test:25570" {
		t.Fatalf("dial address = %q, want modern.test:25570", dialer.addr)
	}
	if string(data) != response {
		t.Fatalf("response = %s, want %s", data, response)
	}
}

func TestPingAndListNettyAllowsUnlistedProtocol(t *testing.T) {
	const (
		protocolNumber = 9999
		response       = `{"version":{"name":"proxy","protocol":9999},"players":{"max":0,"online":0},"description":{"text":"Future"}}`
	)
	clientSocket, serverSocket := net.Pipe()
	dialer := &pingPipeDialer{conn: mcnet.WrapConn(clientSocket)}
	serverErr := runPingServer(serverSocket, func(conn net.Conn) error {
		mcConn := mcnet.WrapConn(conn)
		var packet pk.Packet
		if err := mcConn.ReadPacket(&packet); err != nil {
			return err
		}
		var gotProtocol pk.VarInt
		var host pk.String
		var port pk.UnsignedShort
		var nextState pk.VarInt
		if err := packet.Scan(&gotProtocol, &host, &port, &nextState); err != nil {
			return err
		}
		if packet.ID != 0 || gotProtocol != protocolNumber || nextState != 1 {
			return fmt.Errorf("handshake = ID %d protocol %d state %d", packet.ID, gotProtocol, nextState)
		}
		if err := mcConn.ReadPacket(&packet); err != nil {
			return err
		}
		if packet.ID != 0 || len(packet.Data) != 0 {
			return fmt.Errorf("status request = ID %d data %x", packet.ID, packet.Data)
		}
		if err := mcConn.WritePacket(pk.Marshal(0, pk.String(response))); err != nil {
			return err
		}
		if err := mcConn.ReadPacket(&packet); err != nil {
			return err
		}
		if packet.ID != 1 {
			return fmt.Errorf("ping request ID = %d", packet.ID)
		}
		var nonce pk.Long
		if err := packet.Scan(&nonce); err != nil {
			return err
		}
		return mcConn.WritePacket(pk.Marshal(1, nonce))
	})

	data, _, clientErr := PingAndListContextWithOptions(context.Background(), "future.test", PingOptions{
		Version: protocol.Version{
			Name:      "proxy-future",
			Major:     "proxy",
			Protocol:  protocolNumber,
			Transport: protocol.TransportNetty,
		},
		MCDialer: dialer,
	})
	if err := <-serverErr; err != nil {
		t.Fatalf("fake server: %v", err)
	}
	if clientErr != nil {
		t.Fatalf("PingAndListContextWithOptions: %v", clientErr)
	}
	if string(data) != response {
		t.Fatalf("response = %s, want %s", data, response)
	}
}

func TestPingAndListLegacyWireFamilies(t *testing.T) {
	tests := []struct {
		name         string
		addr         string
		version      protocol.Version
		request      []byte
		response     string
		wantVersion  string
		wantProtocol int32
		wantMOTD     string
		wantOnline   int
		wantMax      int
	}{
		{
			name: "1.3 and earlier FE",
			addr: "legacy.test",
			version: protocol.Version{
				Name:      "1.3.2",
				Major:     "1.3",
				Protocol:  39,
				Transport: protocol.TransportLegacy,
			},
			request:      []byte{0xFE},
			response:     "Legacy §aMOTD§5§20",
			wantVersion:  "1.3.2",
			wantProtocol: 39,
			wantMOTD:     "Legacy §aMOTD",
			wantOnline:   5,
			wantMax:      20,
		},
		{
			name: "1.4 through 1.5 FE 01",
			addr: "legacy.test:25565",
			version: protocol.Version{
				Name:      "1.5.2",
				Major:     "1.5",
				Protocol:  61,
				Transport: protocol.TransportLegacy,
			},
			request:      []byte{0xFE, 0x01},
			response:     "§1\x0061\x001.5.2\x00Legacy 1.5\x006\x0021",
			wantVersion:  "1.5.2",
			wantProtocol: 61,
			wantMOTD:     "Legacy 1.5",
			wantOnline:   6,
			wantMax:      21,
		},
		{
			name: "1.6 extended MC PingHost",
			addr: "legacy.test:25570",
			version: protocol.Version{
				Name:      "1.6.4",
				Major:     "1.6",
				Protocol:  78,
				Transport: protocol.TransportLegacy,
			},
			request: []byte{
				0xFE, 0x01, 0xFA,
				0x00, 0x0B,
				0x00, 0x4D, 0x00, 0x43, 0x00, 0x7C, 0x00, 0x50,
				0x00, 0x69, 0x00, 0x6E, 0x00, 0x67, 0x00, 0x48,
				0x00, 0x6F, 0x00, 0x73, 0x00, 0x74,
				0x00, 0x1D,
				0x4E,
				0x00, 0x0B,
				0x00, 0x6C, 0x00, 0x65, 0x00, 0x67, 0x00, 0x61,
				0x00, 0x63, 0x00, 0x79, 0x00, 0x2E, 0x00, 0x74,
				0x00, 0x65, 0x00, 0x73, 0x00, 0x74,
				0x00, 0x00, 0x63, 0xE2,
			},
			response:     "§1\x0078\x001.6.4\x00Legacy 1.6\x007\x0022",
			wantVersion:  "1.6.4",
			wantProtocol: 78,
			wantMOTD:     "Legacy 1.6",
			wantOnline:   7,
			wantMax:      22,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientSocket, serverSocket := net.Pipe()
			dialer := &pingPipeDialer{conn: mcnet.WrapConn(clientSocket)}
			serverErr := runPingServer(serverSocket, func(conn net.Conn) error {
				request := make([]byte, len(test.request))
				if _, err := io.ReadFull(conn, request); err != nil {
					return fmt.Errorf("read request: %w", err)
				}
				if !bytes.Equal(request, test.request) {
					return fmt.Errorf("request = % x, want % x", request, test.request)
				}
				if _, err := conn.Write(legacyResponseFixture(test.response)); err != nil {
					return fmt.Errorf("write response: %w", err)
				}
				return nil
			})

			data, _, clientErr := PingAndListContextWithOptions(context.Background(), test.addr, PingOptions{
				Version:  test.version,
				MCDialer: dialer,
			})
			if err := <-serverErr; err != nil {
				t.Fatalf("fake server: %v", err)
			}
			if clientErr != nil {
				t.Fatalf("PingAndListContextWithOptions: %v", clientErr)
			}

			var status pingStatus
			if err := json.Unmarshal(data, &status); err != nil {
				t.Fatalf("unmarshal normalized status %q: %v", data, err)
			}
			if status.Version.Name != test.wantVersion || status.Version.Protocol != test.wantProtocol {
				t.Errorf("version = %q/%d, want %q/%d", status.Version.Name, status.Version.Protocol, test.wantVersion, test.wantProtocol)
			}
			if status.Description.Text != test.wantMOTD {
				t.Errorf("MOTD = %q, want %q", status.Description.Text, test.wantMOTD)
			}
			if status.Players.Online != test.wantOnline || status.Players.Max != test.wantMax {
				t.Errorf("players = %d/%d, want %d/%d", status.Players.Online, status.Players.Max, test.wantOnline, test.wantMax)
			}
		})
	}
}

func runPingServer(conn net.Conn, serve func(net.Conn) error) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer conn.Close()
		done <- serve(conn)
	}()
	return done
}

func legacyResponseFixture(value string) []byte {
	units := utf16.Encode([]rune(value))
	response := make([]byte, 3+len(units)*2)
	response[0] = 0xFF
	binary.BigEndian.PutUint16(response[1:3], uint16(len(units)))
	for i, unit := range units {
		binary.BigEndian.PutUint16(response[3+i*2:], unit)
	}
	return response
}
