package bot

import (
	"errors"
	stdnet "net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
)

func TestConfigurationSettingsAndFinishAcrossProfiles(t *testing.T) {
	versions := []string{
		"1.20.2", // 764
		"1.20.3", // 765
		"1.20.5", // 766
		"1.21.1", // 767
		"1.21.2", // 768
		"1.21.9", // 773
		"26.2",   // 776
	}
	want := ConfigurationSettings{
		Locale:              "fr_fr",
		ViewDistance:        12,
		ChatMode:            2,
		ChatColors:          true,
		DisplayedSkinParts:  0x35,
		MainHand:            0,
		EnableTextFiltering: true,
		AllowServerListings: false,
		ParticleStatus:      2,
	}

	for _, version := range versions {
		version := version
		t.Run(version, func(t *testing.T) {
			client := &Client{ConfigurationSettings: &want}
			profile, server, result := startProfileConfiguration(t, version, client)
			assertConfigurationSettings(t, server, profile, want)
			finishProfileConfiguration(t, server, profile, result)
		})
	}
}

func TestConfigurationMandatoryRepliesAndUnknownPacketPreserveSync(t *testing.T) {
	client := NewClient()
	client.Cookies["minecraft:test"] = []byte{1, 2, 3}
	profile, server, result := startProfileConfiguration(t, "26.2", client)
	assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)

	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigKeepAlive, pk.Long(0x102030405060708))
	response := readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigKeepAlive)
	var keepAlive pk.Long
	if err := scanConfigurationPacket(response, &keepAlive); err != nil || keepAlive != 0x102030405060708 {
		t.Fatalf("keep-alive response = %d, %v", keepAlive, err)
	}

	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigPing, pk.Int(-1234567))
	response = readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigPong)
	var ping pk.Int
	if err := scanConfigurationPacket(response, &ping); err != nil || ping != -1234567 {
		t.Fatalf("pong response = %d, %v", ping, err)
	}

	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigCookieRequest, pk.Identifier("minecraft:test"))
	response = readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigCookieResponse)
	var cookieKey pk.Identifier
	var cookie pk.Option[pk.ByteArray, *pk.ByteArray]
	if err := scanConfigurationPacket(response, &cookieKey, &cookie); err != nil {
		t.Fatalf("decode cookie response: %v", err)
	}
	if cookieKey != "minecraft:test" || !cookie.Has || string(cookie.Val) != "\x01\x02\x03" {
		t.Fatalf("cookie response = key %q has %v payload %x", cookieKey, cookie.Has, cookie.Val)
	}

	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigSelectKnownPacks, pk.Array([]DataPack{}))
	response = readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigSelectKnownPacks)
	var packs []DataPack
	if err := scanConfigurationPacket(response, pk.Array(&packs)); err != nil || len(packs) != 0 {
		t.Fatalf("known-packs response = %v, %v", packs, err)
	}

	packID := pk.UUID(uuid.MustParse("12345678-1234-5678-90ab-cdef12345678"))
	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigResourcePackPush,
		packID,
		pk.String("https://example.invalid/pack.zip"),
		pk.String(""),
		pk.Boolean(false),
		pk.OptionEncoder[chat.Message]{},
	)
	response = readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigResourcePackResponse)
	var responsePackID pk.UUID
	var packStatus pk.VarInt
	if err := scanConfigurationPacket(response, &responsePackID, &packStatus); err != nil {
		t.Fatalf("decode resource-pack response: %v", err)
	}
	if responsePackID != packID || packStatus != resourcePackDeclined {
		t.Fatalf("resource-pack response = %s status %d", responsePackID, packStatus)
	}

	// clear_dialog is a mapped, non-blocking packet in p776. Its payload is
	// intentionally opaque here; the following Finish proves frame sync.
	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketKind("clear_dialog"), pk.Byte(0x55))
	finishProfileConfiguration(t, server, profile, result)
}

