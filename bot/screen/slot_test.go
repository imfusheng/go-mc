package screen

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/imfusheng/go-mc/level/component"
	"github.com/imfusheng/go-mc/nbt"
	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestSlotProtocol767RoundTrip(t *testing.T) {
	want := Slot{
		ID:    5,
		Count: 3,
		Components: []component.DataComponent{
			&component.MaxStackSize{VarInt: 64},
			&component.Damage{VarInt: 7},
		},
		RemovedComponents: []pk.VarInt{26}, // minecraft:map_id
	}

	var encoded bytes.Buffer
	n, err := want.WriteTo(&encoded)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	wantWire := []byte{3, 5, 2, 1, 1, 64, 3, 7, 26}
	if n != int64(len(wantWire)) {
		t.Errorf("WriteTo byte count = %d, want %d", n, len(wantWire))
	}
	if got := encoded.Bytes(); !bytes.Equal(got, wantWire) {
		t.Fatalf("wire bytes = %v, want %v", got, wantWire)
	}

	var got Slot
	n, err = got.ReadFrom(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if n != int64(len(wantWire)) {
		t.Errorf("ReadFrom byte count = %d, want %d", n, len(wantWire))
	}
	if got.ID != want.ID || got.Count != want.Count {
		t.Errorf("decoded identity = ID %d, count %d; want ID %d, count %d", got.ID, got.Count, want.ID, want.Count)
	}
	if !reflect.DeepEqual(got.RemovedComponents, want.RemovedComponents) {
		t.Errorf("removed components = %v, want %v", got.RemovedComponents, want.RemovedComponents)
	}
	if len(got.Components) != 2 {
		t.Fatalf("decoded %d components, want 2", len(got.Components))
	}
	maxStack, ok := got.Components[0].(*component.MaxStackSize)
	if !ok || maxStack.VarInt != 64 {
		t.Errorf("first component = %#v, want max_stack_size 64", got.Components[0])
	}
	damage, ok := got.Components[1].(*component.Damage)
	if !ok || damage.VarInt != 7 {
		t.Errorf("second component = %#v, want damage 7", got.Components[1])
	}

	var reencoded bytes.Buffer
	if _, err := got.WriteTo(&reencoded); err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(reencoded.Bytes(), wantWire) {
		t.Errorf("re-encoded bytes = %v, want %v", reencoded.Bytes(), wantWire)
	}
}

func TestSlotProtocol767EmptyEncoding(t *testing.T) {
	for name, slot := range map[string]*Slot{
		"nil pointer": nil,
		"zero value":  {},
	} {
		t.Run(name, func(t *testing.T) {
			var encoded bytes.Buffer
			n, err := slot.WriteTo(&encoded)
			if err != nil {
				t.Fatalf("WriteTo: %v", err)
			}
			if n != 1 || !bytes.Equal(encoded.Bytes(), []byte{0}) {
				t.Fatalf("WriteTo = %d bytes %v, want 1 byte [0]", n, encoded.Bytes())
			}
		})
	}

	destination := Slot{
		ID:                10,
		Count:             2,
		Components:        []component.DataComponent{&component.Damage{}},
		RemovedComponents: []pk.VarInt{1},
	}
	n, err := destination.ReadFrom(bytes.NewReader([]byte{0}))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if n != 1 || !reflect.DeepEqual(destination, Slot{}) {
		t.Fatalf("decoded empty slot = %#v, byte count %d", destination, n)
	}
}

func TestSlotRejectsLegacyNBT(t *testing.T) {
	slot := Slot{ID: 1, Count: 1, NBT: nbt.RawMessage{Type: nbt.TagCompound}}
	if _, err := slot.WriteTo(new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "use data components") {
		t.Fatalf("WriteTo error = %v, want explicit legacy NBT rejection", err)
	}
}

func TestSlotRejectsUnknownAndUnsupportedComponents(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{
			name: "unknown type ID",
			wire: []byte{1, 1, 1, 0, 57},
			want: component.ErrUnknownComponent,
		},
		{
			name: "known type without codec",
			wire: []byte{1, 1, 1, 0, 34}, // minecraft:written_book_content
			want: component.ErrUnsupportedComponent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var slot Slot
			if _, err := slot.ReadFrom(bytes.NewReader(test.wire)); !errors.Is(err, test.want) {
				t.Fatalf("ReadFrom error = %v, want %v", err, test.want)
			}
		})
	}

	t.Run("unknown component name on encode", func(t *testing.T) {
		slot := Slot{ID: 1, Count: 1, Components: []component.DataComponent{namedTestComponent{name: "example:unknown"}}}
		if _, err := slot.WriteTo(new(bytes.Buffer)); !errors.Is(err, component.ErrUnknownComponent) {
			t.Fatalf("WriteTo error = %v, want ErrUnknownComponent", err)
		}
	})

	t.Run("known component without codec on encode", func(t *testing.T) {
		slot := Slot{ID: 1, Count: 1, Components: []component.DataComponent{namedTestComponent{name: "minecraft:written_book_content"}}}
		if _, err := slot.WriteTo(new(bytes.Buffer)); !errors.Is(err, component.ErrUnsupportedComponent) {
			t.Fatalf("WriteTo error = %v, want ErrUnsupportedComponent", err)
		}
	})
}

