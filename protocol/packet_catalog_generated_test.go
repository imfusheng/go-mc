package protocol

import "testing"

func TestGeneratedPacketMappingsCoverageAndBijection(t *testing.T) {
	states := []State{StateHandshake, StateStatus, StateLogin, StateConfiguration, StatePlay}
	directions := []Direction{Clientbound, Serverbound}
	var mapped int
	for _, profile := range Profiles() {
		if profile.Key().Transport != TransportNetty {
			continue
		}
		protocolNumber := profile.Key().Protocol
		mapped++
		for _, required := range []State{StateHandshake, StateStatus, StateLogin, StatePlay} {
			var count int
			for _, direction := range directions {
				count += len(profile.SortedPacketKinds(required, direction))
			}
			if count == 0 {
				t.Errorf("netty/%d has no %s packet mappings", protocolNumber, required)
			}
		}
		configurationPackets := len(profile.SortedPacketKinds(StateConfiguration, Clientbound)) +
			len(profile.SortedPacketKinds(StateConfiguration, Serverbound))
		if protocolNumber >= 764 && configurationPackets == 0 {
			t.Errorf("netty/%d has no configuration packet mappings", protocolNumber)
		}
		if protocolNumber < 764 && configurationPackets != 0 {
			t.Errorf("netty/%d unexpectedly has %d configuration packet mappings", protocolNumber, configurationPackets)
		}

		for _, state := range states {
			for _, direction := range directions {
				for _, kind := range profile.SortedPacketKinds(state, direction) {
					id, ok := profile.PacketID(state, direction, kind)
					if !ok {
						t.Errorf("netty/%d cannot resolve generated %s/%s kind %q", protocolNumber, state, direction, kind)
						continue
					}
					got, ok := profile.PacketKind(state, direction, id)
					if !ok || got != kind {
						t.Errorf("netty/%d reverse lookup for %s/%s ID %d = %q, %v; want %q", protocolNumber, state, direction, id, got, ok, kind)
					}
				}
			}
		}
	}
	if mapped != 51 {
		t.Fatalf("mapped Netty profiles = %d, want 51", mapped)
	}
}

func TestGeneratedPacketMappingBoundaries(t *testing.T) {
	tests := []struct {
		version   string
		state     State
		direction Direction
		kind      PacketKind
		want      int32
	}{
		{version: "1.7.5", state: StateLogin, direction: Clientbound, kind: "success", want: 2},
		{version: "1.7.5", state: StatePlay, direction: Clientbound, kind: "login", want: 1},
		{version: "1.7.10", state: StatePlay, direction: Clientbound, kind: "keep_alive", want: 0},
		{version: "1.8.9", state: StateLogin, direction: Clientbound, kind: "success", want: 2},
		{version: "1.9.1", state: StatePlay, direction: Clientbound, kind: "keep_alive", want: 31},
		{version: "1.14.2", state: StatePlay, direction: Clientbound, kind: "keep_alive", want: 32},
		{version: "1.20.2", state: StateConfiguration, direction: Clientbound, kind: "registry_data", want: 5},
		{version: "1.21.1", state: StatePlay, direction: Clientbound, kind: "keep_alive", want: 38},
		{version: "26.1", state: StateConfiguration, direction: Clientbound, kind: "code_of_conduct", want: 19},
		{version: "26.1", state: StatePlay, direction: Serverbound, kind: "keep_alive", want: 28},
		{version: "26.2", state: StateConfiguration, direction: Clientbound, kind: "registry_data", want: 7},
		{version: "26.2", state: StatePlay, direction: Clientbound, kind: "keep_alive", want: 44},
	}
	for _, test := range tests {
		t.Run(test.version+"/"+test.state.String()+"/"+string(test.kind), func(t *testing.T) {
			profile := MustByName(test.version)
			got, ok := profile.PacketID(test.state, test.direction, test.kind)
			if !ok || got != test.want {
				t.Fatalf("PacketID(%s, %s, %q) = %d, %v; want %d, true", test.state, test.direction, test.kind, got, ok, test.want)
			}
		})
	}
}

func TestGeneratedPacketMappingPinnedSources(t *testing.T) {
	if got := MustByName("1.7.5").SortedPacketKinds(StatePlay, Clientbound); len(got) != 65 {
		t.Fatalf("1.7.5 clientbound Play mappings = %d, want 65 from the Mojang bytecode fixture", len(got))
	}
	if got := MustByName("26.2").SortedPacketKinds(StateStatus, Clientbound); len(got) == 0 {
		t.Fatal("26.2 should use Mojang's official generated packet report")
	}
	if got := MustByName("1.14.2").SortedPacketKinds(StatePlay, Clientbound); len(got) == 0 {
		t.Fatal("1.14.2 should use the pinned go-mc v1.14.2 historical packet table")
	}
	for _, profile := range Profiles() {
		if _, ok := profile.PacketID(StateHandshake, Serverbound, "legacy_server_list_ping"); ok {
			t.Errorf("%v exposes the special unframed FE ping as a framed Profile packet", profile.Key())
		}
	}
}