func TestConfigurationProtocol764ResourcePackResponse(t *testing.T) {
	client := NewClient()
	profile, server, result := startProfileConfiguration(t, "1.20.2", client)
	assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)

	prompt := chat.JsonMessage(chat.Text("Download this pack?"))
	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigResourcePackSend,
		pk.String("https://example.invalid/legacy.zip"),
		pk.String("sha1"),
		pk.Boolean(true),
		pk.OptionEncoder[chat.JsonMessage]{Has: true, Val: prompt},
	)
	response := readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigResourcePackResponse)
	var status pk.VarInt
	if err := scanConfigurationPacket(response, &status); err != nil || status != resourcePackDeclined {
		t.Fatalf("resource-pack response = %d, %v", status, err)
	}
	finishProfileConfiguration(t, server, profile, result)
}

func TestConfigurationProtocol765ResourcePackResponseIncludesUUID(t *testing.T) {
	client := NewClient()
	profile, server, result := startProfileConfiguration(t, "1.20.3", client)
	assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)

	packID := pk.UUID(uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))
	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigResourcePackPush,
		packID,
		pk.String("https://example.invalid/pack.zip"),
		pk.String(""),
		pk.Boolean(false),
		pk.OptionEncoder[chat.Message]{},
	)
	response := readConfigPacket(t, server)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigResourcePackResponse)
	var gotID pk.UUID
	var status pk.VarInt
	if err := scanConfigurationPacket(response, &gotID, &status); err != nil {
		t.Fatalf("decode resource-pack response: %v", err)
	}
	if gotID != packID || status != resourcePackDeclined {
		t.Fatalf("resource-pack response = %s status %d", gotID, status)
	}
	finishProfileConfiguration(t, server, profile, result)
}

func TestConfigurationCookieResponseAcrossProfiles(t *testing.T) {
	// p772 is intentionally included: its response has the same
	// Identifier+optional ByteArray schema as every protocol since p766.
	for _, version := range []string{"1.20.5", "1.21.7", "26.2"} {
		version := version
		t.Run(version, func(t *testing.T) {
			client := NewClient()
			client.Cookies["minecraft:session"] = []byte("value")
			profile, server, result := startProfileConfiguration(t, version, client)
			assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)
			writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigCookieRequest, pk.Identifier("minecraft:session"))
			response := readConfigPacket(t, server)
			assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigCookieResponse)
			var key pk.Identifier
			var value pk.Option[pk.ByteArray, *pk.ByteArray]
			if err := scanConfigurationPacket(response, &key, &value); err != nil {
				t.Fatalf("decode cookie response: %v", err)
			}
			if key != "minecraft:session" || !value.Has || string(value.Val) != "value" {
				t.Fatalf("cookie response = key %q has %v value %q", key, value.Has, value.Val)
			}
			finishProfileConfiguration(t, server, profile, result)
		})
	}
}

func TestConfigurationCodeOfConductRequiresExplicitConsent(t *testing.T) {
	t.Run("not accepted", func(t *testing.T) {
		client := NewClient()
		profile, server, result := startProfileConfiguration(t, "1.21.9", client)
		assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)
		writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigCodeOfConduct, pk.String("Be kind"))

		err := waitConfigurationResult(t, result)
		var consent *ConsentRequiredError
		if !errors.As(err, &consent) || consent.Text != "Be kind" {
			t.Fatalf("configuration error = %v; want ConsentRequiredError", err)
		}

		if server.Socket != nil {
			_ = server.Socket.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		}
		var unexpected pk.Packet
		err = server.ReadPacket(&unexpected)
		var netErr stdnet.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("client sent implicit acceptance packet %+v (read error %v)", unexpected, err)
		}
	})

	t.Run("accepted by callback", func(t *testing.T) {
		client := NewClient()
		client.CodeOfConduct = func(text string) bool { return text == "Be kind" }
		profile, server, result := startProfileConfiguration(t, "1.21.9", client)
		assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)
		writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigCodeOfConduct, pk.String("Be kind"))
		response := readConfigPacket(t, server)
		assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigAcceptCodeOfConduct)
		if err := requireEmptyConfigurationPacket(response); err != nil {
			t.Fatal(err)
		}
		finishProfileConfiguration(t, server, profile, result)
	})
}

