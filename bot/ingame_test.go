package bot

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/net/queue"
	"github.com/imfusheng/go-mc/protocol"
)

func TestProtocol4PacketZeroIsKeepAliveNotBundle(t *testing.T) {
	profile := protocol.MustByName("1.7.5")
	client := newDispatchTestClient(profile)

	kind, err := client.resolvePlayPacketKind(0)
	if err != nil {
		t.Fatalf("resolvePlayPacketKind(0): %v", err)
	}
	if kind != protocol.PacketKind("keep_alive") {
		t.Fatalf("protocol 4 packet 0 kind = %q, want keep_alive", kind)
	}

	var semanticCalls, numericCalls int
	client.Events.AddSemanticListener(SemanticPacketHandler{
		Kind: kind,
		F: func(pk.Packet) error {
			semanticCalls++
			return nil
		},
	})
	client.Events.AddListener(PacketHandler{
		ID: packetid.BundleDelimiter,
		F: func(pk.Packet) error {
			numericCalls++
			return nil
		},
	})

	if err := client.handlePacket(pk.Packet{ID: 0}); err != nil {
		t.Fatalf("handlePacket(protocol 4 packet 0): %v", err)
	}
	if semanticCalls != 1 {
		t.Errorf("semantic keep_alive calls = %d, want 1", semanticCalls)
	}
	if numericCalls != 0 {
		t.Errorf("protocol-767 numeric ID-0 calls = %d, want 0", numericCalls)
	}
}

func TestBundleDelimiterUsesActiveProfileMapping(t *testing.T) {
	oldProfile := protocol.MustByName("1.7.5")
	if kind, err := newDispatchTestClient(oldProfile).resolvePlayPacketKind(0); err != nil || kind == bundleDelimiterKind {
		t.Fatalf("protocol 4 ID 0 = %q, %v; must not be a bundle delimiter", kind, err)
	}

	bundleProfile := protocol.MustByName("1.19.4") // protocol 762 introduced bundles
	delimiterID := requirePlayPacketID(t, bundleProfile, bundleDelimiterKind)
	if delimiterID != 0 {
		t.Fatalf("protocol 762 bundle delimiter ID = %d, want 0", delimiterID)
	}
	client := newDispatchTestClient(bundleProfile)
	err := client.handlePacket(pk.Packet{ID: delimiterID})
	if err == nil || !strings.Contains(err.Error(), "unexpected bundle delimiter") {
		t.Fatalf("direct bundle delimiter dispatch error = %v", err)
	}
}

func TestPlayDispatchMatrixUsesSemanticIDs(t *testing.T) {
	const expectedNettyProfiles = 51
	var tested int
	for _, profile := range protocol.Profiles() {
		if profile.Key().Transport != protocol.TransportNetty {
			continue
		}
		tested++
		profile := profile
		t.Run(profile.Key().String(), func(t *testing.T) {
			loginKind := protocol.PacketKind("login")
			loginID := requirePlayPacketID(t, profile, loginKind)
			client := newDispatchTestClient(profile)

			var order []string
			client.Events.AddGeneric(PacketHandler{
				F: func(pk.Packet) error {
					order = append(order, "generic")
					return nil
				},
			})
			client.Events.AddSemanticListener(
				SemanticPacketHandler{
					Kind:     loginKind,
					Priority: 5,
					F: func(pk.Packet) error {
						order = append(order, "semantic-high-first")
						return nil
					},
				},
				SemanticPacketHandler{
					Kind:     loginKind,
					Priority: 5,
					F: func(pk.Packet) error {
						order = append(order, "semantic-high-second")
						return nil
					},
				},
				SemanticPacketHandler{
					Kind: loginKind,
					F: func(pk.Packet) error {
						order = append(order, "semantic-low")
						return nil
					},
				},
			)
			client.Events.AddListener(PacketHandler{
				ID: packetid.ClientboundPacketID(loginID),
				F: func(pk.Packet) error {
					order = append(order, "numeric")
					return nil
				},
			})

			if err := client.handlePacket(pk.Packet{ID: loginID}); err != nil {
				t.Fatalf("handle login ID %d: %v", loginID, err)
			}

			want := []string{"generic", "semantic-high-first", "semantic-high-second", "semantic-low"}
			if usesBaselineNumericPacketIDs(profile) {
				want = append(want, "numeric")
			}
			if !slices.Equal(order, want) {
				t.Fatalf("handler order = %v, want %v", order, want)
			}
		})
	}
	if tested != expectedNettyProfiles {
		t.Fatalf("tested %d Netty profiles, want %d", tested, expectedNettyProfiles)
	}
}

func TestUnknownPlayPacketReturnsTypedMappingError(t *testing.T) {
	client := newDispatchTestClient(protocol.MustByName("1.7.5"))
	err := client.handlePacket(pk.Packet{ID: 1 << 20})
	if !errors.Is(err, ErrInvalidPacketID) {
		t.Fatalf("handlePacket() error = %v, want ErrInvalidPacketID", err)
	}
	if !errors.Is(err, protocol.ErrPacketMappingUnavailable) {
		t.Fatalf("handlePacket() error = %v, want ErrPacketMappingUnavailable", err)
	}

	var mappingErr *protocol.PacketMappingError
	if !errors.As(err, &mappingErr) {
		t.Fatalf("handlePacket() error type = %T, want PacketMappingError", err)
	}
	if !mappingErr.ByID || mappingErr.ID != 1<<20 || mappingErr.State != protocol.StatePlay || mappingErr.Direction != protocol.Clientbound {
		t.Errorf("PacketMappingError = %+v", mappingErr)
	}

	var handlerErr PacketHandlerError
	if !errors.As(err, &handlerErr) {
		t.Fatalf("handlePacket() error type = %T, want PacketHandlerError", err)
	}
	if got := int32(handlerErr.ID); got != 1<<20 {
		t.Errorf("PacketHandlerError.ID = %d, want %d", got, 1<<20)
	}
}

