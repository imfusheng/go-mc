package bot

import (
	"errors"
	stdnet "net"
	"testing"
	"time"

	"github.com/imfusheng/go-mc/data/packetid"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
)

func TestClientConfigurationParsesRegistryWireFamilies(t *testing.T) {
	for _, version := range []string{"1.20.2", "1.20.4", "1.20.5", "1.21.1", "26.2"} {
		t.Run(version, func(t *testing.T) {
			testClientAndServerConfigurationAreWireCompatible(t, protocol.MustByName(version))
		})
	}
}

func testClientAndServerConfigurationAreWireCompatible(t *testing.T, profile *protocol.Profile) {
	t.Helper()
	serverSocket, clientSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})
	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)
	client := &Client{}

	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serveConfigurationWireFixture(serverConn, profile)
	}()
	clientResult := make(chan error, 1)
	go func() { clientResult <- client.joinConfiguration(clientConn, profile) }()

	for name, result := range map[string]<-chan error{
		"server": serverResult,
		"client": clientResult,
	} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("%s configuration: %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s configuration did not complete", name)
		}
	}
}

// serveConfigurationWireFixture deliberately sends empty registry fixtures:
// this integration test covers the client's versioned packet parser, not a
// playable Vanilla server registry datapack.
func serveConfigurationWireFixture(conn *mcnet.Conn, profile *protocol.Profile) error {
	if err := consumeClientConfigurationSettings(conn, profile); err != nil {
		return err
	}
	registries := registry.NewNetworkCodec()
	registryID, err := protocol.RequirePacketID(
		profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigRegistryData,
	)
	if err != nil {
		return err
	}
	switch profile.ConfigurationRegistryDataStyle() {
	case protocol.ConfigurationRegistryDataCompound:
		codec, err := registries.LegacyNetworkCodec()
		if err != nil {
			return err
		}
		if err := conn.WritePacket(pk.Marshal(registryID, codec)); err != nil {
			return err
		}
	case protocol.ConfigurationRegistryDataPerRegistry:
		for _, networkRegistry := range registries.NetworkRegistriesForProtocol(profile.Key().Protocol) {
			if err := conn.WritePacket(pk.Marshal(
				registryID, pk.Identifier(networkRegistry.ID), networkRegistry.Codec,
			)); err != nil {
				return err
			}
		}
	default:
		return protocol.UnsupportedCapabilityError{
			Version: profile.Version().Name, Capability: "configuration registry fixture",
		}
	}
	finishID, err := protocol.RequirePacketID(
		profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish,
	)
	if err != nil {
		return err
	}
	if err := conn.WritePacket(pk.Marshal(finishID)); err != nil {
		return err
	}
	var acknowledgement pk.Packet
	if err := conn.ReadPacket(&acknowledgement); err != nil {
		return err
	}
	wantAcknowledgement, err := protocol.RequirePacketID(
		profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish,
	)
	if err != nil {
		return err
	}
	if acknowledgement.ID != wantAcknowledgement || len(acknowledgement.Data) != 0 {
		return errors.New("wrong configuration acknowledgement")
	}
	return nil
}

func TestConfigurationPreservesUnknownRegistry(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	serverSocket, clientSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})
	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)
	client := &Client{}

	serverResult := make(chan error, 1)
	go func() {
		if err := consumeClientConfigurationSettings(serverConn, profile); err != nil {
			serverResult <- err
			return
		}
		if err := serverConn.WritePacket(pk.Marshal(
			packetid.ClientboundConfigRegistryData,
			pk.Identifier("minecraft:future_registry"),
			pk.VarInt(0),
		)); err != nil {
			serverResult <- err
			return
		}
		if err := serverConn.WritePacket(pk.Marshal(packetid.ClientboundConfigFinishConfiguration)); err != nil {
			serverResult <- err
			return
		}
		var ack pk.Packet
		if err := serverConn.ReadPacket(&ack); err != nil {
			serverResult <- err
			return
		}
		if ack.ID != int32(packetid.ServerboundConfigFinishConfiguration) {
			serverResult <- errors.New("wrong configuration acknowledgement")
			return
		}
		serverResult <- nil
	}()

	if err := client.joinConfiguration(clientConn, profile); err != nil {
		t.Fatalf("joinConfiguration: %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("server: %v", err)
	}
	if client.UnknownRegistries["minecraft:future_registry"] == nil {
		t.Fatal("unknown registry was discarded")
	}
	if client.ConfigHandler == nil || client.Cookies == nil || client.CustomReportDetails == nil {
		t.Fatal("joinConfiguration did not initialize optional client state")
	}
}