func TestConfigurationTransferReturnsTypedTarget(t *testing.T) {
	client := NewClient()
	profile, server, result := startProfileConfiguration(t, "1.21.2", client)
	assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)
	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigTransfer,
		pk.String("new.example.test"), pk.VarInt(25570))

	err := waitConfigurationResult(t, result)
	var transfer *TransferError
	if !errors.As(err, &transfer) || transfer.Host != "new.example.test" || transfer.Port != 25570 {
		t.Fatalf("configuration error = %v; want TransferError new.example.test:25570", err)
	}
}

func TestConfigurationDisconnectEncodingFamilies(t *testing.T) {
	tests := []struct {
		version string
		reason  pk.FieldEncoder
	}{
		{version: "1.20.2", reason: chat.JsonMessage(chat.Text("json disconnect"))},
		{version: "1.20.3", reason: pk.NBT("nbt disconnect")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.version, func(t *testing.T) {
			client := NewClient()
			profile, server, result := startProfileConfiguration(t, test.version, client)
			assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)
			writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigDisconnect, test.reason)
			err := waitConfigurationResult(t, result)
			var disconnect DisconnectErr
			if !errors.As(err, &disconnect) {
				t.Fatalf("configuration error = %v; want DisconnectErr", err)
			}
		})
	}
}

func TestConfigurationRejectsMalformedMandatoryPayload(t *testing.T) {
	tests := []struct {
		name   string
		fields []pk.FieldEncoder
	}{
		{name: "truncated", fields: []pk.FieldEncoder{pk.Int(42)}},
		{name: "trailing", fields: []pk.FieldEncoder{pk.Long(42), pk.Byte(1)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			client := NewClient()
			profile, server, result := startProfileConfiguration(t, "26.2", client)
			assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)
			writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigKeepAlive, test.fields...)
			if err := waitConfigurationResult(t, result); err == nil {
				t.Fatal("malformed keep-alive was accepted")
			}
		})
	}
}

func TestConfigurationRejectsOversizedKnownPackCountBeforeAllocation(t *testing.T) {
	client := NewClient()
	profile, server, result := startProfileConfiguration(t, "26.2", client)
	assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)

	// The payload contains only a count. The generic packet array limit would
	// otherwise permit this value and allocate a very large []DataPack before
	// discovering that the first element is absent.
	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigSelectKnownPacks,
		pk.VarInt(pk.MaxDataLength),
	)
	err := waitConfigurationResult(t, result)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum "+strconv.Itoa(MaxConfigurationKnownPacks)) {
		t.Fatalf("joinConfiguration() error = %v, want known-pack limit error", err)
	}
}

func TestConfigurationRejectsOversizedUnknownRegistryTag(t *testing.T) {
	client := NewClient()
	profile, server, result := startProfileConfiguration(t, "1.21.1", client)
	assertConfigurationSettings(t, server, profile, *client.ConfigurationSettings)

	writeConfigPacket(t, server, profile, protocol.Clientbound, protocol.PacketConfigTags,
		pk.VarInt(1),
		pk.Identifier("minecraft:future_registry"),
		pk.VarInt(1),
		pk.Identifier("minecraft:oversized"),
		pk.VarInt(registry.MaxNetworkTagEntries+1),
	)
	err := waitConfigurationResult(t, result)
	if err == nil || !strings.Contains(err.Error(), "tag value count") {
		t.Fatalf("joinConfiguration() error = %v, want tag element limit error", err)
	}
}

func startProfileConfiguration(t *testing.T, version string, client *Client) (*protocol.Profile, *mcnet.Conn, <-chan error) {
	t.Helper()
	profile := protocol.MustByName(version)
	clientSocket, serverSocket := stdnet.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	if err := clientSocket.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := serverSocket.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	clientConn := mcnet.WrapConn(clientSocket)
	serverConn := mcnet.WrapConn(serverSocket)
	t.Cleanup(func() {
		_ = clientSocket.Close()
		_ = serverSocket.Close()
	})
	result := make(chan error, 1)
	go func() { result <- client.joinConfiguration(clientConn, profile) }()
	return profile, serverConn, result
}

