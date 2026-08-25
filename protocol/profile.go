// Package protocol describes Minecraft Java Edition protocol versions and
// their wire-level capabilities.
//
// The package intentionally separates a marketing version (for example
// "1.20.2") from a wire profile. A wire profile is identified by both its
// transport and protocol number because Mojang reused protocol number 47 for
// the pre-Netty 1.4.2 protocol and the Netty-based 1.8 protocol.
package protocol

import "fmt"

// Transport identifies the framing family used on the wire.
type Transport uint8

const (
	TransportUnknown Transport = iota
	// TransportLegacy is the pre-1.7 packet stream.
	TransportLegacy
	// TransportNetty is the length-prefixed packet stream introduced in 1.7.
	TransportNetty
)

func (t Transport) String() string {
	switch t {
	case TransportLegacy:
		return "legacy"
	case TransportNetty:
		return "netty"
	default:
		return "unknown"
	}
}

// Version is one named Minecraft Java Edition release.
type Version struct {
	Name        string
	Major       string
	Protocol    int32
	DataVersion int32
	Transport   Transport
}

// Key returns the unambiguous wire-profile key for v.
func (v Version) Key() Key {
	return Key{Transport: v.Transport, Protocol: v.Protocol}
}

// Key uniquely identifies a wire protocol.
type Key struct {
	Transport Transport
	Protocol  int32
}

func (k Key) String() string {
	return fmt.Sprintf("%s/%d", k.Transport, k.Protocol)
}

// State is a connection state. Packet IDs are scoped to a state.
type State uint8

const (
	StateHandshake State = iota
	StateStatus
	StateLogin
	StateConfiguration
	StatePlay
)

func (s State) String() string {
	switch s {
	case StateHandshake:
		return "handshake"
	case StateStatus:
		return "status"
	case StateLogin:
		return "login"
	case StateConfiguration:
		return "configuration"
	case StatePlay:
		return "play"
	default:
		return fmt.Sprintf("state(%d)", s)
	}
}

// Direction is relative to the Minecraft server.
type Direction uint8

const (
	Clientbound Direction = iota
	Serverbound
)

func (d Direction) String() string {
	switch d {
	case Clientbound:
		return "clientbound"
	case Serverbound:
		return "serverbound"
	default:
		return fmt.Sprintf("direction(%d)", d)
	}
}

// PacketKind is a stable semantic packet name, independent of its numeric ID.
// Names use the form "keep_alive" or "login_start".
type PacketKind string

type packetScope struct {
	state     State
	direction Direction
}

// SupportLevel records what has actually been verified. A version appearing
// in the catalogue does not by itself mean every subsystem supports it.
type SupportLevel uint8

const (
	Unsupported SupportLevel = iota
	Experimental
	Verified
)

func (s SupportLevel) String() string {
	switch s {
	case Unsupported:
		return "unsupported"
	case Experimental:
		return "experimental"
	case Verified:
		return "verified"
	default:
		return fmt.Sprintf("support(%d)", s)
	}
}

// Capabilities is the public compatibility contract for a Profile.
type Capabilities struct {
	Status        SupportLevel // server-list status exchange
	Login         SupportLevel // isolated Netty Login state
	Configuration SupportLevel // Configuration state transition when present
	PlayCore      SupportLevel // raw Play transition, identity map, and semantic dispatch
	Chat          SupportLevel
	Inventory     SupportLevel
	World         SupportLevel
	Server        SupportLevel
}

// Profile describes one wire protocol. Multiple named releases may share it.
// Profiles are immutable after construction and safe for concurrent use.
type Profile struct {
	key          Key
	canonical    Version
	aliases      []Version
	capabilities Capabilities
	byKind       map[packetScope]map[PacketKind]int32
	byID         map[packetScope]map[int32]PacketKind
}

// Key returns the profile's wire key.
func (p *Profile) Key() Key { return p.key }

// Version returns the canonical (latest stable) release for this profile.
func (p *Profile) Version() Version { return p.canonical }

// Versions returns every stable release alias for this profile.
func (p *Profile) Versions() []Version { return append([]Version(nil), p.aliases...) }

// Capabilities returns the profile's verified support levels.
func (p *Profile) Capabilities() Capabilities { return p.capabilities }

// PacketID resolves a semantic packet to its version-specific numeric ID.
func (p *Profile) PacketID(state State, direction Direction, kind PacketKind) (int32, bool) {
	id, ok := p.byKind[packetScope{state: state, direction: direction}][kind]
	return id, ok
}

// PacketKind resolves a version-specific numeric ID to its semantic packet.
func (p *Profile) PacketKind(state State, direction Direction, id int32) (PacketKind, bool) {
	kind, ok := p.byID[packetScope{state: state, direction: direction}][id]
	return kind, ok
}

// UnsupportedCapabilityError reports an operation that a profile cannot
// safely encode or decode.
type UnsupportedCapabilityError struct {
	Version    string
	Capability string
}

func (e UnsupportedCapabilityError) Error() string {
	return fmt.Sprintf("protocol: Minecraft %s does not support %s in this build", e.Version, e.Capability)
}
