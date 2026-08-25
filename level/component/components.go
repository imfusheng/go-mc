package component

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/imfusheng/go-mc/data/registryid"
	pk "github.com/imfusheng/go-mc/net/packet"
)

type DataComponent interface {
	pk.Field
	ID() string
}

var (
	// ErrNilComponent is returned when a nil DataComponent is used where a
	// concrete component value is required.
	ErrNilComponent = errors.New("nil data component")
	// ErrUnknownComponent is returned for a component name or numeric type ID
	// that is not present in the protocol 767 data-component registry.
	ErrUnknownComponent = errors.New("unknown data component")
	// ErrUnsupportedComponent is returned for a protocol 767 component whose
	// payload codec has not been implemented by this package.
	ErrUnsupportedComponent = errors.New("unsupported data component")

	protocol767TypeNames = append([]string(nil), registryid.DataComponentType...)
)

// TypeError describes a failed data-component registry lookup.
type TypeError struct {
	Kind error
	ID   int32
	Name string
}

func (e *TypeError) Error() string {
	switch {
	case e.Name != "" && e.ID >= 0:
		return fmt.Sprintf("%v %q (type %d)", e.Kind, e.Name, e.ID)
	case e.Name != "":
		return fmt.Sprintf("%v %q", e.Kind, e.Name)
	default:
		return fmt.Sprintf("%v type %d", e.Kind, e.ID)
	}
}

func (e *TypeError) Unwrap() error { return e.Kind }

// TypeCount returns the number of data-component types in the protocol 767
// registry. Valid numeric type IDs are in the range [0, TypeCount()).
func TypeCount() int { return len(protocol767TypeNames) }

// TypeName resolves a protocol 767 numeric data-component type ID.
func TypeName(id int32) (string, bool) {
	if id < 0 || int(id) >= len(protocol767TypeNames) {
		return "", false
	}
	return protocol767TypeNames[id], true
}

// TypeID resolves a data component to its protocol 767 numeric type ID.
func TypeID(value DataComponent) (int32, error) {
	if value == nil || isNilComponent(value) {
		return -1, ErrNilComponent
	}

	name, err := componentName(value)
	if err != nil {
		return -1, err
	}
	for id, candidate := range protocol767TypeNames {
		if candidate == name {
			return int32(id), nil
		}
	}
	return -1, &TypeError{Kind: ErrUnknownComponent, ID: -1, Name: name}
}

func isNilComponent(value DataComponent) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func componentName(value DataComponent) (name string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			name = ""
			err = fmt.Errorf("data component ID panicked: %v", recovered)
		}
	}()
	return value.ID(), nil
}

func NewComponent(id int32) DataComponent {
	switch id {
	case 0:
		return new(CustomData)
	case 1:
		return new(MaxStackSize)
	case 2:
		return new(MaxDamage)
	case 3:
		return new(Damage)
	case 4:
		return new(Unbreakable)
	case 5:
		return new(CustomName)
	case 6:
		return new(ItemName)
	case 7:
		return new(Lore)
	case 8:
		return new(Rarity)
	case 9:
		return new(Enchantments)
	case 10:
		return new(CanPlaceOn)
	case 11:
		return new(CanBreak)
	case 12:
		return new(AttributeModifiers)
	case 13:
		return new(CustomModelData)
	case 14:
		return new(HideAdditionalTooptip)
	case 15:
		return new(HideTooptip)
	case 16:
		return new(RepairCost)
	case 17:
		return new(CreativeSlotLock)
	case 18:
		return new(EnchantmentGlintOverride)
	case 19:
		return new(IntangibleProjectile)
	case 20:
		return new(Food)
	case 21:
		return new(FireResistant)
	case 22:
		return new(Tool)
	case 23:
		return new(StoredEnchantments)
	case 24:
		return new(DyedColor)
	case 25:
		return new(MapColor)
	case 26:
		return new(MapID)
	case 27:
		return new(MapDecorations)
	case 28:
		return new(MapPostProcessing)
	case 29:
		return new(ChargedProjectiles)
	case 30:
		return new(BundleContents)
	case 31:
		return new(PotionContents)
	case 32:
		return new(SuspiciousStewEffects)
	case 33:
		return new(WritableBookContent)
	case 34:
	case 35:
		return new(Trim)
	case 36:
		return new(DebugStickState)
	case 37:
		return new(EntityData)
	case 38:
		return new(BucketEntityData)
	case 39:
		return new(BlockEntityData)
	case 40:
		return new(Instrument)
	case 41:
		return new(OminousBottleAmplifier)
	case 42:
		return new(JukeboxPlayable)
	case 43:
		return new(Recipes)
	case 44:
		return new(LodestoneTracker)
	case 45:
	case 46:
	case 47:
	case 48:
	case 49:
	case 50:
	case 51:
	case 52:
	case 53:
	case 54:
	case 55:
	case 56:
	}
	return nil
}
