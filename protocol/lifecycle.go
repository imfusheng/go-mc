package protocol

import (
	"errors"
	"fmt"
)

// Packet kinds used before the Play state. These names are stable across
// protocol versions; Profile resolves them to the numeric ID used on the wire.
const (
	PacketHandshakeSetProtocol PacketKind = "set_protocol"

	PacketStatusRequest  PacketKind = "ping_start"
	PacketStatusResponse PacketKind = "server_info"
	PacketStatusPing     PacketKind = "ping"

	PacketLoginStart              PacketKind = "login_start"
	PacketLoginDisconnect         PacketKind = "disconnect"
	PacketLoginEncryptionRequest  PacketKind = "encryption_begin"
	PacketLoginEncryptionResponse PacketKind = "encryption_begin"
	PacketLoginSuccess            PacketKind = "success"
	PacketLoginSetCompression     PacketKind = "compress"
	PacketLoginPluginRequest      PacketKind = "login_plugin_request"
	PacketLoginPluginResponse     PacketKind = "login_plugin_response"
	PacketLoginAcknowledged       PacketKind = "login_acknowledged"
	PacketLoginCookieRequest      PacketKind = "cookie_request"
	PacketLoginCookieResponse     PacketKind = "cookie_response"

	PacketConfigCookieRequest  PacketKind = "cookie_request"
	PacketConfigCookieResponse PacketKind = "cookie_response"
	PacketConfigSettings       PacketKind = "settings"
	PacketConfigCustomPayload  PacketKind = "custom_payload"
	PacketConfigDisconnect     PacketKind = "disconnect"
	PacketConfigFinish         PacketKind = "finish_configuration"
	PacketConfigKeepAlive      PacketKind = "keep_alive"
	PacketConfigPing           PacketKind = "ping"
	PacketConfigPong           PacketKind = "pong"
	PacketConfigResetChat      PacketKind = "reset_chat"
	PacketConfigRegistryData   PacketKind = "registry_data"
	// PacketConfigResourcePackSend is the pre-UUID resource-pack request used
	// only by protocol 764 (Minecraft 1.20.2).
	PacketConfigResourcePackSend     PacketKind = "resource_pack_send"
	PacketConfigResourcePackPop      PacketKind = "remove_resource_pack"
	PacketConfigResourcePackPush     PacketKind = "add_resource_pack"
	PacketConfigResourcePackResponse PacketKind = "resource_pack_receive"
	PacketConfigStoreCookie          PacketKind = "store_cookie"
	PacketConfigTransfer             PacketKind = "transfer"
	PacketConfigFeatureFlags         PacketKind = "feature_flags"
	PacketConfigTags                 PacketKind = "tags"
	PacketConfigSelectKnownPacks     PacketKind = "select_known_packs"
	PacketConfigCustomReportDetails  PacketKind = "custom_report_details"
	PacketConfigServerLinks          PacketKind = "server_links"
	PacketConfigCodeOfConduct        PacketKind = "code_of_conduct"
	PacketConfigAcceptCodeOfConduct  PacketKind = "accept_code_of_conduct"
)

// ErrPacketMappingUnavailable is returned when a wire profile does not have
// an audited mapping for a requested semantic packet.
var ErrPacketMappingUnavailable = errors.New("protocol: packet mapping unavailable")

// PacketMappingError identifies the exact missing semantic or numeric lookup.
// It can be matched with errors.As and unwraps to ErrPacketMappingUnavailable.
type PacketMappingError struct {
	Version   string
	State     State
	Direction Direction
	Kind      PacketKind
	ID        int32
	ByID      bool
}

func (e *PacketMappingError) Error() string {
	version := e.Version
	if version == "" {
		version = "<nil>"
	}
	if e.ByID {
		return fmt.Sprintf("protocol: Minecraft %s has no audited %s/%s mapping for packet ID %d", version, e.State, e.Direction, e.ID)
	}
	return fmt.Sprintf("protocol: Minecraft %s has no audited %s/%s mapping for packet %q", version, e.State, e.Direction, e.Kind)
}

func (e *PacketMappingError) Unwrap() error { return ErrPacketMappingUnavailable }

// RequirePacketID resolves kind to a wire ID or returns a typed error.
func RequirePacketID(profile *Profile, state State, direction Direction, kind PacketKind) (int32, error) {
	if profile != nil {
		if id, ok := profile.PacketID(state, direction, kind); ok {
			return id, nil
		}
	}
	return 0, &PacketMappingError{
		Version:   profileVersion(profile),
		State:     state,
		Direction: direction,
		Kind:      kind,
	}
}

// RequirePacketKind resolves a wire ID to its stable semantic kind or returns
// a typed error. Callers should dispatch incoming lifecycle packets through
// this helper instead of comparing IDs from one protocol version.
func RequirePacketKind(profile *Profile, state State, direction Direction, id int32) (PacketKind, error) {
	if profile != nil {
		if kind, ok := profile.PacketKind(state, direction, id); ok {
			return kind, nil
		}
	}
	return "", &PacketMappingError{
		Version:   profileVersion(profile),
		State:     state,
		Direction: direction,
		ID:        id,
		ByID:      true,
	}
}

func profileVersion(profile *Profile) string {
	if profile == nil {
		return ""
	}
	return profile.Version().Name
}
