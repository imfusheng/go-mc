package registry

import (
	"bytes"
	"strings"
	"testing"

	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestRegistryReadFromPreservesIDsWithoutData(t *testing.T) {
	var wire bytes.Buffer
	fields := pk.Tuple{
		pk.VarInt(3),
		pk.Identifier("minecraft:first"), pk.Boolean(false),
		pk.Identifier("minecraft:second"), pk.Boolean(false),
		pk.Identifier("minecraft:third"), pk.Boolean(false),
	}
	written, err := fields.WriteTo(&wire)
	if err != nil {
		t.Fatalf("encode registry: %v", err)
	}

	reg := NewRegistry[struct{}]()
	read, err := reg.ReadFrom(&wire)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if read != written {
		t.Errorf("ReadFrom() bytes = %d, want %d", read, written)
	}
	if wire.Len() != 0 {
		t.Errorf("ReadFrom() left %d unread bytes", wire.Len())
	}

	for wantID, key := range []string{
		"minecraft:first",
		"minecraft:second",
		"minecraft:third",
	} {
		gotID, value := reg.Get(key)
		if gotID != int32(wantID) {
			t.Errorf("Get(%q) ID = %d, want %d", key, gotID, wantID)
		}
		if value == nil {
			t.Errorf("Get(%q) value is nil", key)
		}
		if reg.GetByID(int32(wantID)) == nil {
			t.Errorf("GetByID(%d) is nil", wantID)
		}
	}

	var roundTrip bytes.Buffer
	if _, err := reg.WriteTo(&roundTrip); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if !bytes.Equal(roundTrip.Bytes(), wireBytes(fields, t)) {
		t.Fatalf("round trip = %x, want %x", roundTrip.Bytes(), wireBytes(fields, t))
	}
}

func TestRegistryReadFromRejectsOversizedCountBeforeMutation(t *testing.T) {
	reg := NewRegistry[struct{}]()
	reg.Put("minecraft:existing", struct{}{})
	wire := wireBytes(pk.VarInt(MaxNetworkRegistryEntries+1), t)

	var gotErr error
	allocs := testing.AllocsPerRun(100, func() {
		_, gotErr = reg.ReadFrom(bytes.NewReader(wire))
	})
	if gotErr == nil || !strings.Contains(gotErr.Error(), "registry entry count") {
		t.Fatalf("ReadFrom() error = %v, want registry entry limit error", gotErr)
	}
	if id, value := reg.Get("minecraft:existing"); id != 0 || value == nil {
		t.Fatal("ReadFrom() mutated the registry before rejecting the count")
	}
	if allocs > 16 {
		t.Fatalf("ReadFrom() allocations = %.1f, want a small constant number", allocs)
	}
}

func TestRegistryReadTagsRejectsOversizedElementCountBeforeAllocation(t *testing.T) {
	reg := NewRegistry[struct{}]()
	reg.Put("minecraft:value", struct{}{})
	wire := wireBytes(pk.Tuple{
		pk.VarInt(1),
		pk.Identifier("minecraft:test"),
		pk.VarInt(MaxNetworkTagEntries + 1),
	}, t)

	var gotErr error
	allocs := testing.AllocsPerRun(100, func() {
		_, gotErr = reg.ReadTagsFrom(bytes.NewReader(wire))
	})
	if gotErr == nil || !strings.Contains(gotErr.Error(), "registry tag entry count") {
		t.Fatalf("ReadTagsFrom() error = %v, want tag entry limit error", gotErr)
	}
	if values := reg.Tag("minecraft:test"); values != nil {
		t.Fatalf("ReadTagsFrom() retained %d values after rejecting the count", len(values))
	}
	if allocs > 24 {
		t.Fatalf("ReadTagsFrom() allocations = %.1f, want a small constant number", allocs)
	}
}

func TestRegistryReadTagsRejectsOversizedTagCount(t *testing.T) {
	reg := NewRegistry[struct{}]()
	wire := wireBytes(pk.VarInt(MaxNetworkRegistryTags+1), t)
	if _, err := reg.ReadTagsFrom(bytes.NewReader(wire)); err == nil || !strings.Contains(err.Error(), "registry tag count") {
		t.Fatalf("ReadTagsFrom() error = %v, want tag count limit error", err)
	}
}

func wireBytes(field pk.FieldEncoder, t *testing.T) []byte {
	t.Helper()
	var wire bytes.Buffer
	if _, err := field.WriteTo(&wire); err != nil {
		t.Fatalf("encode field: %v", err)
	}
	return wire.Bytes()
}
