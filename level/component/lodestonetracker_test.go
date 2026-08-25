package component_test

import (
	"bytes"
	"testing"

	"github.com/imfusheng/go-mc/level/component"
	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestLodestoneTrackerOptionalPosition(t *testing.T) {
	tests := []struct {
		name string
		want component.LodestoneTracker
	}{
		{
			name: "absent",
			want: component.LodestoneTracker{Tracked: true},
		},
		{
			name: "present",
			want: component.LodestoneTracker{
				HasGlobalPosition: true,
				Dimension:         "minecraft:overworld",
				Position:          pk.Position{X: 12, Y: 64, Z: 4},
				Tracked:           true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var wire bytes.Buffer
			if _, err := test.want.WriteTo(&wire); err != nil {
				t.Fatalf("WriteTo: %v", err)
			}

			var got component.LodestoneTracker
			if _, err := got.ReadFrom(bytes.NewReader(wire.Bytes())); err != nil {
				t.Fatalf("ReadFrom: %v", err)
			}
			if got != test.want {
				t.Errorf("round trip = %#v, want %#v", got, test.want)
			}
		})
	}
}