func TestConfigurationRejectsNegativeRegistryCountWithoutPanic(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	serverSocket, clientSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})
	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)

	go func() {
		if err := consumeClientConfigurationSettings(serverConn, profile); err != nil {
			return
		}
		_ = serverConn.WritePacket(pk.Marshal(
			packetid.ClientboundConfigRegistryData,
			pk.Identifier("minecraft:future_registry"),
			pk.VarInt(-1),
		))
	}()

	client := &Client{}
	if err := client.joinConfiguration(clientConn, profile); err == nil {
		t.Fatal("joinConfiguration() accepted a negative registry count")
	}
}

func TestConfigurationResourcePackPopUpdatesHandler(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	serverSocket, clientSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})
	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)
	handler := NewDefaultConfigHandler()
	handler.resourcesPack = append(handler.resourcesPack, ResourcePack{})
	client := NewClient()
	client.ConfigHandler = handler

	serverResult := make(chan error, 1)
	go func() {
		if err := consumeClientConfigurationSettings(serverConn, profile); err != nil {
			serverResult <- err
			return
		}
		if err := serverConn.WritePacket(pk.Marshal(
			packetid.ClientboundConfigResourcePackPop,
			pk.Boolean(false),
		)); err != nil {
			serverResult <- err
			return
		}
		if err := serverConn.WritePacket(pk.Marshal(packetid.ClientboundConfigFinishConfiguration)); err != nil {
			serverResult <- err
			return
		}
		var ack pk.Packet
		serverResult <- serverConn.ReadPacket(&ack)
	}()

	if err := client.joinConfiguration(clientConn, profile); err != nil {
		t.Fatalf("joinConfiguration() error = %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("server error = %v", err)
	}
	if len(handler.resourcesPack) != 0 {
		t.Fatalf("resource packs = %d, want 0", len(handler.resourcesPack))
	}
}

func consumeClientConfigurationSettings(conn *mcnet.Conn, profile *protocol.Profile) error {
	var p pk.Packet
	if err := conn.ReadPacket(&p); err != nil {
		return err
	}
	kind, err := protocol.RequirePacketKind(profile, protocol.StateConfiguration, protocol.Serverbound, p.ID)
	if err != nil {
		return err
	}
	if kind != protocol.PacketConfigSettings {
		return errors.New("expected client configuration settings")
	}
	return nil
}

func TestConfigurationRejectsMissingPacketProfile(t *testing.T) {
	clientSocket, serverSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = clientSocket.Close()
		_ = serverSocket.Close()
	})
	clientConn := mcnet.WrapConn(clientSocket)
	client := &Client{}

	for _, profile := range []*protocol.Profile{nil, protocol.MustByName("1.20.1")} {
		err := client.joinConfiguration(clientConn, profile)
		if !errors.Is(err, protocol.ErrPacketMappingUnavailable) {
			t.Fatalf("joinConfiguration(%v) error = %v; want ErrPacketMappingUnavailable", profile, err)
		}
		var mappingErr *protocol.PacketMappingError
		if !errors.As(err, &mappingErr) {
			t.Fatalf("joinConfiguration(%v) error type = %T; want *PacketMappingError", profile, err)
		}
	}
}
