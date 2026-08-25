package protocol

import "testing"

func TestKeySeparatesReusedProtocolNumbers(t *testing.T) {
	legacy := Key{Transport: TransportLegacy, Protocol: 47}
	netty := Key{Transport: TransportNetty, Protocol: 47}
	if legacy == netty {
		t.Fatal("legacy 1.4.2 and Netty 1.8 must not share a profile key")
	}
}

func TestPacketLookupIsScopedByStateAndDirection(t *testing.T) {
	p := &Profile{
		byKind: map[packetScope]map[PacketKind]int32{
			{state: StateLogin, direction: Clientbound}: {"disconnect": 0},
			{state: StatePlay, direction: Clientbound}:  {"bundle_delimiter": 0},
		},
		byID: map[packetScope]map[int32]PacketKind{
			{state: StateLogin, direction: Clientbound}: {0: "disconnect"},
			{state: StatePlay, direction: Clientbound}:  {0: "bundle_delimiter"},
		},
	}

	if id, ok := p.PacketID(StateLogin, Clientbound, "disconnect"); !ok || id != 0 {
		t.Fatalf("PacketID() = %d, %v; want 0, true", id, ok)
	}
	if kind, ok := p.PacketKind(StatePlay, Clientbound, 0); !ok || kind != "bundle_delimiter" {
		t.Fatalf("PacketKind() = %q, %v; want bundle_delimiter, true", kind, ok)
	}
	if _, ok := p.PacketID(StateStatus, Clientbound, "disconnect"); ok {
		t.Fatal("packet lookup leaked across states")
	}
}

func TestVersionsReturnsCopy(t *testing.T) {
	p := &Profile{aliases: []Version{{Name: "1.8"}}}
	got := p.Versions()
	got[0].Name = "changed"
	if p.aliases[0].Name != "1.8" {
		t.Fatal("Versions exposed mutable profile storage")
	}
}
