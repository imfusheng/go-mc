package protocol

import (
	"errors"
	"testing"
)

func TestRequireLifecyclePacketBoundaries(t *testing.T) {
	tests := []struct {
		version   string
		state     State
		direction Direction
		kind      PacketKind
		want      int32
	}{
		{"1.7.5", StateLogin, Clientbound, PacketLoginSuccess, 2},
		{"1.7.5", StatePlay, Clientbound, "login", 1},
		{"1.7.10", StateStatus, Serverbound, PacketStatusPing, 1},
		{"1.20.2", StateConfiguration, Clientbound, PacketConfigRegistryData, 5},
		{"1.21.1", StateLogin, Serverbound, PacketLoginAcknowledged, 3},
		{"26.2", StateConfiguration, Clientbound, PacketConfigRegistryData, 7},
	}
	for _, test := range tests {
		t.Run(test.version+"/"+string(test.kind), func(t *testing.T) {
			profile := MustByName(test.version)
			id, err := RequirePacketID(profile, test.state, test.direction, test.kind)
			if err != nil || id != test.want {
				t.Fatalf("RequirePacketID() = %d, %v; want %d, nil", id, err, test.want)
			}
			gotKind, err := RequirePacketKind(profile, test.state, test.direction, id)
			if err != nil || gotKind != test.kind {
				t.Fatalf("RequirePacketKind() = %q, %v; want %q, nil", gotKind, err, test.kind)
			}
		})
	}
}

func TestRequireLifecyclePacketMissingMapping(t *testing.T) {
	tests := []struct {
		name      string
		profile   *Profile
		state     State
		direction Direction
		kind      PacketKind
	}{
		{"nil profile", nil, StateStatus, Serverbound, PacketStatusRequest},
		{"protocol 4 unframed legacy ping stays outside Profile", MustByName("1.7.5"), StateHandshake, Serverbound, "legacy_server_list_ping"},
		{"protocol 485 plugin response gap", MustByName("1.14.2"), StateLogin, Serverbound, PacketLoginPluginResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := RequirePacketID(test.profile, test.state, test.direction, test.kind)
			if !errors.Is(err, ErrPacketMappingUnavailable) {
				t.Fatalf("RequirePacketID() error = %v; want ErrPacketMappingUnavailable", err)
			}
			var mappingErr *PacketMappingError
			if !errors.As(err, &mappingErr) {
				t.Fatalf("RequirePacketID() error type = %T; want *PacketMappingError", err)
			}
		})
	}

	_, err := RequirePacketKind(MustByName("1.7.5"), StateHandshake, Serverbound, 0xfe)
	if !errors.Is(err, ErrPacketMappingUnavailable) {
		t.Fatalf("RequirePacketKind() error = %v; want ErrPacketMappingUnavailable", err)
	}
}
