package basic

import (
	"bytes"
	"errors"
	"testing"

	"github.com/imfusheng/go-mc/data/packetid"
	"github.com/imfusheng/go-mc/protocol"
)

func TestPlayResponsePacketUsesActiveProfileAndOwnsPayload(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		kind       protocol.PacketKind
		payload    []byte
		responseID int32
	}{
		{name: "protocol 4 fixed-width keepalive", version: "1.7.5", kind: playKeepAliveKind, payload: []byte{0x12, 0x34, 0x56, 0x78}, responseID: 0},
		{name: "protocol 47 varint keepalive", version: "1.8.9", kind: playKeepAliveKind, payload: []byte{0xac, 0x02}, responseID: 0},
		{name: "protocol 340 long keepalive", version: "1.12.2", kind: playKeepAliveKind, payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}, responseID: 11},
		{name: "protocol 755 first play ping", version: "1.17", kind: playPongKind, payload: []byte{0x7f, 0xff, 0xff, 0xff}, responseID: 29},
		{name: "protocol 767 baseline keepalive", version: "1.21.1", kind: playKeepAliveKind, payload: []byte{8, 7, 6, 5, 4, 3, 2, 1}, responseID: 24},
		{name: "protocol 767 baseline pong", version: "1.21.1", kind: playPongKind, payload: []byte{1, 3, 3, 7}, responseID: 39},
		{name: "protocol 776 latest keepalive", version: "26.2", kind: playKeepAliveKind, payload: []byte{9, 8, 7, 6, 5, 4, 3, 2}, responseID: 28},
		{name: "protocol 776 latest pong", version: "26.2", kind: playPongKind, payload: []byte{4, 3, 2, 1}, responseID: 45},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			incoming := append([]byte(nil), test.payload...)
			response, err := playResponsePacket(protocol.MustByName(test.version), test.kind, incoming)
			if err != nil {
				t.Fatalf("playResponsePacket(): %v", err)
			}
			if response.ID != test.responseID || !bytes.Equal(response.Data, test.payload) {
				t.Fatalf("playResponsePacket() = {ID:%d Data:%x}, want {ID:%d Data:%x}", response.ID, response.Data, test.responseID, test.payload)
			}

			copy(incoming, bytes.Repeat([]byte{0xff}, len(incoming)))
			if !bytes.Equal(response.Data, test.payload) {
				t.Fatalf("response aliases the borrowed receive buffer: %x", response.Data)
			}
		})
	}
}

func TestPlayResponsePacketRejectsMissingMapping(t *testing.T) {
	_, err := playResponsePacket(protocol.MustByName("1.7.5"), playPongKind, []byte{1, 2, 3, 4})
	if !errors.Is(err, protocol.ErrPacketMappingUnavailable) {
		t.Fatalf("playResponsePacket() error = %v, want ErrPacketMappingUnavailable", err)
	}
}

func TestPlayResponseMappingsCoverAllNettyProfiles(t *testing.T) {
	const expectedNettyProfiles = 51
	var profiles, pingProfiles int
	for _, profile := range protocol.Profiles() {
		if profile.Key().Transport != protocol.TransportNetty {
			continue
		}
		profiles++
		if _, ok := profile.PacketID(protocol.StatePlay, protocol.Clientbound, playKeepAliveKind); !ok {
			t.Errorf("%s has no clientbound Play keep_alive mapping", profile.Key())
			continue
		}
		if _, err := playResponsePacket(profile, playKeepAliveKind, []byte{1}); err != nil {
			t.Errorf("%s keep_alive response: %v", profile.Key(), err)
		}

		if _, ok := profile.PacketID(protocol.StatePlay, protocol.Clientbound, playPingKind); !ok {
			continue
		}
		pingProfiles++
		if _, err := playResponsePacket(profile, playPongKind, []byte{1}); err != nil {
			t.Errorf("%s pong response: %v", profile.Key(), err)
		}
	}
	if profiles != expectedNettyProfiles {
		t.Fatalf("tested %d Netty profiles, want %d", profiles, expectedNettyProfiles)
	}
	if pingProfiles == 0 {
		t.Fatal("catalog has no clientbound Play ping mappings")
	}
}

func TestPlayerResponsesUseSemanticHandlersOnly(t *testing.T) {
	p := &Player{}
	for _, handler := range p.numericPacketHandlers() {
		if handler.ID == packetid.ClientboundKeepAlive || handler.ID == packetid.ClientboundPing {
			t.Fatalf("response packet %v is still registered as a protocol-767 numeric handler", handler.ID)
		}
	}

	semantic := p.semanticPacketHandlers()
	if len(semantic) != 2 {
		t.Fatalf("semantic response handlers = %d, want 2", len(semantic))
	}
	seen := make(map[protocol.PacketKind]int, len(semantic))
	for _, handler := range semantic {
		seen[handler.Kind]++
		if handler.F == nil {
			t.Fatalf("semantic handler %q has nil callback", handler.Kind)
		}
	}
	if seen[playKeepAliveKind] != 1 || seen[playPingKind] != 1 {
		t.Fatalf("semantic response kinds = %v, want one keep_alive and one ping", seen)
	}
}
