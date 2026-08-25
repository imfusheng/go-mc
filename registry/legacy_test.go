package registry

import (
	"bytes"
	"testing"

	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestLegacyNetworkCodecEncodesCompleteCompound(t *testing.T) {
	registries := NewNetworkCodec()
	registries.ChatType.Put("minecraft:test_chat", ChatType{})

	field, err := registries.LegacyNetworkCodec()
	if err != nil {
		t.Fatalf("LegacyNetworkCodec() error = %v", err)
	}
	var wire bytes.Buffer
	written, err := field.WriteTo(&wire)
	if err != nil {
		t.Fatalf("legacy codec WriteTo() error = %v", err)
	}
	if written != int64(wire.Len()) {
		t.Fatalf("legacy codec wrote %d bytes, buffer has %d", written, wire.Len())
	}

	var decoded legacyNetworkCodec
	if _, err := (pk.NBTField{V: &decoded}).ReadFrom(&wire); err != nil {
		t.Fatalf("decode legacy codec: %v", err)
	}
	if wire.Len() != 0 {
		t.Fatalf("decode legacy codec left %d bytes", wire.Len())
	}
	if decoded.ChatType.Type != "minecraft:chat_type" || len(decoded.ChatType.Value) != 1 {
		t.Fatalf("decoded chat registry = %#v", decoded.ChatType)
	}
	entry := decoded.ChatType.Value[0]
	if entry.Name != "minecraft:test_chat" || entry.ID != 0 {
		t.Fatalf("decoded entry = %#v", entry)
	}
	if decoded.DamageType.Type != "minecraft:damage_type" || decoded.DimensionType.Type != "minecraft:dimension_type" {
		t.Fatalf("legacy codec omitted required registry envelopes")
	}
}

func TestNetworkRegistriesForProtocolFiltersIntroductions(t *testing.T) {
	registries := NewNetworkCodec()
	future, err := registries.AddRawNetworkRegistry("example:future_registry", 768)
	if err != nil {
		t.Fatalf("AddRawNetworkRegistry() error = %v", err)
	}
	if future == nil || registries.Registry("example:future_registry") != future {
		t.Fatal("additional registry was not retained")
	}
	if _, err := registries.AddRawNetworkRegistry("example:future_registry", 768); err == nil {
		t.Fatal("duplicate additional registry succeeded")
	}
	for _, test := range []struct {
		protocol int32
		want     int
	}{
		{protocol: 763, want: 0},
		{protocol: 764, want: 6},
		{protocol: 765, want: 6},
		{protocol: 766, want: 8},
		{protocol: 767, want: 11},
		{protocol: 768, want: 13},
		{protocol: 769, want: 13},
		{protocol: 770, want: 21},
		{protocol: 771, want: 22},
		{protocol: 774, want: 24},
		{protocol: 775, want: 29},
		{protocol: 776, want: 30},
	} {
		if got := len(registries.NetworkRegistriesForProtocol(test.protocol)); got != test.want {
			t.Errorf("protocol %d registries = %d, want %d", test.protocol, got, test.want)
		}
	}
	if containsNetworkRegistry(registries.NetworkRegistriesForProtocol(767), "minecraft:instrument") {
		t.Fatal("protocol 767 unexpectedly includes minecraft:instrument")
	}
	if !containsNetworkRegistry(registries.NetworkRegistriesForProtocol(768), "minecraft:instrument") {
		t.Fatal("protocol 768 omitted minecraft:instrument")
	}
}

func containsNetworkRegistry(registries []NetworkRegistry, id string) bool {
	for _, reg := range registries {
		if reg.ID == id {
			return true
		}
	}
	return false
}

func TestVanillaNetworkRegistryIntroductionBoundaries(t *testing.T) {
	registries := NewNetworkCodec()
	for _, test := range []struct {
		id    string
		since int32
	}{
		{id: "minecraft:chat_type", since: 764},
		{id: "minecraft:wolf_variant", since: 766},
		{id: "minecraft:banner_pattern", since: 766},
		{id: "minecraft:painting_variant", since: 767},
		{id: "minecraft:enchantment", since: 767},
		{id: "minecraft:jukebox_song", since: 767},
		{id: "minecraft:instrument", since: 768},
		{id: "minecraft:wolf_sound_variant", since: 770},
		{id: "minecraft:pig_variant", since: 770},
		{id: "minecraft:frog_variant", since: 770},
		{id: "minecraft:cat_variant", since: 770},
		{id: "minecraft:cow_variant", since: 770},
		{id: "minecraft:chicken_variant", since: 770},
		{id: "minecraft:test_environment", since: 770},
		{id: "minecraft:test_instance", since: 770},
		{id: "minecraft:dialog", since: 771},
		{id: "minecraft:zombie_nautilus_variant", since: 774},
		{id: "minecraft:timeline", since: 774},
		{id: "minecraft:pig_sound_variant", since: 775},
		{id: "minecraft:cat_sound_variant", since: 775},
		{id: "minecraft:cow_sound_variant", since: 775},
		{id: "minecraft:chicken_sound_variant", since: 775},
		{id: "minecraft:world_clock", since: 775},
		{id: "minecraft:sulfur_cube_archetype", since: 776},
	} {
		t.Run(test.id, func(t *testing.T) {
			if containsNetworkRegistry(registries.NetworkRegistriesForProtocol(test.since-1), test.id) {
				t.Fatalf("%s is present before protocol %d", test.id, test.since)
			}
			if !containsNetworkRegistry(registries.NetworkRegistriesForProtocol(test.since), test.id) {
				t.Fatalf("%s is absent at protocol %d", test.id, test.since)
			}
		})
	}
}

var networkCodecSink Registries

func TestNewNetworkCodecDoesNotEagerlyAllocateRegistryStorage(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		networkCodecSink = NewNetworkCodec()
	})
	if allocations != 0 {
		t.Fatalf("NewNetworkCodec() allocations = %.2f, want 0", allocations)
	}
}
