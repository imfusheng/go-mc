package main

import "testing"

func TestOfflinePacketMappingFixture(t *testing.T) {
	fixture, err := decodePacketMappings(offlinePacketMappings)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(fixture.Profiles), 48; got != want {
		t.Fatalf("mapped minecraft-data profiles = %d, want %d", got, want)
	}
	wantGaps := map[int32]bool{4: true, 485: true, 776: true}
	if got, want := len(fixture.Gaps), len(wantGaps); got != want {
		t.Fatalf("minecraft-data gaps = %d, want %d", got, want)
	}
	for _, gap := range fixture.Gaps {
		if !wantGaps[gap.Protocol] {
			t.Errorf("unexpected minecraft-data gap netty/%d", gap.Protocol)
		}
	}
}

func TestMojang262PacketReport(t *testing.T) {
	packets, err := decodeMojang262PacketReport(mojang262PacketReport)
	if err != nil {
		t.Fatal(err)
	}
	wants := map[packetMapping]bool{
		{State: "handshake", Direction: "serverbound", Kind: "set_protocol", ID: 0}:              true,
		{State: "login", Direction: "clientbound", Kind: "success", ID: 2}:                       true,
		{State: "configuration", Direction: "clientbound", Kind: "code_of_conduct", ID: 19}:      true,
		{State: "configuration", Direction: "serverbound", Kind: "resource_pack_receive", ID: 6}: true,
		{State: "play", Direction: "clientbound", Kind: "keep_alive", ID: 44}:                    true,
	}
	for _, packet := range packets {
		delete(wants, packet)
	}
	for missing := range wants {
		t.Errorf("Mojang 26.2 mapping is missing %+v", missing)
	}
}

func TestGoMC1142HistoricalPacketMappings(t *testing.T) {
	packets, err := decodeGoMC1142PacketMappings(goMC1142PacketMappings)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(packets), 150; got != want {
		t.Fatalf("go-mc v1.14.2 mappings = %d, want %d", got, want)
	}
	want := packetMapping{State: "play", Direction: "clientbound", Kind: "keep_alive", ID: 32}
	for _, packet := range packets {
		if packet == want {
			return
		}
	}
	t.Fatalf("go-mc v1.14.2 mapping is missing %+v", want)
}

func TestMojangProtocol4ResearchFixture(t *testing.T) {
	packets, err := decodeMojangProtocol4Research(mojangProtocol4Research)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(packets), 99; got != want {
		t.Fatalf("Mojang protocol-4 framed mappings = %d, want %d", got, want)
	}
	wants := map[packetMapping]bool{
		{State: "login", Direction: "clientbound", Kind: "success", ID: 2}: true,
		{State: "play", Direction: "clientbound", Kind: "login", ID: 1}:    true,
	}
	for _, packet := range packets {
		if isSpecialUnframedLegacyPing(packet.State, packet.Direction, packet.Kind, packet.ID) {
			t.Fatalf("special unframed FE ping leaked into framed mappings: %+v", packet)
		}
		delete(wants, packet)
	}
	for missing := range wants {
		t.Errorf("Mojang protocol-4 mapping is missing %+v", missing)
	}
}