func TestSlotContainsComponentCodecPanics(t *testing.T) {
	t.Run("decode", func(t *testing.T) {
		var slot Slot
		// minecraft:enchantments is registered as type 9, but its payload
		// decoder is intentionally not implemented and currently panics.
		_, err := slot.ReadFrom(bytes.NewReader([]byte{1, 1, 1, 0, 9}))
		if !errors.Is(err, ErrComponentCodecPanic) {
			t.Fatalf("ReadFrom error = %v, want ErrComponentCodecPanic", err)
		}
		var panicErr *ComponentCodecPanicError
		if !errors.As(err, &panicErr) || panicErr.Operation != "decode" || panicErr.TypeID != 9 {
			t.Fatalf("ReadFrom error details = %#v", panicErr)
		}
	})

	t.Run("encode", func(t *testing.T) {
		slot := Slot{
			ID:         1,
			Count:      1,
			Components: []component.DataComponent{&component.Enchantments{}},
		}
		_, err := slot.WriteTo(new(bytes.Buffer))
		if !errors.Is(err, ErrComponentCodecPanic) {
			t.Fatalf("WriteTo error = %v, want ErrComponentCodecPanic", err)
		}
		var panicErr *ComponentCodecPanicError
		if !errors.As(err, &panicErr) || panicErr.Operation != "encode" || panicErr.TypeID != 9 {
			t.Fatalf("WriteTo error details = %#v", panicErr)
		}
	})
}

func TestSlotValidatesCountsBeforeAllocation(t *testing.T) {
	var negative bytes.Buffer
	if _, err := pk.VarInt(-1).WriteTo(&negative); err != nil {
		t.Fatal(err)
	}
	var slot Slot
	if _, err := slot.ReadFrom(&negative); err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("negative item count error = %v", err)
	}

	tooMany := byte(component.TypeCount() + 1)
	if _, err := slot.ReadFrom(bytes.NewReader([]byte{1, 1, tooMany, 0})); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized component count error = %v", err)
	}

	var negativeID bytes.Buffer
	for _, value := range []pk.VarInt{1, -1, 0, 0} {
		if _, err := value.WriteTo(&negativeID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := slot.ReadFrom(&negativeID); err == nil || !strings.Contains(err.Error(), "item ID must not be negative") {
		t.Fatalf("negative item ID error = %v", err)
	}
}

type namedTestComponent struct {
	name string
}

func (c namedTestComponent) ID() string                      { return c.name }
func (namedTestComponent) ReadFrom(io.Reader) (int64, error) { return 0, nil }
func (namedTestComponent) WriteTo(io.Writer) (int64, error)  { return 0, nil }
