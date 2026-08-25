package registry

import (
	"bytes"
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

func wireBytes(field pk.FieldEncoder, t *testing.T) []byte {
	t.Helper()
	var wire bytes.Buffer
	if _, err := field.WriteTo(&wire); err != nil {
		t.Fatalf("encode field: %v", err)
	}
	return wire.Bytes()
}
