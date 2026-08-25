package server

import (
	"bytes"
	"errors"
	stdnet "net"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/data/packetid"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

func TestConfigurationsAcceptConfigWaitsForFinishAcknowledgement(t *testing.T) {
	serverConn, clientConn := pipeMC(t)
	config := Configurations{Registries: configurationStateMachineRegistries()}

	result := make(chan error, 1)
	go func() {
		result <- config.AcceptConfig(serverConn)
	}()

	writeConfigurationSettingsForProfile(t, clientConn, protocol.MustByName("1.21.1"))
	readConfigurationPackets(t, clientConn, len(config.Registries.NetworkRegistriesForProtocol(767)))

	select {
	case err := <-result:
		t.Fatalf("AcceptConfig() returned before acknowledgement: %v", err)
	default:
	}

	if err := clientConn.WritePacket(pk.Marshal(packetid.ServerboundConfigFinishConfiguration)); err != nil {
		t.Fatalf("write finish acknowledgement: %v", err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("AcceptConfig() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcceptConfig() did not accept finish acknowledgement")
	}
}

func TestConfigurationsAcceptConfigRejectsWrongAcknowledgement(t *testing.T) {
	serverConn, clientConn := pipeMC(t)
	config := Configurations{Registries: configurationStateMachineRegistries()}

	result := make(chan error, 1)
	go func() {
		result <- config.AcceptConfig(serverConn)
	}()

	writeConfigurationSettingsForProfile(t, clientConn, protocol.MustByName("1.21.1"))
	readConfigurationPackets(t, clientConn, len(config.Registries.NetworkRegistriesForProtocol(767)))
	if err := clientConn.WritePacket(pk.Marshal(packetid.ServerboundConfigPong)); err != nil {
		t.Fatalf("write wrong acknowledgement: %v", err)
	}

	select {
	case err := <-result:
		var wrong wrongPacketErr
		if !errors.As(err, &wrong) {
			t.Fatalf("AcceptConfig() error = %v, want wrongPacketErr", err)
		}
		if wrong.expect != int32(packetid.ServerboundConfigFinishConfiguration) {
			t.Errorf("wrongPacketErr.expect = %d", wrong.expect)
		}
		if wrong.get != int32(packetid.ServerboundConfigPong) {
			t.Errorf("wrongPacketErr.get = %d", wrong.get)
		}
	case <-time.After(time.Second):
		t.Fatal("AcceptConfig() did not reject wrong acknowledgement")
	}
}

func TestConfigurationsAcceptConfigRejectsTrailingAcknowledgementData(t *testing.T) {
	serverConn, clientConn := pipeMC(t)
	config := Configurations{Registries: configurationStateMachineRegistries()}
	result := make(chan error, 1)
	go func() { result <- config.AcceptConfig(serverConn) }()

	writeConfigurationSettingsForProfile(t, clientConn, protocol.MustByName("1.21.1"))
	readConfigurationPackets(t, clientConn, len(config.Registries.NetworkRegistriesForProtocol(767)))
	if err := clientConn.WritePacket(pk.Marshal(
		packetid.ServerboundConfigFinishConfiguration,
		pk.Byte(1),
	)); err != nil {
		t.Fatalf("write finish acknowledgement: %v", err)
	}
	if err := <-result; err == nil {
		t.Fatal("AcceptConfig() accepted trailing acknowledgement data")
	}
}

func TestConfigurationsAcceptsBrandPayloadBeforeInformationAndAcknowledgement(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	serverConn, clientConn := pipeMC(t)
	config := Configurations{Registries: configurationStateMachineRegistries()}
	result := make(chan error, 1)
	go func() { result <- config.AcceptConfigForProfile(serverConn, profile) }()

	writeConfigurationBrandPayload(t, clientConn, profile)
	writeConfigurationSettingsForProfile(t, clientConn, profile)
	readConfigurationPackets(t, clientConn, len(config.Registries.NetworkRegistriesForProtocol(profile.Key().Protocol)))
	writeConfigurationBrandPayload(t, clientConn, profile)
	ackID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientConn.WritePacket(pk.Marshal(ackID)); err != nil {
		t.Fatalf("write finish acknowledgement: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("AcceptConfigForProfile() error = %v", err)
	}
}

func TestConfigurationsAcceptConfigValidatesArguments(t *testing.T) {
	var config *Configurations
	if err := config.AcceptConfig(nil); err == nil {
		t.Fatal("nil Configurations.AcceptConfig() succeeded")
	}
	if err := (&Configurations{}).AcceptConfig(nil); err == nil {
		t.Fatal("Configurations.AcceptConfig(nil) succeeded")
	}
}

func TestConfigurationsAcceptConfigRejectsNilProfileWithTypedError(t *testing.T) {
	serverConn, _ := pipeMC(t)
	err := (&Configurations{}).AcceptConfigForProfile(serverConn, nil)
	if !errors.Is(err, protocol.ErrPacketMappingUnavailable) {
		t.Fatalf("AcceptConfigForProfile(nil) error = %v; want ErrPacketMappingUnavailable", err)
	}
	var mappingErr *protocol.PacketMappingError
	if !errors.As(err, &mappingErr) {
		t.Fatalf("AcceptConfigForProfile(nil) error type = %T; want *PacketMappingError", err)
	}
}

func TestBuiltInConfigurationsRejectsUnvalidatedSchemasAndEmptyData(t *testing.T) {
	for _, version := range []string{
		"1.20.2", "1.20.4", "1.20.5", "1.21.3", "1.21.5", "1.21.6", "1.21.11", "26.1", "26.2",
	} {
		t.Run(version, func(t *testing.T) {
			serverConn, _ := pipeMC(t)
			err := (&Configurations{Registries: configurationStateMachineRegistries()}).AcceptConfigForProfile(
				serverConn, protocol.MustByName(version),
			)
			var unsupported protocol.UnsupportedCapabilityError
			if !errors.As(err, &unsupported) {
				t.Fatalf("AcceptConfigForProfile() error = %v, want UnsupportedCapabilityError", err)
			}
		})
	}

	serverConn, _ := pipeMC(t)
	err := (&Configurations{Registries: registry.NewNetworkCodec()}).AcceptConfigForProfile(
		serverConn, protocol.MustByName("1.21.1"),
	)
	if err == nil {
		t.Fatal("protocol-767 configuration accepted empty required registries")
	}
	var unsupported protocol.UnsupportedCapabilityError
	if errors.As(err, &unsupported) {
		t.Fatalf("protocol-767 empty registry error = %v, want data validation error", err)
	}
}

func TestServerConfigurationHandlerProfileDispatch(t *testing.T) {
	serverConn, _ := pipeMC(t)
	latest := protocol.MustByName("26.2")
	baseline := protocol.MustByName("1.21.1")

	aware := &recordingProfileConfigHandler{}
	server := Server{ConfigHandler: aware}
	if err := server.acceptConfigForProfile(serverConn, latest); err != nil {
		t.Fatalf("profile-aware handler: %v", err)
	}
	if aware.profile != latest || aware.legacyCalls != 0 {
		t.Fatalf("profile-aware dispatch = profile %v, legacy calls %d", aware.profile, aware.legacyCalls)
	}

	legacyCalls := 0
	server.ConfigHandler = configHandlerFunc(func(*mcnet.Conn) error {
		legacyCalls++
		return nil
	})
	if err := server.acceptConfigForProfile(serverConn, baseline); err != nil {
		t.Fatalf("p767 legacy fallback: %v", err)
	}
	if legacyCalls != 1 {
		t.Fatalf("p767 legacy calls = %d, want 1", legacyCalls)
	}
	if err := server.acceptConfigForProfile(serverConn, latest); err == nil {
		t.Fatal("legacy handler accepted a non-p767 profile")
	} else {
		var unsupported protocol.UnsupportedCapabilityError
		if !errors.As(err, &unsupported) {
			t.Fatalf("non-p767 legacy handler error = %v, want UnsupportedCapabilityError", err)
		}
	}
	if legacyCalls != 1 {
		t.Fatalf("non-p767 dispatch called legacy handler; calls = %d", legacyCalls)
	}
}

func TestWriteConfigDisconnectUsesProfilePacketID(t *testing.T) {
	for _, version := range []string{"1.20.2", "1.21.1", "26.2"} {
		t.Run(version, func(t *testing.T) {
			profile := protocol.MustByName(version)
			serverConn, clientConn := pipeMC(t)
			result := make(chan error, 1)
			go func() {
				result <- writeConfigDisconnect(serverConn, profile, chat.Text("configuration rejected"))
			}()

			var packet pk.Packet
			if err := clientConn.ReadPacket(&packet); err != nil {
				t.Fatalf("read disconnect: %v", err)
			}
			expected, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigDisconnect)
			if err != nil {
				t.Fatal(err)
			}
			if packet.ID != expected {
				t.Fatalf("disconnect ID = %d, want %d", packet.ID, expected)
			}
			r := bytes.NewReader(packet.Data)
			var decoded chat.Message
			if profile.Key().Protocol == 764 {
				var jsonReason chat.JsonMessage
				if _, err := jsonReason.ReadFrom(r); err != nil {
					t.Fatalf("decode protocol-764 JSON disconnect: %v", err)
				}
				decoded = chat.Message(jsonReason)
			} else if _, err := decoded.ReadFrom(r); err != nil {
				t.Fatalf("decode anonymous-NBT disconnect: %v", err)
			}
			if decoded.ClearString() != "configuration rejected" || r.Len() != 0 {
				t.Fatalf("decoded reason = %q, trailing = %d", decoded.ClearString(), r.Len())
			}
			if err := <-result; err != nil {
				t.Fatalf("writeConfigDisconnect: %v", err)
			}
		})
	}

	serverConn, _ := pipeMC(t)
	err := writeConfigDisconnect(serverConn, protocol.MustByName("1.20.1"), chat.Text("unsupported"))
	if !errors.Is(err, protocol.ErrPacketMappingUnavailable) {
		t.Fatalf("pre-configuration disconnect error = %v; want ErrPacketMappingUnavailable", err)
	}
}

func TestAcceptConnStopsWhenConfigurationFails(t *testing.T) {
	serverConn, clientConn := pipeMC(t)
	wantErr := errors.New("configuration failed")
	gameplayCalled := make(chan struct{}, 1)

	s := Server{
		LoginHandler: loginHandlerFunc(func(*mcnet.Conn, int32) (string, uuid.UUID, *user.PublicKey, []user.Property, error) {
			return "tester", uuid.Nil, nil, nil, nil
		}),
		ConfigHandler: configHandlerFunc(func(*mcnet.Conn) error {
			return wantErr
		}),
		GamePlay: gamePlayFunc(func(string, uuid.UUID, *user.PublicKey, []user.Property, int32, *mcnet.Conn) {
			gameplayCalled <- struct{}{}
		}),
	}

	done := make(chan struct{})
	go func() {
		s.AcceptConn(serverConn)
		close(done)
	}()

	err := clientConn.WritePacket(pk.Marshal(
		0,
		pk.VarInt(767),
		pk.String("localhost"),
		pk.UnsignedShort(25565),
		pk.VarInt(2),
	))
	if err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AcceptConn() did not return after configuration error")
	}

	select {
	case <-gameplayCalled:
		t.Fatal("AcceptConn() called gameplay after configuration error")
	default:
	}
}

func TestAcceptConnEncodesConfigurationFailureAsAnonymousNBT(t *testing.T) {
	serverConn, clientConn := pipeMC(t)
	reason := chat.Text("configuration rejected")
	s := Server{
		LoginHandler: loginHandlerFunc(func(*mcnet.Conn, int32) (string, uuid.UUID, *user.PublicKey, []user.Property, error) {
			return "tester", uuid.Nil, nil, nil, nil
		}),
		ConfigHandler: configHandlerFunc(func(*mcnet.Conn) error {
			return ConfigFailErr{reason: reason}
		}),
		GamePlay: gamePlayFunc(func(string, uuid.UUID, *user.PublicKey, []user.Property, int32, *mcnet.Conn) {}),
	}
	go s.AcceptConn(serverConn)

	if err := clientConn.WritePacket(pk.Marshal(
		0,
		pk.VarInt(767),
		pk.String("localhost"),
		pk.UnsignedShort(25565),
		pk.VarInt(2),
	)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	var packet pk.Packet
	if err := clientConn.ReadPacket(&packet); err != nil {
		t.Fatalf("read configuration disconnect: %v", err)
	}
	if packet.ID != int32(packetid.ClientboundConfigDisconnect) {
		t.Fatalf("packet ID = %d, want configuration disconnect", packet.ID)
	}
	var decoded chat.Message
	r := bytes.NewReader(packet.Data)
	if _, err := decoded.ReadFrom(r); err != nil {
		t.Fatalf("decode configuration disconnect: %v", err)
	}
	if decoded.ClearString() != reason.ClearString() || r.Len() != 0 {
		t.Fatalf("decoded reason = %q, trailing = %d", decoded.ClearString(), r.Len())
	}
}

func pipeMC(t *testing.T) (*mcnet.Conn, *mcnet.Conn) {
	t.Helper()
	serverSocket, clientSocket := stdnet.Pipe()
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})
	return mcnet.WrapConn(serverSocket), mcnet.WrapConn(clientSocket)
}

