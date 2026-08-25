package bot

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

func TestEventsConcurrentRegistrationAndDispatch(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	kind := protocol.PacketKind("login")
	id, err := protocol.RequirePacketID(profile, protocol.StatePlay, protocol.Clientbound, kind)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		Profile: profile,
		Events: Events{
			handlers: make([][]PacketHandler, packetid.ClientboundPacketIDGuard),
		},
	}

	const iterations = 256
	var calls atomic.Int64
	handler := func(pk.Packet) error {
		calls.Add(1)
		return nil
	}
	start := make(chan struct{})
	errors := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for range iterations {
			client.Events.AddGeneric(PacketHandler{F: handler})
			client.Events.AddSemanticListener(SemanticPacketHandler{Kind: kind, F: handler})
			client.Events.AddListener(PacketHandler{ID: packetid.ClientboundPacketID(id), F: handler})
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range iterations {
			if err := client.handlePacketKind(pk.Packet{ID: id}, kind); err != nil {
				select {
				case errors <- err:
				default:
				}
				return
			}
			runtime.Gosched()
		}
	}()
	close(start)
	workers.Wait()
	close(errors)
	if err := <-errors; err != nil {
		t.Fatalf("concurrent dispatch: %v", err)
	}

	if err := client.handlePacketKind(pk.Packet{ID: id}, kind); err != nil {
		t.Fatalf("final dispatch: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("registered handlers were never dispatched")
	}
}

func TestEventsSnapshotIsImmutableAcrossRegistration(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	kind := protocol.PacketKind("login")
	id, err := protocol.RequirePacketID(profile, protocol.StatePlay, protocol.Clientbound, kind)
	if err != nil {
		t.Fatal(err)
	}
	events := Events{handlers: make([][]PacketHandler, packetid.ClientboundPacketIDGuard)}

	var called string
	low := func(pk.Packet) error { called += "low"; return nil }
	high := func(pk.Packet) error { called += "high"; return nil }
	events.AddGeneric(PacketHandler{F: low})
	events.AddSemanticListener(SemanticPacketHandler{Kind: kind, F: low})
	events.AddListener(PacketHandler{ID: packetid.ClientboundPacketID(id), F: low})

	generic, semantic, numeric := events.snapshot(kind, packetid.ClientboundPacketID(id), true)
	events.AddGeneric(PacketHandler{Priority: 1, F: high})
	events.AddSemanticListener(SemanticPacketHandler{Kind: kind, Priority: 1, F: high})
	events.AddListener(PacketHandler{ID: packetid.ClientboundPacketID(id), Priority: 1, F: high})

	if len(generic) != 1 || len(semantic) != 1 || len(numeric) != 1 {
		t.Fatalf("snapshot lengths = %d/%d/%d, want 1/1/1", len(generic), len(semantic), len(numeric))
	}
	for _, call := range []func(pk.Packet) error{generic[0].F, semantic[0].F, numeric[0].F} {
		if err := call(pk.Packet{}); err != nil {
			t.Fatal(err)
		}
	}
	if called != "lowlowlow" {
		t.Fatalf("snapshot changed after registration: calls = %q, want %q", called, "lowlowlow")
	}
}

func TestEventsSnapshotDoesNotAllocate(t *testing.T) {
	profile := protocol.MustByName("1.21.1")
	kind := protocol.PacketKind("login")
	id, err := protocol.RequirePacketID(profile, protocol.StatePlay, protocol.Clientbound, kind)
	if err != nil {
		t.Fatal(err)
	}
	events := Events{handlers: make([][]PacketHandler, packetid.ClientboundPacketIDGuard)}
	events.AddGeneric(PacketHandler{F: func(pk.Packet) error { return nil }})
	events.AddSemanticListener(SemanticPacketHandler{Kind: kind, F: func(pk.Packet) error { return nil }})
	events.AddListener(PacketHandler{ID: packetid.ClientboundPacketID(id), F: func(pk.Packet) error { return nil }})

	allocations := testing.AllocsPerRun(1000, func() {
		generic, semantic, numeric := events.snapshot(kind, packetid.ClientboundPacketID(id), true)
		if len(generic) != 1 || len(semantic) != 1 || len(numeric) != 1 {
			panic("unexpected snapshot length")
		}
	})
	if allocations != 0 {
		t.Fatalf("snapshot allocations per dispatch = %v, want 0", allocations)
	}
}
