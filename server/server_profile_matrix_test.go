package server

import (
	"fmt"
	stdnet "net"
	"testing"
	"time"

	"github.com/google/uuid"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/offline"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

const serverProfileMatrixCount = 51

func TestAcceptConnAcrossEveryNettyProfile(t *testing.T) {
	var profiles []*protocol.Profile
	for _, profile := range protocol.Profiles() {
		if profile.Key().Transport == protocol.TransportNetty {
			profiles = append(profiles, profile)
		}
	}
	if got := len(profiles); got != serverProfileMatrixCount {
		t.Fatalf("Netty profile count = %d, want %d", got, serverProfileMatrixCount)
	}

	for _, profile := range profiles {
		profile := profile
		t.Run(profile.Version().Name, func(t *testing.T) {
			t.Parallel()
			testAcceptConnProfile(t, profile)
		})
	}
}

func testAcceptConnProfile(t *testing.T, profile *protocol.Profile) {
	t.Helper()

	capabilities := profile.Capabilities()
	if capabilities.Login == protocol.Unsupported {
		t.Fatalf("Minecraft %s Login capability is unsupported", profile.Version().Name)
	}
	if capabilities.PlayCore == protocol.Unsupported {
		t.Fatalf("Minecraft %s PlayCore capability is unsupported; promote it only after this AcceptConn test passes", profile.Version().Name)
	}
	if profile.HasConfigurationState() && capabilities.Configuration == protocol.Unsupported {
		t.Fatalf("Minecraft %s Configuration capability is unsupported; promote it only after this AcceptConn test passes", profile.Version().Name)
	}

	handshakeID := requireServerProfilePacketID(t, profile, protocol.StateHandshake, protocol.Serverbound, protocol.PacketHandshakeSetProtocol)
	loginSuccessID := requireServerProfilePacketID(t, profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSuccess)

	serverSocket, clientSocket := stdnet.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	if err := serverSocket.SetDeadline(deadline); err != nil {
		t.Fatalf("set server deadline: %v", err)
	}
	if err := clientSocket.SetDeadline(deadline); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}

	serverConn := mcnet.WrapConn(serverSocket)
	clientConn := mcnet.WrapConn(clientSocket)
	t.Cleanup(func() {
		_ = serverSocket.Close()
		_ = clientSocket.Close()
	})

	configCalls := make(chan *protocol.Profile, 1)
	gameplayCalls := make(chan serverProfileGameplayCall, 1)
	server := Server{
		LoginHandler: &MojangLoginHandler{Threshold: -1},
		ConfigHandler: &serverProfileConfigHandler{
			calls: configCalls,
		},
		GamePlay: &serverProfileGameplayRecorder{
			calls: gameplayCalls,
		},
	}
	serverDone := make(chan struct{})
	go func() {
		server.AcceptConn(serverConn)
		close(serverDone)
	}()

	if err := clientConn.WritePacket(pk.Marshal(
		handshakeID,
		pk.VarInt(profile.Key().Protocol),
		pk.String("matrix.invalid"),
		pk.UnsignedShort(25565),
		pk.VarInt(2),
	)); err != nil {
		t.Fatalf("write Handshake: %v", err)
	}
	if err := clientConn.WritePacket(loginStartFixture(profile)); err != nil {
		t.Fatalf("write Login Start: %v", err)
	}

	wantID := offline.NameToUUID("ProfileTest")
	var success pk.Packet
	if err := clientConn.ReadPacket(&success); err != nil {
		t.Fatalf("read Login Success: %v", err)
	}
	if success.ID != loginSuccessID {
		t.Fatalf("Login Success ID = %d, want %d", success.ID, loginSuccessID)
	}
	assertLoginSuccessFixture(t, profile, success.Data, wantID)

	if profile.HasLoginAcknowledgement() {
		ackID := requireServerProfilePacketID(t, profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginAcknowledged)
		if err := clientConn.WritePacket(pk.Marshal(ackID)); err != nil {
			t.Fatalf("write Login Acknowledged: %v", err)
		}
	}

	if profile.HasConfigurationState() {
		finishID := requireServerProfilePacketID(t, profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish)
		var finish pk.Packet
		if err := clientConn.ReadPacket(&finish); err != nil {
			t.Fatalf("read Configuration Finish: %v", err)
		}
		if finish.ID != finishID || len(finish.Data) != 0 {
			t.Fatalf("Configuration Finish = ID %d data %x, want ID %d with empty data", finish.ID, finish.Data, finishID)
		}

		finishAckID := requireServerProfilePacketID(t, profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
		if err := clientConn.WritePacket(pk.Marshal(finishAckID)); err != nil {
			t.Fatalf("write Configuration Finish acknowledgement: %v", err)
		}

		select {
		case gotProfile := <-configCalls:
			if gotProfile != profile {
				t.Fatalf("ConfigHandler profile = %v, want %v", gotProfile, profile)
			}
		case <-time.After(time.Second):
			t.Fatal("profile-aware ConfigHandler was not called")
		}
	}

	select {
	case call := <-gameplayCalls:
		if call.name != "ProfileTest" {
			t.Errorf("AcceptPlayer name = %q, want ProfileTest", call.name)
		}
		if call.id != wantID {
			t.Errorf("AcceptPlayer UUID = %s, want %s", call.id, wantID)
		}
		if call.profilePubKey != nil {
			t.Errorf("AcceptPlayer profile key = %v, want nil", call.profilePubKey)
		}
		if len(call.properties) != 0 {
			t.Errorf("AcceptPlayer properties = %v, want none", call.properties)
		}
		if call.protocol != profile.Key().Protocol {
			t.Errorf("AcceptPlayer protocol = %d, want %d", call.protocol, profile.Key().Protocol)
		}
	case <-time.After(time.Second):
		t.Fatal("AcceptPlayer was not called")
	}

	if !profile.HasConfigurationState() {
		select {
		case gotProfile := <-configCalls:
			t.Fatalf("pre-configuration profile unexpectedly called ConfigHandler with %v", gotProfile)
		default:
		}
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("AcceptConn did not return after AcceptPlayer")
	}
}

func requireServerProfilePacketID(t *testing.T, profile *protocol.Profile, state protocol.State, direction protocol.Direction, kind protocol.PacketKind) int32 {
	t.Helper()
	id, err := protocol.RequirePacketID(profile, state, direction, kind)
	if err != nil {
		t.Fatalf("resolve %s/%s packet %q: %v", state, direction, kind, err)
	}
	return id
}

type serverProfileConfigHandler struct {
	calls chan<- *protocol.Profile
}

func (*serverProfileConfigHandler) AcceptConfig(*mcnet.Conn) error {
	return fmt.Errorf("server profile matrix requires ProfileConfigHandler dispatch")
}

func (h *serverProfileConfigHandler) AcceptConfigForProfile(conn *mcnet.Conn, profile *protocol.Profile) error {
	finishID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish)
	if err != nil {
		return err
	}
	if err := conn.WritePacket(pk.Marshal(finishID)); err != nil {
		return fmt.Errorf("write Configuration Finish: %w", err)
	}

	var ack pk.Packet
	if err := conn.ReadPacket(&ack); err != nil {
		return fmt.Errorf("read Configuration Finish acknowledgement: %w", err)
	}
	kind, err := protocol.RequirePacketKind(profile, protocol.StateConfiguration, protocol.Serverbound, ack.ID)
	if err != nil {
		return err
	}
	if kind != protocol.PacketConfigFinish {
		return fmt.Errorf("Configuration acknowledgement kind = %q, want %q", kind, protocol.PacketConfigFinish)
	}
	if len(ack.Data) != 0 {
		return fmt.Errorf("Configuration Finish acknowledgement contains %d trailing bytes", len(ack.Data))
	}
	h.calls <- profile
	return nil
}

type serverProfileGameplayCall struct {
	name          string
	id            uuid.UUID
	profilePubKey *user.PublicKey
	properties    []user.Property
	protocol      int32
}

type serverProfileGameplayRecorder struct {
	calls chan<- serverProfileGameplayCall
}

func (r *serverProfileGameplayRecorder) AcceptPlayer(name string, id uuid.UUID, profilePubKey *user.PublicKey, properties []user.Property, protocolNumber int32, _ *mcnet.Conn) {
	r.calls <- serverProfileGameplayCall{
		name:          name,
		id:            id,
		profilePubKey: profilePubKey,
		properties:    append([]user.Property(nil), properties...),
		protocol:      protocolNumber,
	}
}
