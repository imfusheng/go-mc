package server

import (
	"errors"
	"testing"
	"time"

	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

func TestHandshakeSemanticPacketDispatch(t *testing.T) {
	tests := []struct {
		name           string
		protocolNumber int32
		packetID       int32
		wantErr        string
	}{
		{name: "protocol 4 audited mapping", protocolNumber: 4, packetID: 0},
		{name: "known current profile", protocolNumber: 767, packetID: 0},
		{name: "known latest profile", protocolNumber: 776, packetID: 0},
		{name: "unknown status probe", protocolNumber: 9999, packetID: 0},
		// 0xFE is an unframed legacy ping marker, not a Netty Handshake
		// packet. Once excluded from the framed identity catalogue it must
		// fail as an unavailable mapping rather than as another semantic kind.
		{name: "known unframed marker", protocolNumber: 767, packetID: 254, wantErr: "mapping"},
		{name: "unknown wrong packet ID", protocolNumber: 9999, packetID: 7, wantErr: "mapping"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverConn, clientConn := pipeMC(t)
			type result struct {
				protocol  int32
				intention int32
				err       error
			}
			resultCh := make(chan result, 1)
			go func() {
				gotProtocol, gotIntention, err := new(Server).handshake(serverConn)
				resultCh <- result{protocol: gotProtocol, intention: gotIntention, err: err}
			}()
			if err := clientConn.WritePacket(pk.Marshal(
				test.packetID,
				pk.VarInt(test.protocolNumber),
				pk.String("localhost"),
				pk.UnsignedShort(25565),
				pk.VarInt(1),
			)); err != nil {
				t.Fatalf("write handshake: %v", err)
			}

			select {
			case got := <-resultCh:
				if got.protocol != test.protocolNumber || got.intention != 1 {
					t.Fatalf("handshake = protocol %d intention %d", got.protocol, got.intention)
				}
				switch test.wantErr {
				case "":
					if got.err != nil {
						t.Fatalf("handshake error = %v", got.err)
					}
				case "wrong":
					var wrong wrongPacketErr
					if !errors.As(got.err, &wrong) {
						t.Fatalf("handshake error = %v; want wrongPacketErr", got.err)
					}
				case "mapping":
					if !errors.Is(got.err, protocol.ErrPacketMappingUnavailable) {
						t.Fatalf("handshake error = %v; want ErrPacketMappingUnavailable", got.err)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("handshake did not complete")
			}
		})
	}
}
