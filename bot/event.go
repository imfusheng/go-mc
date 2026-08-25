package bot

import (
	"sort"
	"strconv"
	"sync"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

type Events struct {
	mu       sync.RWMutex
	generic  []PacketHandler                                 // for every packet
	semantic map[protocol.PacketKind][]SemanticPacketHandler // for one semantic packet kind
	handlers [][]PacketHandler                               // protocol-767 packet IDs only
}

// AddListener registers protocol-767 numeric packet handlers.
//
// Numeric packet IDs are not stable between Minecraft versions. These
// listeners are therefore invoked only for the Netty protocol-767 baseline
// selected by NewClient. Use AddSemanticListener for handlers that should run
// with every supported profile.
func (e *Events) AddListener(listeners ...PacketHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, l := range listeners {
		// panic if l.ID is invalid
		if l.ID < 0 || int(l.ID) >= len(e.handlers) {
			panic("Invalid packet ID (" + strconv.Itoa(int(l.ID)) + ")")
		}
		current := e.handlers[l.ID]
		next := make([]PacketHandler, len(current), len(current)+1)
		copy(next, current)
		next = append(next, l)
		sortPacketHandlers(next)
		e.handlers[l.ID] = next
	}
}

// AddSemanticListener registers handlers by their stable, version-independent
// packet kind. Semantic listeners run after generic listeners and before
// protocol-767 numeric listeners. Higher priority listeners run first; equal
// priorities retain registration order.
func (e *Events) AddSemanticListener(listeners ...SemanticPacketHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.semantic == nil {
		e.semantic = make(map[protocol.PacketKind][]SemanticPacketHandler)
	}
	for _, listener := range listeners {
		if listener.Kind == "" {
			panic("Invalid empty packet kind")
		}
		current := e.semantic[listener.Kind]
		next := make([]SemanticPacketHandler, len(current), len(current)+1)
		copy(next, current)
		next = append(next, listener)
		sortSemanticPacketHandlers(next)
		e.semantic[listener.Kind] = next
	}
}

// AddGeneric adds listeners like AddListener, but the packet ID is ignored.
// Generic listener is always called before specific packet listener.
func (e *Events) AddGeneric(listeners ...PacketHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()

	next := make([]PacketHandler, len(e.generic), len(e.generic)+len(listeners))
	copy(next, e.generic)
	next = append(next, listeners...)
	sortPacketHandlers(next)
	e.generic = next
}

// snapshot returns immutable handler lists for one dispatch. Registration is
// allowed concurrently with packet handling, but a handler added after this
// snapshot starts is intentionally observed by the next packet.
func (e *Events) snapshot(kind protocol.PacketKind, packetID packetid.ClientboundPacketID, includeNumeric bool) (
	generic []PacketHandler,
	semantic []SemanticPacketHandler,
	numeric []PacketHandler,
) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	generic = e.generic
	semantic = e.semantic[kind]
	if includeNumeric && packetID >= 0 && int(packetID) < len(e.handlers) {
		numeric = e.handlers[packetID]
	}
	return generic, semantic, numeric
}

type (
	// PacketHandlerFunc receives a packet whose Data is borrowed and valid only
	// until the callback returns. Clone or re-encode Data before retaining it or
	// passing it to asynchronous code such as Conn.WritePacket.
	PacketHandlerFunc func(p pk.Packet) error
	PacketHandler     struct {
		ID       packetid.ClientboundPacketID
		Priority int
		// F receives borrowed packet data; see PacketHandlerFunc.
		F func(p pk.Packet) error
	}
	// SemanticPacketHandler associates a raw packet handler with a stable
	// protocol.PacketKind instead of a version-specific numeric ID.
	SemanticPacketHandler struct {
		Kind     protocol.PacketKind
		Priority int
		// F receives borrowed packet data; see PacketHandlerFunc.
		F func(p pk.Packet) error
	}
)

func sortPacketHandlers(slice []PacketHandler) {
	sort.SliceStable(slice, func(i, j int) bool {
		return slice[i].Priority > slice[j].Priority
	})
}

func sortSemanticPacketHandlers(slice []SemanticPacketHandler) {
	sort.SliceStable(slice, func(i, j int) bool {
		return slice[i].Priority > slice[j].Priority
	})
}
