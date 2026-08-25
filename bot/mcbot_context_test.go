package bot

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	pk "github.com/imfusheng/go-mc/net/packet"
)

func TestDefaultPacketQueueChargesRetainedCapacity(t *testing.T) {
	queue := newDefaultPacketQueue(2, 20)
	largeBacking := pk.Packet{Data: make([]byte, 1, 10)}
	if !queue.Push(largeBacking) {
		t.Fatal("packet at exact retained-byte budget was rejected")
	}

	pushed := make(chan bool, 1)
	go func() {
		pushed <- queue.Push(pk.Packet{})
	}()
	select {
	case <-pushed:
		t.Fatal("second packet bypassed retained-capacity byte budget")
	case <-time.After(20 * time.Millisecond):
	}

	if _, ok := queue.Pull(); !ok {
		t.Fatal("failed to pull first packet")
	}
	select {
	case ok := <-pushed:
		if !ok {
			t.Fatal("blocked push failed after byte capacity became available")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked push did not resume")
	}
	queue.Close()
}

func TestWatchJoinContextCancelsAndClearsDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stop, err := watchJoinContext(client, ctx)
	if err != nil {
		t.Fatalf("watchJoinContext() error = %v", err)
	}
	cancel()

	readDone := make(chan error, 1)
	go func() {
		var one [1]byte
		_, err := client.Read(one[:])
		readDone <- err
	}()
	select {
	case err := <-readDone:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("Read after cancellation error = %v; want timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt socket read")
	}

	if err := stop(); err != nil {
		t.Fatalf("stop context watcher: %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := server.Write([]byte{42})
		writeDone <- err
	}()
	var one [1]byte
	if _, err := client.Read(one[:]); err != nil {
		t.Fatalf("Read after clearing deadline error = %v", err)
	}
	if one[0] != 42 {
		t.Fatalf("Read byte = %d; want 42", one[0])
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("peer Write error = %v", err)
	}
}

func TestWatchJoinContextRejectsCanceledContext(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := watchJoinContext(client, ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("watchJoinContext() error = %v; want context.Canceled", err)
	}
}
