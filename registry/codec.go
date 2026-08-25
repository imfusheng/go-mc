package registry

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/nbt"
	pk "github.com/imfusheng/go-mc/net/packet"
)

type Registries struct {
	ChatType        Registry[ChatType]       `registry:"minecraft:chat_type" since:"764"`
	DamageType      Registry[DamageType]     `registry:"minecraft:damage_type" since:"764"`
	DimensionType   Registry[Dimension]      `registry:"minecraft:dimension_type" since:"764"`
	TrimMaterial    Registry[nbt.RawMessage] `registry:"minecraft:trim_material" since:"764"`
	TrimPattern     Registry[nbt.RawMessage] `registry:"minecraft:trim_pattern" since:"764"`
	WorldGenBiome   Registry[nbt.RawMessage] `registry:"minecraft:worldgen/biome" since:"764"`
	Wolfvariant     Registry[nbt.RawMessage] `registry:"minecraft:wolf_variant" since:"766"`
	PaintingVariant Registry[nbt.RawMessage] `registry:"minecraft:painting_variant" since:"767"`
	BannerPattern   Registry[nbt.RawMessage] `registry:"minecraft:banner_pattern" since:"766"`
	Enchantment     Registry[nbt.RawMessage] `registry:"minecraft:enchantment" since:"767"`
	JukeboxSong     Registry[nbt.RawMessage] `registry:"minecraft:jukebox_song" since:"767"`
	Instrument      Registry[nbt.RawMessage] `registry:"minecraft:instrument" since:"768"`

	WolfSoundVariant Registry[nbt.RawMessage] `registry:"minecraft:wolf_sound_variant" since:"770"`
	PigVariant       Registry[nbt.RawMessage] `registry:"minecraft:pig_variant" since:"770"`
	FrogVariant      Registry[nbt.RawMessage] `registry:"minecraft:frog_variant" since:"770"`
	CatVariant       Registry[nbt.RawMessage] `registry:"minecraft:cat_variant" since:"770"`
	CowVariant       Registry[nbt.RawMessage] `registry:"minecraft:cow_variant" since:"770"`
	ChickenVariant   Registry[nbt.RawMessage] `registry:"minecraft:chicken_variant" since:"770"`
	TestEnvironment  Registry[nbt.RawMessage] `registry:"minecraft:test_environment" since:"770"`
	TestInstance     Registry[nbt.RawMessage] `registry:"minecraft:test_instance" since:"770"`
	Dialog           Registry[nbt.RawMessage] `registry:"minecraft:dialog" since:"771"`

	ZombieNautilusVariant Registry[nbt.RawMessage] `registry:"minecraft:zombie_nautilus_variant" since:"774"`
	Timeline              Registry[nbt.RawMessage] `registry:"minecraft:timeline" since:"774"`

	PigSoundVariant     Registry[nbt.RawMessage] `registry:"minecraft:pig_sound_variant" since:"775"`
	CatSoundVariant     Registry[nbt.RawMessage] `registry:"minecraft:cat_sound_variant" since:"775"`
	CowSoundVariant     Registry[nbt.RawMessage] `registry:"minecraft:cow_sound_variant" since:"775"`
	ChickenSoundVariant Registry[nbt.RawMessage] `registry:"minecraft:chicken_sound_variant" since:"775"`
	WorldClock          Registry[nbt.RawMessage] `registry:"minecraft:world_clock" since:"775"`

	SulfurCubeArchetype Registry[nbt.RawMessage] `registry:"minecraft:sulfur_cube_archetype" since:"776"`

	additional []versionedNetworkRegistry
}

func NewNetworkCodec() Registries {
	// Registry storage initializes on first mutation or network decode. Most
	// pre-Configuration clients never use it, and newer profiles have an
	// increasing number of registry types, so eager per-registry maps and
	// 256-entry slices would impose a large idle-client memory penalty.
	return Registries{}
}

type ChatType struct {
	Chat      chat.Decoration `nbt:"chat"`
	Narration chat.Decoration `nbt:"narration"`
}

type DamageType struct {
	MessageID        string  `nbt:"message_id"`
	Scaling          string  `nbt:"scaling"`
	Exhaustion       float32 `nbt:"exhaustion"`
	Effects          string  `nbt:"effects,omitempty"`
	DeathMessageType string  `nbt:"death_message_type,omitempty"`
}

