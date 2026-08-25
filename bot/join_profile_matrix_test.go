package bot

import (
	"bytes"
	"context"
	"fmt"
	stdnet "net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

const joinProfileMatrixCount = 51

type joinProfilePipeDialer struct {
	conn  *mcnet.Conn
	calls int
}

func (d *joinProfilePipeDialer) DialMCContext(ctx context.Context, _ string) (*mcnet.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if d == nil || d.conn == nil {
		return nil, fmt.Errorf("join profile test dialer has no connection")
	}
	d.calls++
	return d.conn, nil
}

func TestJoinServerWireAcrossEveryNettyProfile(t *testing.T) {
	var profiles []*protocol.Profile
	for _, profile := range protocol.Profiles() {
		if profile.Key().Transport == protocol.TransportNetty {
			profiles = append(profiles, profile)
		}
	}
	if got := len(profiles); got != joinProfileMatrixCount {
		t.Fatalf("Netty profile count = %d, want %d", got, joinProfileMatrixCount)
	}

	for _, profile := range profiles {
		profile := profile
		t.Run(profile.Version().Name, func(t *testing.T) {
			t.Parallel()
			testJoinServerWireProfile(t, profile)
		})
	}
}

func testJoinServerWireProfile(t *testing.T, profile *protocol.Profile) {
	t.Helper()

	capabilities := profile.Capabilities()
	if capabilities.PlayCore == protocol.Unsupported {
		t.Fatalf("Minecraft %s PlayCore capability is unsupported; promote it only after this wire test passes", profile.Version().Name)
	}
	if profile.HasConfigurationState() && capabilities.Configuration == protocol.Unsupported {
		t.Fatalf("Minecraft %s Configuration capability is unsupported; promote it only after this wire test passes", profile.Version().Name)
	}

	handshakeID := requireJoinProfilePacketID(t, profile, protocol.StateHandshake, protocol.Serverbound, protocol.PacketHandshakeSetProtocol)
	loginStartID := requireJoinProfilePacketID(t, profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginStart)
	loginSuccessID := requireJoinProfilePacketID(t, profile, protocol.StateLogin, protocol.Clientbound, protocol.PacketLoginSuccess)
	playLoginID := requireJoinProfilePacketID(t, profile, protocol.StatePlay, protocol.Clientbound, protocol.PacketKind("login"))

	var loginAcknowledgedID, configFinishID, configFinishAcknowledgedID int32
	if profile.HasConfigurationState() {
		loginAcknowledgedID = requireJoinProfilePacketID(t, profile, protocol.StateLogin, protocol.Serverbound, protocol.PacketLoginAcknowledged)
		configFinishID = requireJoinProfilePacketID(t, profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish)
		configFinishAcknowledgedID = requireJoinProfilePacketID(t, profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
	}

	clientSocket, serverSocket := stdnet.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	if err := clientSocket.SetDeadline(deadline); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	if err := serverSocket.SetDeadline(deadline); err != nil {
		t.Fatalf("set server deadline: %v", err)
	}

	clientWire := mcnet.WrapConn(clientSocket)
	serverWire := mcnet.WrapConn(serverSocket)
	dialer := &joinProfilePipeDialer{conn: clientWire}
	client := NewClient()
	client.Auth.Name = "ProfileTest"
	t.Cleanup(func() {
		_ = client.Close()
		_ = clientSocket.Close()
		_ = serverSocket.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	joinResult := make(chan error, 1)
	go func() {
		joinResult <- client.JoinServerWithOptions("matrix.invalid:25565", JoinOptions{
			Context:     ctx,
			MCDialer:    dialer,
			Profile:     profile,
			NoPublicKey: true,
		})
	}()

	var handshake pk.Packet
	if err := serverWire.ReadPacket(&handshake); err != nil {
		t.Fatalf("read Handshake: %v", err)
	}
	if handshake.ID != handshakeID {
		t.Fatalf("Handshake ID = %d, want %d", handshake.ID, handshakeID)
	}
	var (
		gotProtocol pk.VarInt
		gotHost     pk.String
		gotPort     pk.UnsignedShort
		gotNext     pk.VarInt
	)
	if err := scanJoinProfileFields(handshake.Data, &gotProtocol, &gotHost, &gotPort, &gotNext); err != nil {
		t.Fatalf("decode Handshake: %v", err)
	}
	if gotProtocol != pk.VarInt(profile.Key().Protocol) || gotHost != "matrix.invalid" || gotPort != 25565 || gotNext != 2 {
		t.Fatalf("Handshake = protocol %d host %q port %d next %d", gotProtocol, gotHost, gotPort, gotNext)
	}

	var loginStart pk.Packet
	if err := serverWire.ReadPacket(&loginStart); err != nil {
		t.Fatalf("read Login Start: %v", err)
	}
	if loginStart.ID != loginStartID {
		t.Fatalf("Login Start ID = %d, want %d", loginStart.ID, loginStartID)
	}
	assertLoginStartFixture(t, profile, loginStart.Data)

	wantUUID := uuid.MustParse("12345678-1234-5678-90ab-cdef12345678")
	wantSessionID := uuid.MustParse("87654321-4321-8765-ba09-876543210fed")
	if err := serverWire.WritePacket(joinProfileLoginSuccess(profile, loginSuccessID, wantUUID, wantSessionID)); err != nil {
		t.Fatalf("write Login Success: %v", err)
	}

	if profile.HasConfigurationState() {
		var loginAcknowledged pk.Packet
		if err := serverWire.ReadPacket(&loginAcknowledged); err != nil {
			t.Fatalf("read Login Acknowledged: %v", err)
		}
		if loginAcknowledged.ID != loginAcknowledgedID || len(loginAcknowledged.Data) != 0 {
			t.Fatalf("Login Acknowledged = ID %d data %x, want ID %d with empty data", loginAcknowledged.ID, loginAcknowledged.Data, loginAcknowledgedID)
		}
		assertConfigurationSettings(t, serverWire, profile, *client.ConfigurationSettings)

		if err := serverWire.WritePacket(pk.Marshal(configFinishID)); err != nil {
			t.Fatalf("write Configuration Finish: %v", err)
		}
		var finishAcknowledged pk.Packet
		if err := serverWire.ReadPacket(&finishAcknowledged); err != nil {
			t.Fatalf("read Configuration Finish acknowledgement: %v", err)
		}
		if finishAcknowledged.ID != configFinishAcknowledgedID || len(finishAcknowledged.Data) != 0 {
			t.Fatalf("Configuration Finish acknowledgement = ID %d data %x, want ID %d with empty data", finishAcknowledged.ID, finishAcknowledged.Data, configFinishAcknowledgedID)
		}
	}

	wantPlayData := []byte{0x51, 0x00, 0xca, 0xfe}
	playWriteResult := make(chan error, 1)
	go func() {
		playWriteResult <- serverWire.WritePacket(pk.Packet{ID: playLoginID, Data: wantPlayData})
	}()

	select {
	case err := <-joinResult:
		if err != nil {
			t.Fatalf("JoinServerWithOptions: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("JoinServerWithOptions did not enter Play: %v", ctx.Err())
	}
	if dialer.calls != 1 {
		t.Fatalf("dial calls = %d, want 1", dialer.calls)
	}
	if client.Profile != profile {
		t.Fatalf("Client.Profile = %p, want %p (%s)", client.Profile, profile, profile.Key())
	}
	if client.UUID != wantUUID || client.Name != "ProfileTest" {
		t.Fatalf("logged-in profile = %s/%s, want %s/ProfileTest", client.UUID, client.Name, wantUUID)
	}
	if profile.LoginSuccessHasSessionID() {
		if client.SessionID != wantSessionID {
			t.Fatalf("session ID = %s, want %s", client.SessionID, wantSessionID)
		}
	} else if client.SessionID != uuid.Nil {
		t.Fatalf("legacy session ID = %s, want nil", client.SessionID)
	}

	type packetResult struct {
		packet pk.Packet
		err    error
	}
	firstPlayResult := make(chan packetResult, 1)
	go func() {
		var packet pk.Packet
		err := client.Conn.ReadPacket(&packet)
		firstPlayResult <- packetResult{packet: packet, err: err}
	}()
	var firstPlay pk.Packet
	select {
	case result := <-firstPlayResult:
		if result.err != nil {
			t.Fatalf("read first Play packet: %v", result.err)
		}
		firstPlay = result.packet
	case <-ctx.Done():
		t.Fatalf("first Play packet was not exposed through Client.Conn: %v", ctx.Err())
	}

	kind, err := protocol.RequirePacketKind(profile, protocol.StatePlay, protocol.Clientbound, firstPlay.ID)
	if err != nil {
		t.Fatalf("resolve first Play packet ID %d: %v", firstPlay.ID, err)
	}
	if kind != protocol.PacketKind("login") {
		t.Fatalf("first Play packet kind = %q, want %q", kind, protocol.PacketKind("login"))
	}
	if !bytes.Equal(firstPlay.Data, wantPlayData) {
		t.Fatalf("first Play packet data = %x, want %x", firstPlay.Data, wantPlayData)
	}

	select {
	case err := <-playWriteResult:
		if err != nil {
			t.Fatalf("write first Play packet: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("server did not finish writing first Play packet: %v", ctx.Err())
	}
}

func requireJoinProfilePacketID(t *testing.T, profile *protocol.Profile, state protocol.State, direction protocol.Direction, kind protocol.PacketKind) int32 {
	t.Helper()
	id, err := protocol.RequirePacketID(profile, state, direction, kind)
	if err != nil {
		t.Fatalf("resolve %s/%s packet %q: %v", state, direction, kind, err)
	}
	return id
}

func scanJoinProfileFields(data []byte, fields ...pk.FieldDecoder) error {
	r := bytes.NewReader(data)
	for i, field := range fields {
		if _, err := field.ReadFrom(r); err != nil {
			return fmt.Errorf("field %d: %w", i, err)
		}
	}
	if r.Len() != 0 {
		return fmt.Errorf("%d trailing bytes", r.Len())
	}
	return nil
}

func joinProfileLoginSuccess(profile *protocol.Profile, packetID int32, id, sessionID uuid.UUID) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, 5)
	if profile.LoginSuccessUsesStringUUID() {
		wireID := id.String()
		if !profile.LoginSuccessStringUUIDUsesDashes() {
			wireID = strings.ReplaceAll(wireID, "-", "")
		}
		fields = append(fields, pk.String(wireID))
	} else {
		fields = append(fields, pk.UUID(id))
	}
	fields = append(fields, pk.String("ProfileTest"))
	if profile.LoginSuccessHasProperties() {
		fields = append(fields, pk.Array([]user.Property{}))
	}
	if profile.LoginSuccessHasStrictErrorHandling() {
		fields = append(fields, pk.Boolean(false))
	}
	if profile.LoginSuccessHasSessionID() {
		fields = append(fields, pk.UUID(sessionID))
	}
	return pk.Marshal(packetID, fields...)
}