func assertConfigurationSettings(t *testing.T, conn *mcnet.Conn, profile *protocol.Profile, want ConfigurationSettings) {
	t.Helper()
	p := readConfigPacket(t, conn)
	assertConfigPacketKind(t, profile, protocol.Serverbound, p, protocol.PacketConfigSettings)
	var locale pk.String
	var view pk.Byte
	var chatMode pk.VarInt
	var colors pk.Boolean
	var skin pk.UnsignedByte
	var mainHand pk.VarInt
	var filtering, listing pk.Boolean
	fields := []pk.FieldDecoder{&locale, &view, &chatMode, &colors, &skin, &mainHand, &filtering, &listing}
	var particles pk.VarInt
	if profile.Key().Protocol >= 768 {
		fields = append(fields, &particles)
	}
	if err := scanConfigurationPacket(p, fields...); err != nil {
		t.Fatalf("decode Configuration settings: %v", err)
	}
	if string(locale) != want.Locale || int8(view) != want.ViewDistance || int32(chatMode) != want.ChatMode ||
		bool(colors) != want.ChatColors || uint8(skin) != want.DisplayedSkinParts || int32(mainHand) != want.MainHand ||
		bool(filtering) != want.EnableTextFiltering || bool(listing) != want.AllowServerListings {
		t.Fatalf("settings = locale %q view %d chat %d colors %v skin %#x hand %d filtering %v listing %v",
			locale, view, chatMode, colors, skin, mainHand, filtering, listing)
	}
	if profile.Key().Protocol >= 768 && int32(particles) != want.ParticleStatus {
		t.Fatalf("particle status = %d, want %d", particles, want.ParticleStatus)
	}
}

func finishProfileConfiguration(t *testing.T, conn *mcnet.Conn, profile *protocol.Profile, result <-chan error) {
	t.Helper()
	writeConfigPacket(t, conn, profile, protocol.Clientbound, protocol.PacketConfigFinish)
	response := readConfigPacket(t, conn)
	assertConfigPacketKind(t, profile, protocol.Serverbound, response, protocol.PacketConfigFinish)
	if err := requireEmptyConfigurationPacket(response); err != nil {
		t.Fatal(err)
	}
	if err := waitConfigurationResult(t, result); err != nil {
		t.Fatalf("joinConfiguration: %v", err)
	}
}

func writeConfigPacket(t *testing.T, conn *mcnet.Conn, profile *protocol.Profile, direction protocol.Direction, kind protocol.PacketKind, fields ...pk.FieldEncoder) {
	t.Helper()
	id, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, direction, kind)
	if err != nil {
		t.Fatalf("resolve configuration packet %q: %v", kind, err)
	}
	if err := conn.WritePacket(pk.Marshal(id, fields...)); err != nil {
		t.Fatalf("write configuration packet %q: %v", kind, err)
	}
}

func readConfigPacket(t *testing.T, conn *mcnet.Conn) pk.Packet {
	t.Helper()
	var p pk.Packet
	if err := conn.ReadPacket(&p); err != nil {
		t.Fatalf("read configuration packet: %v", err)
	}
	return p
}

func assertConfigPacketKind(t *testing.T, profile *protocol.Profile, direction protocol.Direction, p pk.Packet, want protocol.PacketKind) {
	t.Helper()
	got, err := protocol.RequirePacketKind(profile, protocol.StateConfiguration, direction, p.ID)
	if err != nil {
		t.Fatalf("resolve configuration packet ID %d: %v", p.ID, err)
	}
	if got != want {
		t.Fatalf("configuration packet kind = %q, want %q", got, want)
	}
}

func waitConfigurationResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("joinConfiguration did not return")
		return nil
	}
}
