package server

import (
	"errors"
	"fmt"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
)

type ConfigHandler interface {
	AcceptConfig(conn *net.Conn) error
}

// ProfileConfigHandler is the version-aware extension of ConfigHandler.
// Implementations that support more than the p767 compatibility baseline
// should implement this interface.
type ProfileConfigHandler interface {
	AcceptConfigForProfile(conn *net.Conn, profile *protocol.Profile) error
}

type Configurations struct {
	Registries registry.Registries
}

func (c *Configurations) AcceptConfig(conn *net.Conn) error {
	return c.AcceptConfigForProfile(conn, protocol.MustByName("1.21.1"))
}

// AcceptConfigForProfile performs configuration using profile-specific packet
// IDs. The compatibility level is checked before any bytes are written.
func (c *Configurations) AcceptConfigForProfile(conn *net.Conn, profile *protocol.Profile) error {
	if c == nil {
		return errors.New("server: nil configuration")
	}
	if conn == nil {
		return errors.New("server: nil connection")
	}
	if profile == nil {
		_, mappingErr := protocol.RequirePacketID(nil, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigRegistryData)
		return mappingErr
	}
	if profile.Capabilities().Configuration == protocol.Unsupported {
		return protocol.UnsupportedCapabilityError{Version: profile.Version().Name, Capability: "server configuration state"}
	}
	// The framework can dispatch every Configuration profile to a
	// ProfileConfigHandler, but this concrete registry codec is the audited
	// protocol-767 baseline. Registry payload schemas are not interchangeable.
	if profile.Key() != protocol.MustByName("1.21.1").Key() {
		return protocol.UnsupportedCapabilityError{
			Version:    profile.Version().Name,
			Capability: "built-in protocol-767 registry configuration codec",
		}
	}
	registryDataID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigRegistryData)
	if err != nil {
		return err
	}
	for _, networkRegistry := range c.Registries.NetworkRegistries() {
		err := conn.WritePacket(pk.Marshal(
			registryDataID,
			pk.Identifier(networkRegistry.ID),
			networkRegistry.Codec,
		))
		if err != nil {
			return err
		}
	}
	finishID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish)
	if err != nil {
		return err
	}
	err = conn.WritePacket(pk.Marshal(finishID))
	if err != nil {
		return err
	}

	var p pk.Packet
	if err = conn.ReadPacket(&p); err != nil {
		return err
	}
	kind, err := protocol.RequirePacketKind(profile, protocol.StateConfiguration, protocol.Serverbound, p.ID)
	if err != nil {
		return err
	}
	if kind != protocol.PacketConfigFinish {
		expected, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
		if resolveErr != nil {
			return resolveErr
		}
		return wrongPacketErr{
			expect: expected,
			get:    p.ID,
		}
	}
	if len(p.Data) != 0 {
		return fmt.Errorf("finish configuration acknowledgement contains %d trailing bytes", len(p.Data))
	}
	return nil
}

func (s *Server) acceptConfigForProfile(conn *net.Conn, profile *protocol.Profile) error {
	if profile == nil {
		_, mappingErr := protocol.RequirePacketID(nil, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish)
		return mappingErr
	}
	if s == nil || s.ConfigHandler == nil {
		return errors.New("server: nil configuration handler")
	}
	if handler, ok := s.ConfigHandler.(ProfileConfigHandler); ok {
		return handler.AcceptConfigForProfile(conn, profile)
	}
	baseline := protocol.MustByName("1.21.1")
	if profile.Key() != baseline.Key() {
		return protocol.UnsupportedCapabilityError{
			Version:    profile.Version().Name,
			Capability: "profile-aware configuration handler",
		}
	}
	return s.ConfigHandler.AcceptConfig(conn)
}

func writeConfigDisconnect(conn *net.Conn, profile *protocol.Profile, reason chat.Message) error {
	if conn == nil {
		return errors.New("server: nil connection")
	}
	packetID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigDisconnect)
	if err != nil {
		return err
	}
	if profile.Key().Protocol == 764 {
		return conn.WritePacket(pk.Marshal(packetID, chat.JsonMessage(reason)))
	}
	return conn.WritePacket(pk.Marshal(packetID, pk.NBT(networkChatMessage(reason))))
}

type ConfigFailErr struct {
	reason chat.Message
}

func (c ConfigFailErr) Error() string {
	return "config error: " + c.reason.ClearString()
}
