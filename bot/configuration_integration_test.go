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
	"github.com/imfusheng/go-mc/server"
)

func TestClientAndServerConfigurationAreWireCompatible(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	serverSocket, clientSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})
	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)
	config := server.Configurations{Registries: registry.NewNetworkCodec()}
	client := &Client{}

	serverResult := make(chan error, 1)
	go func() {
		if err := consumeClientConfigurationSettings(serverConn, profile); err != nil {
			serverResult <- err
			return
		}
		serverResult <- config.AcceptConfig(serverConn)
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