func TestNilPlayHandlersReturnContextualError(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	loginKind := protocol.PacketKind("login")
	loginID := requirePlayPacketID(t, profile, loginKind)

	tests := []struct {
		name string
		add  func(*Events)
		want string
	}{
		{
			name: "generic",
			add:  func(events *Events) { events.AddGeneric(PacketHandler{}) },
			want: "nil generic packet handler",
		},
		{
			name: "semantic",
			add: func(events *Events) {
				events.AddSemanticListener(SemanticPacketHandler{Kind: loginKind})
			},
			want: "nil semantic packet handler",
		},
		{
			name: "numeric baseline",
			add: func(events *Events) {
				events.AddListener(PacketHandler{ID: packetid.ClientboundPacketID(loginID)})
			},
			want: "nil packet handler",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newDispatchTestClient(profile)
			test.add(&client.Events)
			err := client.handlePacket(pk.Packet{ID: loginID})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("handlePacket() error = %v, want %q", err, test.want)
			}
			var handlerErr PacketHandlerError
			if !errors.As(err, &handlerErr) {
				t.Fatalf("handlePacket() error type = %T, want PacketHandlerError", err)
			}
			if handlerErr.Kind != loginKind || int32(handlerErr.ID) != loginID {
				t.Errorf("PacketHandlerError = %+v, want kind %q ID %d", handlerErr, loginKind, loginID)
			}
		})
	}
}

func TestHandleGameBundleDispatchAndBufferRelease(t *testing.T) {
	// Keep the goroutine on one P so immediate sync.Pool Gets below observe all
	// buffers returned by releasePacket without scheduling onto another P.
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	profile := protocol.MustByName("1.21.1")
	delimiterID := requirePlayPacketID(t, profile, bundleDelimiterKind)
	loginKind := protocol.PacketKind("login")
	loginID := requirePlayPacketID(t, profile, loginKind)

	recv := queue.NewChannelQueue[pk.Packet](3)
	for _, packet := range []pk.Packet{
		{ID: delimiterID, Data: make([]byte, 1, 11)},
		{ID: loginID, Data: make([]byte, 1, 13)},
		{ID: delimiterID, Data: make([]byte, 1, 17)},
	} {
		if !recv.Push(packet) {
			t.Fatal("failed to enqueue bundle packet")
		}
	}
	recv.Close()

	client := newDispatchTestClient(profile)
	client.Conn = &Conn{recv: recv}
	var calls int
	client.Events.AddSemanticListener(SemanticPacketHandler{
		Kind: loginKind,
		F: func(pk.Packet) error {
			calls++
			return nil
		},
	})

	err := client.HandleGame()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("HandleGame() error = %v, want EOF after queued bundle", err)
	}
	if calls != 1 {
		t.Fatalf("semantic login calls = %d, want 1", calls)
	}

	wantCaps := map[int]bool{11: true, 13: true, 17: true}
	for range 3 {
		value := client.Conn.pool.Get()
		data, ok := value.([]byte)
		if !ok {
			t.Fatalf("released pool value = %T, want []byte", value)
		}
		delete(wantCaps, cap(data))
	}
	if len(wantCaps) != 0 {
		t.Errorf("buffers with capacities %v were not returned to the pool", wantCaps)
	}
}

func TestBundleLimitReleasesBufferedPackets(t *testing.T) {
	profile := protocol.MustByName("1.19.4")
	loginID := requirePlayPacketID(t, profile, protocol.PacketKind("login"))
	recv := queue.NewChannelQueue[pk.Packet](maxBundlePackets)
	for i := 0; i < maxBundlePackets; i++ {
		if !recv.Push(pk.Packet{ID: loginID, Data: []byte{byte(i)}}) {
			t.Fatalf("failed to enqueue packet %d", i)
		}
	}
	client := newDispatchTestClient(profile)
	client.Conn = &Conn{recv: recv}
	err := client.handleBundlePackets()
	if err == nil || !strings.Contains(err.Error(), "out of limit") {
		t.Fatalf("handleBundlePackets() error = %v, want bundle limit error", err)
	}
	// Every buffered packet was returned. This intentionally checks only a
	// sample because sync.Pool may discard entries at any garbage collection.
	if value := client.Conn.pool.Get(); value == nil {
		t.Fatal("bundle limit path did not return packet buffers")
	}
}

func newDispatchTestClient(profile *protocol.Profile) *Client {
	return &Client{
		Profile: profile,
		Events: Events{
			handlers: make([][]PacketHandler, packetid.ClientboundPacketIDGuard),
		},
	}
}

func requirePlayPacketID(t *testing.T, profile *protocol.Profile, kind protocol.PacketKind) int32 {
	t.Helper()
	id, err := protocol.RequirePacketID(profile, protocol.StatePlay, protocol.Clientbound, kind)
	if err != nil {
		t.Fatalf("%s clientbound Play packet %q: %v", profile.Key(), kind, err)
	}
	return id
}

func TestPacketHandlerErrorFormattingWithoutSemanticKind(t *testing.T) {
	err := PacketHandlerError{ID: 7, Err: errors.New("boom")}
	if got, want := err.Error(), fmt.Sprintf("handle packet %v error: boom", packetid.ClientboundPacketID(7)); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
