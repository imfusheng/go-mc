package component_test

import (
	"errors"
	"io"
	"testing"

	"github.com/imfusheng/go-mc/data/registryid"
	"github.com/imfusheng/go-mc/level/component"
)

func TestProtocol767TypeRegistry(t *testing.T) {
	if got, want := component.TypeCount(), len(registryid.DataComponentType); got != want {
		t.Fatalf("TypeCount() = %d, want %d", got, want)
	}

	for id, wantName := range registryid.DataComponentType {
		name, ok := component.TypeName(int32(id))
		if !ok || name != wantName {
			t.Errorf("TypeName(%d) = %q, %v; want %q, true", id, name, ok, wantName)
		}

		value := component.NewComponent(int32(id))
		if value == nil {
			continue // Known protocol type whose payload codec is not implemented yet.
		}
		gotID, err := component.TypeID(value)
		if err != nil {
			t.Errorf("TypeID(NewComponent(%d)): %v", id, err)
			continue
		}
		if gotID != int32(id) {
			t.Errorf("TypeID(NewComponent(%d)) = %d", id, gotID)
		}
	}

	for _, id := range []int32{-1, int32(component.TypeCount())} {
		if name, ok := component.TypeName(id); ok {
			t.Errorf("TypeName(%d) = %q, true; want unknown", id, name)
		}
	}
}

func TestTypeIDRejectsNilAndUnknownComponents(t *testing.T) {
	if _, err := component.TypeID(nil); !errors.Is(err, component.ErrNilComponent) {
		t.Fatalf("TypeID(nil) error = %v, want ErrNilComponent", err)
	}

	var typedNil *component.MaxDamage
	if _, err := component.TypeID(typedNil); !errors.Is(err, component.ErrNilComponent) {
		t.Fatalf("TypeID(typed nil) error = %v, want ErrNilComponent", err)
	}

	if _, err := component.TypeID(unknownComponent{}); !errors.Is(err, component.ErrUnknownComponent) {
		t.Fatalf("TypeID(unknown) error = %v, want ErrUnknownComponent", err)
	}
}

type unknownComponent struct{}

func (unknownComponent) ID() string                        { return "example:unknown" }
func (unknownComponent) ReadFrom(io.Reader) (int64, error) { return 0, nil }
func (unknownComponent) WriteTo(io.Writer) (int64, error)  { return 0, nil }