// configurationStateMachineRegistries supplies structurally encodable entries
// so these tests exercise packet ordering and acknowledgements. They are not a
// claim that the placeholder values form a playable Vanilla datapack.
func configurationStateMachineRegistries() registry.Registries {
	registries := registry.NewNetworkCodec()
	registries.ChatType.PutWithoutData("minecraft:test")
	registries.DamageType.PutWithoutData("minecraft:test")
	registries.DimensionType.PutWithoutData("minecraft:test")
	registries.TrimMaterial.PutWithoutData("minecraft:test")
	registries.TrimPattern.PutWithoutData("minecraft:test")
	registries.WorldGenBiome.PutWithoutData("minecraft:test")
	registries.Wolfvariant.PutWithoutData("minecraft:test")
	registries.PaintingVariant.PutWithoutData("minecraft:test")
	registries.BannerPattern.PutWithoutData("minecraft:test")
	registries.Enchantment.PutWithoutData("minecraft:test")
	registries.JukeboxSong.PutWithoutData("minecraft:test")
	return registries
}

func readPacketID(t *testing.T, conn *mcnet.Conn, want int32) {
	t.Helper()
	var packet pk.Packet
	if err := conn.ReadPacket(&packet); err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if packet.ID != want {
		t.Fatalf("ReadPacket() ID = %d, want %d", packet.ID, want)
	}
}

