package protocol

import (
	"fmt"
	"sort"
)

type packetSpec struct {
	state     State
	direction Direction
	kind      PacketKind
	id        int32
}

type profileSpec struct {
	versions     []Version
	capabilities Capabilities
	packets      []packetSpec
}

type catalogue struct {
	profiles []*Profile
	byName   map[string]*Profile
	byKey    map[Key]*Profile
	versions []Version
}

var defaultCatalogue = buildCatalogue(generatedProfileSpecs)

func buildCatalogue(specs []profileSpec) catalogue {
	c := catalogue{
		profiles: make([]*Profile, 0, len(specs)),
		byName:   make(map[string]*Profile),
		byKey:    make(map[Key]*Profile),
	}
	for _, spec := range specs {
		if len(spec.versions) == 0 {
			panic("protocol: profile without a version")
		}
		canonical := spec.versions[len(spec.versions)-1]
		key := canonical.Key()
		if key.Transport == TransportUnknown {
			panic(fmt.Sprintf("protocol: %s has unknown transport", canonical.Name))
		}
		if _, exists := c.byKey[key]; exists {
			panic(fmt.Sprintf("protocol: duplicate profile %s", key))
		}

		p := &Profile{
			key:          key,
			canonical:    canonical,
			aliases:      append([]Version(nil), spec.versions...),
			capabilities: spec.capabilities,
			byKind:       make(map[packetScope]map[PacketKind]int32),
			byID:         make(map[packetScope]map[int32]PacketKind),
		}
		for _, packet := range spec.packets {
			scope := packetScope{state: packet.state, direction: packet.direction}
			if p.byKind[scope] == nil {
				p.byKind[scope] = make(map[PacketKind]int32)
				p.byID[scope] = make(map[int32]PacketKind)
			}
			if previous, exists := p.byKind[scope][packet.kind]; exists {
				panic(fmt.Sprintf("protocol: duplicate %s/%s packet %q in %s (IDs %d and %d)", packet.state, packet.direction, packet.kind, key, previous, packet.id))
			}
			if previous, exists := p.byID[scope][packet.id]; exists {
				panic(fmt.Sprintf("protocol: duplicate %s/%s packet ID %d in %s (%q and %q)", packet.state, packet.direction, packet.id, key, previous, packet.kind))
			}
			p.byKind[scope][packet.kind] = packet.id
			p.byID[scope][packet.id] = packet.kind
		}

		c.profiles = append(c.profiles, p)
		c.byKey[key] = p
		for _, version := range p.aliases {
			if version.Key() != key {
				panic(fmt.Sprintf("protocol: version %s has key %s, expected %s", version.Name, version.Key(), key))
			}
			if _, exists := c.byName[version.Name]; exists {
				panic(fmt.Sprintf("protocol: duplicate version %s", version.Name))
			}
			c.byName[version.Name] = p
			c.versions = append(c.versions, version)
		}
	}
	return c
}

// ByName looks up a stable release such as "1.20.2" or "26.1".
func ByName(name string) (*Profile, bool) {
	p, ok := defaultCatalogue.byName[name]
	return p, ok
}

// MustByName is like ByName but panics when the release is unknown. It is
// intended for package defaults and tests, not untrusted input.
func MustByName(name string) *Profile {
	p, ok := ByName(name)
	if !ok {
		panic("protocol: unknown Minecraft Java Edition release " + name)
	}
	return p
}

// ByKey looks up an unambiguous wire profile.
func ByKey(key Key) (*Profile, bool) {
	p, ok := defaultCatalogue.byKey[key]
	return p, ok
}

// ByProtocol looks up a Netty profile by handshake protocol number. Legacy
// protocols do not carry an equivalent modern handshake; use ByName or ByKey
// for them. This also avoids the protocol-47 collision between 1.4.2 and 1.8.
func ByProtocol(number int32) (*Profile, bool) {
	return ByKey(Key{Transport: TransportNetty, Protocol: number})
}

// Profiles returns all known wire profiles in release order.
func Profiles() []*Profile {
	return append([]*Profile(nil), defaultCatalogue.profiles...)
}

// Versions returns all known stable releases in release order.
func Versions() []Version {
	return append([]Version(nil), defaultCatalogue.versions...)
}

// MajorVersions returns the latest stable release in every marketing version
// line (1.0 through 1.21, followed by 26.1 and later year-based lines).
func MajorVersions() []Version {
	latest := make(map[string]Version)
	order := make([]string, 0)
	for _, version := range defaultCatalogue.versions {
		if _, seen := latest[version.Major]; !seen {
			order = append(order, version.Major)
		}
		latest[version.Major] = version
	}
	result := make([]Version, 0, len(order))
	for _, major := range order {
		result = append(result, latest[major])
	}
	return result
}

// LatestRelease returns the newest stable release in the generated catalogue.
func LatestRelease() Version {
	if len(defaultCatalogue.versions) == 0 {
		return Version{}
	}
	return defaultCatalogue.versions[len(defaultCatalogue.versions)-1]
}

// SortedPacketKinds returns a stable view of the packets in one scope. It is
// useful for compatibility reports and generator tests.
func (p *Profile) SortedPacketKinds(state State, direction Direction) []PacketKind {
	m := p.byKind[packetScope{state: state, direction: direction}]
	result := make([]PacketKind, 0, len(m))
	for kind := range m {
		result = append(result, kind)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
