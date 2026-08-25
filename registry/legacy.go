package registry

import (
	"errors"
	"fmt"

	"github.com/imfusheng/go-mc/nbt"
	pk "github.com/imfusheng/go-mc/net/packet"
)

// legacyNetworkCodec is the anonymous-NBT registry codec used by protocols
// 764 and 765. Later protocols moved each member to a separate Registry Data
// packet and use Registry.WriteTo directly.
type legacyNetworkCodec struct {
	ChatType      legacyRegistry[ChatType]   `nbt:"minecraft:chat_type"`
	DamageType    legacyRegistry[DamageType] `nbt:"minecraft:damage_type"`
	DimensionType legacyRegistry[Dimension]  `nbt:"minecraft:dimension_type"`
	TrimMaterial  legacyRegistry[rawNBT]     `nbt:"minecraft:trim_material"`
	TrimPattern   legacyRegistry[rawNBT]     `nbt:"minecraft:trim_pattern"`
	WorldGenBiome legacyRegistry[rawNBT]     `nbt:"minecraft:worldgen/biome"`
}

// rawNBT is an alias so the legacy wire struct remains concise while retaining
// nbt.RawMessage's custom marshaling implementation.
type rawNBT = nbt.RawMessage

type legacyRegistry[E any] struct {
	Type  string                   `nbt:"type"`
	Value []legacyRegistryEntry[E] `nbt:"value"`
}

type legacyRegistryEntry[E any] struct {
	Name    string `nbt:"name"`
	ID      int32  `nbt:"id"`
	Element E      `nbt:"element"`
}

// LegacyNetworkCodec returns the complete anonymous-NBT field required by
// Minecraft 1.20.2 through 1.20.4. Entries without data cannot be represented
// by that legacy format and are rejected instead of being silently changed.
func (c *Registries) LegacyNetworkCodec() (pk.FieldEncoder, error) {
	if c == nil {
		return nil, errors.New("registry: nil registries")
	}

	chatType, err := makeLegacyRegistry("minecraft:chat_type", &c.ChatType)
	if err != nil {
		return nil, err
	}
	damageType, err := makeLegacyRegistry("minecraft:damage_type", &c.DamageType)
	if err != nil {
		return nil, err
	}
	dimensionType, err := makeLegacyRegistry("minecraft:dimension_type", &c.DimensionType)
	if err != nil {
		return nil, err
	}
	trimMaterial, err := makeLegacyRegistry("minecraft:trim_material", &c.TrimMaterial)
	if err != nil {
		return nil, err
	}
	trimPattern, err := makeLegacyRegistry("minecraft:trim_pattern", &c.TrimPattern)
	if err != nil {
		return nil, err
	}
	worldGenBiome, err := makeLegacyRegistry("minecraft:worldgen/biome", &c.WorldGenBiome)
	if err != nil {
		return nil, err
	}

	return pk.NBTField{V: legacyNetworkCodec{
		ChatType:      chatType,
		DamageType:    damageType,
		DimensionType: dimensionType,
		TrimMaterial:  trimMaterial,
		TrimPattern:   trimPattern,
		WorldGenBiome: worldGenBiome,
	}}, nil
}

func makeLegacyRegistry[E any](id string, reg *Registry[E]) (legacyRegistry[E], error) {
	if reg == nil {
		return legacyRegistry[E]{}, fmt.Errorf("registry: nil %s registry", id)
	}
	if len(reg.names) != len(reg.values) || len(reg.present) != len(reg.values) {
		return legacyRegistry[E]{}, fmt.Errorf("registry: inconsistent %s registry lengths", id)
	}

	entries := make([]legacyRegistryEntry[E], len(reg.values))
	for i := range reg.values {
		if !reg.present[i] {
			return legacyRegistry[E]{}, fmt.Errorf("registry: %s entry %q has no data in a legacy codec", id, reg.names[i])
		}
		entries[i] = legacyRegistryEntry[E]{
			Name:    reg.names[i],
			ID:      int32(i),
			Element: reg.values[i],
		}
	}
	return legacyRegistry[E]{Type: id, Value: entries}, nil
}
