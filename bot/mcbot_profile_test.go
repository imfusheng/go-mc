package bot

import (
	"context"
	"errors"
	"testing"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/net/queue"
	"github.com/imfusheng/go-mc/protocol"
)

type recordingDialer struct {
	called bool
	err    error
}

func (d *recordingDialer) DialMCContext(context.Context, string) (*mcnet.Conn, error) {
	d.called = true
	return nil, d.err
}

func TestJoinServerRejectsLegacyProfileBeforeDial(t *testing.T) {
	dialer := &recordingDialer{err: errors.New("dial should not be called")}
	client := NewClient()
	err := client.JoinServerWithOptions("localhost:25565", JoinOptions{
		MCDialer: dialer,
		Profile:  protocol.MustByName("1.6.4"),
	})
	var unsupported protocol.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) {
		t.Fatalf("JoinServerWithOptions() error = %v, want UnsupportedCapabilityError", err)
	}
	if dialer.called {
		t.Fatal("legacy profile opened a Netty login connection")
	}
}

func TestJoinServerParsesExplicitPortAsDecimal(t *testing.T) {
	wantErr := errors.New("dial stopped")
	dialer := &recordingDialer{err: wantErr}
	client := NewClient()
	err := client.JoinServerWithOptions("localhost:080", JoinOptions{
		MCDialer: dialer,
		Profile:  protocol.MustByName("1.21.1"),
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("JoinServerWithOptions() error = %v, want %v", err, wantErr)
	}
	if !dialer.called {
		t.Fatal("decimal port was rejected before dialing")
	}
}

func TestClientCloseAndClosedQueueAreSafe(t *testing.T) {
	var client *Client
	if err := client.Close(); err != nil {
		t.Fatalf("nil Client.Close() error = %v", err)
	}

	conn := &Conn{send: queue.NewChannelQueue[pk.Packet](1)}
	if err := conn.Close(); err != nil {
		t.Fatalf("Conn.Close() error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Conn.Close() error = %v", err)
	}
	if err := conn.WritePacket(pk.Packet{}); err == nil {
		t.Fatal("WritePacket() succeeded after Close")
	}
}
