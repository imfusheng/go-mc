package server

import (
	"bytes"
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
// Implementations that support profiles other than the legacy ConfigHandler
// p767 baseline should implement this interface.
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
// IDs and payload families. The compatibility level is checked before reading
// the required Client Information packet or writing any response bytes.
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
	// Configurations retains the protocol-767 typed registry schema that this
	// package historically exposed. Newer Vanilla releases changed required
	// registry schemas (not just packet framing), so emitting the same Go
	// structs under newer packet IDs would produce data the client rejects.
	// Servers targeting another profile must provide their own
	// ProfileConfigHandler with data generated for that exact release.
	if profile.Key().Protocol != 767 {
		return protocol.UnsupportedCapabilityError{
			Version: profile.Version().Name,
			Capability: "built-in server registry codec (only the protocol-767 schema baseline is available; " +
				"use a profile-specific ConfigHandler)",
		}
	}
	if err := validateBuiltInConfigurationRegistries(&c.Registries, profile); err != nil {
		return err
	}
	if err := readConfigurationClientInformation(conn, profile); err != nil {
		return err
	}
	registryDataID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigRegistryData)
	if err != nil {
		return err
	}
	switch profile.ConfigurationRegistryDataStyle() {
	case protocol.ConfigurationRegistryDataCompound:
		codec, codecErr := c.Registries.LegacyNetworkCodec()
		if codecErr != nil {
			return codecErr
		}
		if err = conn.WritePacket(pk.Marshal(registryDataID, codec)); err != nil {
			return err
		}
	case protocol.ConfigurationRegistryDataPerRegistry:
		for _, networkRegistry := range c.Registries.NetworkRegistriesForProtocol(profile.Key().Protocol) {
			err = conn.WritePacket(pk.Marshal(
				registryDataID,
				pk.Identifier(networkRegistry.ID),
				networkRegistry.Codec,
			))
			if err != nil {
				return err
			}
		}
	default:
		return protocol.UnsupportedCapabilityError{
			Version:    profile.Version().Name,
			Capability: "configuration registry data payload",
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

	customPayloads := 0
	for {
		var p pk.Packet
		if err = conn.ReadPacket(&p); err != nil {
			return err
		}
		kind, resolveErr := protocol.RequirePacketKind(profile, protocol.StateConfiguration, protocol.Serverbound, p.ID)
		if resolveErr != nil {
			return resolveErr
		}
		switch kind {
		case protocol.PacketConfigFinish:
			if len(p.Data) != 0 {
				return fmt.Errorf("finish configuration acknowledgement contains %d trailing bytes", len(p.Data))
			}
			return nil
		case protocol.PacketConfigCustomPayload:
			// Vanilla may send the minecraft:brand payload during Configuration.
			// Validate its framing before continuing to wait for the required ACK.
			customPayloads++
			if customPayloads > maxConfigurationCustomPayloads {
				return fmt.Errorf("configuration received more than %d custom payloads while waiting for finish acknowledgement", maxConfigurationCustomPayloads)
			}
			if err := validateConfigurationCustomPayload(p.Data); err != nil {
				return err
			}
		default:
			expected, expectedErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
			if expectedErr != nil {
				return expectedErr
			}
			return wrongPacketErr{
				expect: expected,
				get:    p.ID,
			}
		}
	}
}

type registryEntryCounter interface {
	Len() int
}

func validateBuiltInConfigurationRegistries(registries *registry.Registries, profile *protocol.Profile) error {
	if registries == nil {
		return errors.New("server: nil configuration registries")
	}
	for _, networkRegistry := range registries.NetworkRegistriesForProtocol(profile.Key().Protocol) {
		counter, ok := networkRegistry.Codec.(registryEntryCounter)
		if !ok {
			return fmt.Errorf("server configuration registry %q cannot be validated for emptiness", networkRegistry.ID)
		}
		if counter.Len() == 0 {
			return fmt.Errorf(
				"server configuration registry %q is empty; supply protocol-%d Vanilla registry data",
				networkRegistry.ID, profile.Key().Protocol,
			)
		}
	}
	return nil
}

func readConfigurationClientInformation(conn *net.Conn, profile *protocol.Profile) error {
	customPayloads := 0
	for {
		var p pk.Packet
		if err := conn.ReadPacket(&p); err != nil {
			return err
		}
		kind, err := protocol.RequirePacketKind(profile, protocol.StateConfiguration, protocol.Serverbound, p.ID)
		if err != nil {
			return err
		}
		if kind == protocol.PacketConfigCustomPayload {
			customPayloads++
			if customPayloads > maxConfigurationCustomPayloads {
				return fmt.Errorf("configuration received more than %d custom payloads before client information", maxConfigurationCustomPayloads)
			}
			if err := validateConfigurationCustomPayload(p.Data); err != nil {
				return err
			}
			continue
		}
		if kind != protocol.PacketConfigSettings {
			expected, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigSettings)
			if resolveErr != nil {
				return resolveErr
			}
			return wrongPacketErr{expect: expected, get: p.ID}
		}
		return validateConfigurationClientInformation(profile, p.Data)
	}
}

func validateConfigurationClientInformation(profile *protocol.Profile, data []byte) error {
	var locale pk.String
	var viewDistance pk.Byte
	var chatMode pk.VarInt
	var chatColors pk.Boolean
	var skinParts pk.UnsignedByte
	var mainHand pk.VarInt
	var textFiltering pk.Boolean
	var serverListings pk.Boolean
	fields := []pk.FieldDecoder{
		&locale,
		&viewDistance,
		&chatMode,
		&chatColors,
		&skinParts,
		&mainHand,
		&textFiltering,
		&serverListings,
	}
	var particleStatus pk.VarInt
	if profile.Key().Protocol >= 768 {
		fields = append(fields, &particleStatus)
	}
	if err := scanConfigurationFields(data, fields...); err != nil {
		return fmt.Errorf("client information: %w", err)
	}
	if len(locale) == 0 || len(locale) > 16 {
		return fmt.Errorf("client information locale byte length %d is outside range 1..16", len(locale))
	}
	if chatMode < 0 || chatMode > 2 {
		return fmt.Errorf("client information chat mode %d is outside range 0..2", chatMode)
	}
	if mainHand < 0 || mainHand > 1 {
		return fmt.Errorf("client information main hand %d is outside range 0..1", mainHand)
	}
	if profile.Key().Protocol >= 768 && (particleStatus < 0 || particleStatus > 2) {
		return fmt.Errorf("client information particle status %d is outside range 0..2", particleStatus)
	}
	return nil
}

const maxConfigurationCustomPayloads = 16

func validateConfigurationCustomPayload(data []byte) error {
	var channel pk.Identifier
	var payload pk.PluginMessageData
	if err := scanConfigurationFields(data, &channel, &payload); err != nil {
		return fmt.Errorf("configuration custom payload: %w", err)
	}
	if channel == "" {
		return errors.New("configuration custom payload has an empty channel")
	}
	return nil
}

func scanConfigurationFields(data []byte, fields ...pk.FieldDecoder) error {
	r := bytes.NewReader(data)
	for i, field := range fields {
		if field == nil {
			return fmt.Errorf("field %d is nil", i)
		}
		if _, err := field.ReadFrom(r); err != nil {
			return fmt.Errorf("field %d: %w", i, err)
		}
	}
	if r.Len() != 0 {
		return fmt.Errorf("contains %d trailing bytes", r.Len())
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