func readConfigurationPackets(t *testing.T, conn *mcnet.Conn, registryCount int) {
	t.Helper()
	for i := 0; i < registryCount; i++ {
		readPacketID(t, conn, int32(packetid.ClientboundConfigRegistryData))
	}
	readPacketID(t, conn, int32(packetid.ClientboundConfigFinishConfiguration))
}

func writeConfigurationSettingsForProfile(t *testing.T, conn *mcnet.Conn, profile *protocol.Profile) {
	t.Helper()
	packetID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigSettings)
	if err != nil {
		t.Fatal(err)
	}
	fields := []pk.FieldEncoder{
		pk.String("en_us"),
		pk.Byte(8),
		pk.VarInt(0),
		pk.Boolean(true),
		pk.UnsignedByte(0x7f),
		pk.VarInt(1),
		pk.Boolean(false),
		pk.Boolean(true),
	}
	if profile.Key().Protocol >= 768 {
		fields = append(fields, pk.VarInt(0))
	}
	if err := conn.WritePacket(pk.Marshal(packetID, fields...)); err != nil {
		t.Fatalf("write configuration client information: %v", err)
	}
}

func writeConfigurationBrandPayload(t *testing.T, conn *mcnet.Conn, profile *protocol.Profile) {
	t.Helper()
	packetID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigCustomPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WritePacket(pk.Marshal(
		packetID,
		pk.Identifier("minecraft:brand"),
		pk.PluginMessageData{0x05, 'g', 'o', '-', 'm', 'c'},
	)); err != nil {
		t.Fatalf("write configuration brand payload: %v", err)
	}
}

