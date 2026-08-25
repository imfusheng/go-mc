package bot

import (
	"errors"
	"fmt"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

const (
	bundleDelimiterKind protocol.PacketKind = "bundle_delimiter"
	maxBundlePackets                        = 4096
)

// ErrInvalidPacketID reports a clientbound packet ID that is absent from the
// active profile's Play packet table. The returned error also wraps a typed
// protocol.PacketMappingError with profile and direction context.
var ErrInvalidPacketID = errors.New("invalid clientbound packet ID")

// HandleGame receive server packet and response them correctly.
// Note that HandleGame will block if you don't receive from Events.
func (c *Client) HandleGame() error {
	if c == nil || c.Conn == nil {
		return errors.New("bot: client is not connected")
	}
	if c.Profile == nil {
		return errors.New("bot: active protocol profile is not set")
	}
	if c.Profile.Capabilities().PlayCore == protocol.Unsupported {
		return protocol.UnsupportedCapabilityError{
			Version: c.Profile.Version().Name, Capability: "play packet codecs",
		}
	}
	for {
		var p pk.Packet
		// Read packets
		if err := c.Conn.ReadPacket(&p); err != nil {
			return err
		}

		kind, err := c.resolvePlayPacketKind(p.ID)
		if err != nil {
			c.releasePacket(p)
			return err
		}
		if kind == bundleDelimiterKind {
			// The opening delimiter is control framing, not an event packet.
			c.releasePacket(p)
			if err := c.handleBundlePackets(); err != nil {
				return err
			}
			continue
		}

		err = c.handlePacketKind(p, kind)
		c.releasePacket(p)
		if err != nil {
			return err
		}
	}
}

type PacketHandlerError struct {
	ID   packetid.ClientboundPacketID
	Kind protocol.PacketKind
	Err  error
}

func (d PacketHandlerError) Error() string {
	if d.Kind != "" {
		return fmt.Sprintf("handle packet %q (ID %d) error: %v", d.Kind, d.ID, d.Err)
	}
	return fmt.Sprintf("handle packet %v error: %v", d.ID, d.Err)
}

func (d PacketHandlerError) Unwrap() error {
	return d.Err
}

func (c *Client) handleBundlePackets() (err error) {
	type resolvedPacket struct {
		packet pk.Packet
		kind   protocol.PacketKind
	}
	packets := make([]resolvedPacket, 0, 16)
	defer func() {
		for _, resolved := range packets {
			c.releasePacket(resolved.packet)
		}
	}()
	for i := 0; i < maxBundlePackets; i++ {
		var p pk.Packet
		// Read packets
		if err := c.Conn.ReadPacket(&p); err != nil {
			return err
		}

		kind, resolveErr := c.resolvePlayPacketKind(p.ID)
		if resolveErr != nil {
			c.releasePacket(p)
			return resolveErr
		}
		if kind == bundleDelimiterKind {
			// bundle finished
			c.releasePacket(p)
			goto handlePackets
		}

		// A bundle delimiter is consumed above and can therefore never be
		// recursively dispatched as the opening of a nested bundle.
		packets = append(packets, resolvedPacket{packet: p, kind: kind})
	}
	return errors.New("packet number of a bundle out of limit")

handlePackets:
	for i := range packets {
		if err := c.handlePacketKind(packets[i].packet, packets[i].kind); err != nil {
			return err
		}
	}
	return nil
}

// handlePacket resolves and dispatches one non-bundle Play packet. Bundle
// framing is owned by HandleGame and handleBundlePackets.
func (c *Client) handlePacket(p pk.Packet) error {
	kind, err := c.resolvePlayPacketKind(p.ID)
	if err != nil {
		return err
	}
	if kind == bundleDelimiterKind {
		return PacketHandlerError{
			ID:   packetid.ClientboundPacketID(p.ID),
			Kind: kind,
			Err:  errors.New("unexpected bundle delimiter outside bundle dispatcher"),
		}
	}
	return c.handlePacketKind(p, kind)
}

func (c *Client) resolvePlayPacketKind(id int32) (protocol.PacketKind, error) {
	kind, err := protocol.RequirePacketKind(c.Profile, protocol.StatePlay, protocol.Clientbound, id)
	if err != nil {
		return "", PacketHandlerError{
			ID:  packetid.ClientboundPacketID(id),
			Err: fmt.Errorf("%w: %w", ErrInvalidPacketID, err),
		}
	}
	return kind, nil
}

func (c *Client) handlePacketKind(p pk.Packet, kind protocol.PacketKind) (err error) {
	packetID := packetid.ClientboundPacketID(p.ID)
	for _, handler := range c.Events.generic {
		if handler.F == nil {
			return PacketHandlerError{ID: packetID, Kind: kind, Err: errors.New("nil generic packet handler")}
		}
		if err = handler.F(p); err != nil {
			return PacketHandlerError{ID: packetID, Kind: kind, Err: err}
		}
	}
	for _, handler := range c.Events.semantic[kind] {
		if handler.F == nil {
			return PacketHandlerError{ID: packetID, Kind: kind, Err: errors.New("nil semantic packet handler")}
		}
		if err = handler.F(p); err != nil {
			return PacketHandlerError{ID: packetID, Kind: kind, Err: err}
		}
	}
	if !usesBaselineNumericPacketIDs(c.Profile) {
		return nil
	}
	if int(packetID) >= len(c.Events.handlers) {
		return nil
	}
	for _, handler := range c.Events.handlers[packetID] {
		if handler.F == nil {
			return PacketHandlerError{ID: packetID, Kind: kind, Err: errors.New("nil packet handler")}
		}
		err = handler.F(p)
		if err != nil {
			return PacketHandlerError{ID: packetID, Kind: kind, Err: err}
		}
	}
	return
}

func usesBaselineNumericPacketIDs(profile *protocol.Profile) bool {
	return profile != nil && profile.Key() == (protocol.Key{
		Transport: protocol.TransportNetty,
		Protocol:  ProtocolVersion,
	})
}

func (c *Client) releasePacket(p pk.Packet) {
	if c != nil && c.Conn != nil && p.Data != nil {
		c.Conn.pool.Put(p.Data)
	}
}
