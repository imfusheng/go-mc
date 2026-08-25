package bot

import (
	"sort"
	"strconv"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

type Events struct {
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
	for _, l := range listeners {
		// panic if l.ID is invalid
		if l.ID < 0 || int(l.ID) >= len(e.handlers) {
			panic("Invalid packet ID (" + strconv.Itoa(int(l.ID)) + ")")
		}
		if s := e.handlers[l.ID]; s == nil {
			e.handlers[l.ID] = []PacketHandler{l}
		} else {
			e.handlers[l.ID] = append(s, l)
			sortPacketHandlers(e.handlers[l.ID])
		}
	}
}

// AddSemanticListener registers handlers by their stable, version-independent
// packet kind. Semantic listeners run after generic listeners and before
// protocol-767 numeric listeners. Higher priority listeners run first; equal
// priorities retain registration order.
func (e *Events) AddSemanticListener(listeners ...SemanticPacketHandler) {
	if e.semantic == nil {
		e.semantic = make(map[protocol.PacketKind][]SemanticPacketHandler)
	}
	for _, listener := range listeners {
		if listener.Kind == "" {
			panic("Invalid empty packet kind")
		}
		e.semantic[listener.Kind] = append(e.semantic[listener.Kind], listener)
		sortSemanticPacketHandlers(e.semantic[listener.Kind])
	}
}

// AddGeneric adds listeners like AddListener, but the packet ID is ignored.
// Generic listener is always called before specific packet listener.
func (e *Events) AddGeneric(listeners ...PacketHandler) {
	e.generic = append(e.generic, listeners...)
	sortPacketHandlers(e.generic)
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