type Dimension struct {
	FixedTime          int64   `nbt:"fixed_time,omitempty"`
	HasSkylight        bool    `nbt:"has_skylight"`
	HasCeiling         bool    `nbt:"has_ceiling"`
	Ultrawarm          bool    `nbt:"ultrawarm"`
	Natural            bool    `nbt:"natural"`
	CoordinateScale    float64 `nbt:"coordinate_scale"`
	BedWorks           bool    `nbt:"bed_works"`
	RespawnAnchorWorks byte    `nbt:"respawn_anchor_works"`
	MinY               int32   `nbt:"min_y"`
	Height             int32   `nbt:"height"`
	LogicalHeight      int32   `nbt:"logical_height"`
	InfiniteBurn       string  `nbt:"infiniburn"`
	Effects            string  `nbt:"effects"`
	AmbientLight       float64 `nbt:"ambient_light"`

	PiglinSafe                  byte           `nbt:"piglin_safe"`
	HasRaids                    byte           `nbt:"has_raids"`
	MonsterSpawnLightLevel      nbt.RawMessage `nbt:"monster_spawn_light_level"` // Tag_Int or {type:"minecraft:uniform", value:{min_inclusive: Tag_Int, max_inclusive: Tag_Int}}
	MonsterSpawnBlockLightLimit int32          `nbt:"monster_spawn_block_light_limit"`
}

type RegistryCodec interface {
	pk.FieldEncoder
	pk.FieldDecoder
	ReadTagsFrom(r io.Reader) (int64, error)
}

// NetworkRegistry pairs a configuration registry identifier with its entries
// codec. The order is deterministic and follows the fields in Registries.
type NetworkRegistry struct {
	ID    string
	Codec RegistryCodec
}

type versionedNetworkRegistry struct {
	NetworkRegistry
	Since int32
}

// AddNetworkRegistry adds a lazily allocated registry codec introduced in the
// supplied protocol. It lets callers provide newer vanilla or modded dynamic
// registries without forcing every Client to eagerly allocate storage for
// registries that do not exist in its selected profile.
func (c *Registries) AddNetworkRegistry(id string, since int32, codec RegistryCodec) error {
	if c == nil {
		return errors.New("registry: nil registries")
	}
	if id == "" {
		return errors.New("registry: empty network registry identifier")
	}
	if since < 764 {
		return fmt.Errorf("registry: network registry %q has invalid introduction protocol %d", id, since)
	}
	if codec == nil || (reflect.ValueOf(codec).Kind() == reflect.Ptr && reflect.ValueOf(codec).IsNil()) {
		return fmt.Errorf("registry: nil codec for %q", id)
	}
	if c.Registry(id) != nil {
		return fmt.Errorf("registry: duplicate network registry %q", id)
	}
	c.additional = append(c.additional, versionedNetworkRegistry{
		NetworkRegistry: NetworkRegistry{ID: id, Codec: codec},
		Since:           since,
	})
	return nil
}

// AddRawNetworkRegistry creates and registers a schema-independent registry.
// Raw NBT entries are useful for registries added after the typed baseline.
func (c *Registries) AddRawNetworkRegistry(id string, since int32) (*Registry[nbt.RawMessage], error) {
	reg := new(Registry[nbt.RawMessage])
	if err := c.AddNetworkRegistry(id, since, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// NetworkRegistries returns every registry represented by c.
func (c *Registries) NetworkRegistries() []NetworkRegistry {
	return c.networkRegistries(0, false)
}

// NetworkRegistriesForProtocol returns only registries that exist in the
// requested Configuration protocol. Registry payloads are not forward
// compatible: sending a registry introduced by a newer release can make an
// older vanilla client reject Configuration.
func (c *Registries) NetworkRegistriesForProtocol(protocol int32) []NetworkRegistry {
	return c.networkRegistries(protocol, true)
}

func (c *Registries) networkRegistries(protocol int32, filter bool) []NetworkRegistry {
	if c == nil {
		return nil
	}
	codecVal := reflect.ValueOf(c).Elem()
	codecTyp := codecVal.Type()
	result := make([]NetworkRegistry, 0, codecVal.NumField())
	for i := 0; i < codecVal.NumField(); i++ {
		field := codecTyp.Field(i)
		registryID, ok := field.Tag.Lookup("registry")
		if !ok {
			continue
		}
		if filter {
			since, err := strconv.ParseInt(field.Tag.Get("since"), 10, 32)
			if err != nil {
				panic("registry: invalid since tag for " + field.Name)
			}
			if int64(protocol) < since {
				continue
			}
		}
		result = append(result, NetworkRegistry{
			ID:    registryID,
			Codec: codecVal.Field(i).Addr().Interface().(RegistryCodec),
		})
	}
	for _, extra := range c.additional {
		if filter && protocol < extra.Since {
			continue
		}
		result = append(result, extra.NetworkRegistry)
	}
	return result
}

func (c *Registries) Registry(id string) RegistryCodec {
	if c == nil {
		return nil
	}
	codecVal := reflect.ValueOf(c).Elem()
	codecTyp := codecVal.Type()
	numField := codecVal.NumField()
	for i := 0; i < numField; i++ {
		registryID, ok := codecTyp.Field(i).Tag.Lookup("registry")
		if !ok {
			continue
		}
		if registryID == id {
			return codecVal.Field(i).Addr().Interface().(RegistryCodec)
		}
	}
	for _, extra := range c.additional {
		if extra.ID == id {
			return extra.Codec
		}
	}
	return nil
}