type loginHandlerFunc func(*mcnet.Conn, int32) (string, uuid.UUID, *user.PublicKey, []user.Property, error)

func (f loginHandlerFunc) AcceptLogin(conn *mcnet.Conn, protocol int32) (string, uuid.UUID, *user.PublicKey, []user.Property, error) {
	return f(conn, protocol)
}

type configHandlerFunc func(*mcnet.Conn) error

func (f configHandlerFunc) AcceptConfig(conn *mcnet.Conn) error {
	return f(conn)
}

type recordingProfileConfigHandler struct {
	profile     *protocol.Profile
	legacyCalls int
}

func (h *recordingProfileConfigHandler) AcceptConfig(*mcnet.Conn) error {
	h.legacyCalls++
	return nil
}

func (h *recordingProfileConfigHandler) AcceptConfigForProfile(_ *mcnet.Conn, profile *protocol.Profile) error {
	h.profile = profile
	return nil
}

type gamePlayFunc func(string, uuid.UUID, *user.PublicKey, []user.Property, int32, *mcnet.Conn)

func (f gamePlayFunc) AcceptPlayer(name string, id uuid.UUID, key *user.PublicKey, properties []user.Property, protocol int32, conn *mcnet.Conn) {
	f(name, id, key, properties, protocol, conn)
}
