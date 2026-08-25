package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/imfusheng/go-mc/bot"
	mcnet "github.com/imfusheng/go-mc/net"
	"github.com/imfusheng/go-mc/protocol"
)

func TestCatalogueVersionPreservesRequestedAlias(t *testing.T) {
	version, err := catalogueVersion("1.16.4")
	if err != nil {
		t.Fatal(err)
	}
	if version.Name != "1.16.4" {
		t.Fatalf("got canonical/shared profile alias %q, want exact requested alias", version.Name)
	}
	if version.Protocol != 754 || version.Transport != protocol.TransportNetty {
		t.Fatalf("unexpected version: %+v", version)
	}
}

func TestRunRetriesAndPassesSelectedLegacyVersion(t *testing.T) {
	var calls int
	ping := func(_ context.Context, address string, options bot.PingOptions) ([]byte, time.Duration, error) {
		calls++
		if address != "localhost:25570" {
			t.Fatalf("address = %q", address)
		}
		if options.Version.Name != "1.6.4" || options.Version.Transport != protocol.TransportLegacy {
			t.Fatalf("ping options version = %+v", options.Version)
		}
		if calls == 1 {
			return nil, 0, errors.New("connection refused")
		}
		return statusJSON(t, "1.6.4", 78), 12 * time.Millisecond, nil
	}

	var stdout strings.Builder
	err := run([]string{
		"-version", "1.6.4",
		"-address", "localhost:25570",
		"-timeout", "1s",
		"-attempt-timeout", "100ms",
		"-retry-interval", "0s",
	}, &stdout, ping, func(context.Context, string, *protocol.Profile) error {
		t.Fatal("legacy status verification must not attempt Netty login")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if got := stdout.String(); !strings.Contains(got, "status-only") || !strings.Contains(got, "transport=legacy") || !strings.Contains(got, "attempts=2") {
		t.Fatalf("unexpected output: %s", got)
	}
}

func TestRunPassesSelectedNettyVersion(t *testing.T) {
	ping := func(_ context.Context, _ string, options bot.PingOptions) ([]byte, time.Duration, error) {
		if options.Version.Name != "26.2" || options.Version.Transport != protocol.TransportNetty {
			t.Fatalf("ping options version = %+v", options.Version)
		}
		return statusJSON(t, "26.2", 776), time.Millisecond, nil
	}

	var joined *protocol.Profile
	join := func(_ context.Context, _ string, profile *protocol.Profile) error {
		joined = profile
		return nil
	}
	var stdout strings.Builder
	if err := run([]string{"-version", "26.2", "-timeout", "1s"}, &stdout, ping, join); err != nil {
		t.Fatal(err)
	}
	if joined != protocol.MustByName("26.2") {
		t.Fatalf("joined profile = %p, want exact selected profile %p", joined, protocol.MustByName("26.2"))
	}
	if got := stdout.String(); !strings.Contains(got, "entered-play") || !strings.Contains(got, "transport=netty") {
		t.Fatalf("unexpected output: %s", got)
	}
}

func TestRunRejectsUnknownVersionBeforePing(t *testing.T) {
	called := false
	ping := func(context.Context, string, bot.PingOptions) ([]byte, time.Duration, error) {
		called = true
		return nil, 0, nil
	}

	joinCalled := false
	err := run([]string{"-version", "does-not-exist"}, &strings.Builder{}, ping, func(context.Context, string, *protocol.Profile) error {
		joinCalled = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "not in the protocol catalogue") {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("ping called for unknown version")
	}
	if joinCalled {
		t.Fatal("join called for unknown version")
	}
}

func TestVerifyRejectsProtocolAndNameMismatch(t *testing.T) {
	version := protocol.MustByName("1.20.6").Version()
	tests := []struct {
		name         string
		reportedName string
		protocol     int32
		want         string
	}{
		{name: "protocol", reportedName: version.Name, protocol: version.Protocol + 1, want: "reported protocol"},
		{name: "name", reportedName: "1.20.5", protocol: version.Protocol, want: "reported version name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			cfg := config{
				address:        "localhost:25565",
				overallTimeout: 20 * time.Millisecond,
				attemptTimeout: 5 * time.Millisecond,
				retryInterval:  0,
			}
			ping := func(context.Context, string, bot.PingOptions) ([]byte, time.Duration, error) {
				return statusJSON(t, tc.reportedName, tc.protocol), 0, nil
			}
			_, err := verify(ctx, cfg, version, ping, func(context.Context, string, *protocol.Profile) error { return nil })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestVerifyStopsAtOverallDeadline(t *testing.T) {
	version := protocol.MustByName("1.20.6").Version()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cfg := config{
		address:        "localhost:25565",
		overallTimeout: 20 * time.Millisecond,
		attemptTimeout: time.Second,
		retryInterval:  time.Second,
	}
	ping := func(ctx context.Context, _ string, _ bot.PingOptions) ([]byte, time.Duration, error) {
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}

	started := time.Now()
	_, err := verify(ctx, cfg, version, ping, func(context.Context, string, *protocol.Profile) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "within 20ms") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("deadline took %s", elapsed)
	}
}

func TestVerifyRetriesInvalidJSONUntilDeadline(t *testing.T) {
	version := protocol.MustByName("1.20.6").Version()
	cfg := config{
		address:        "localhost:25565",
		overallTimeout: 20 * time.Millisecond,
		attemptTimeout: 5 * time.Millisecond,
		retryInterval:  0,
	}
	calls := 0
	ping := func(context.Context, string, bot.PingOptions) ([]byte, time.Duration, error) {
		calls++
		return []byte("not JSON"), 0, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.overallTimeout)
	defer cancel()
	_, err := verify(ctx, cfg, version, ping, func(context.Context, string, *protocol.Profile) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "invalid status JSON") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls < 2 {
		t.Fatalf("calls = %d, want at least 2 retries", calls)
	}
}

func TestVerifyRetriesPlaceholderStatusDuringStartup(t *testing.T) {
	version, err := catalogueVersion("1.7.10")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{
		address:        "localhost:25565",
		overallTimeout: time.Second,
		attemptTimeout: 100 * time.Millisecond,
		retryInterval:  0,
	}
	var calls int
	ping := func(context.Context, string, bot.PingOptions) ([]byte, time.Duration, error) {
		calls++
		if calls == 1 {
			return statusJSON(t, "", 0), 0, nil
		}
		return statusJSON(t, "1.7.10", 5), time.Millisecond, nil
	}
	var joins int
	join := func(context.Context, string, *protocol.Profile) error {
		joins++
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.overallTimeout)
	defer cancel()
	got, err := verify(ctx, cfg, version, ping, join)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || joins != 1 || got.attempts != 2 || !got.enteredPlay {
		t.Fatalf("calls=%d joins=%d result=%+v", calls, joins, got)
	}
}

func TestRunValidatesDurations(t *testing.T) {
	for _, args := range [][]string{
		{"-version", "1.20.6", "-timeout", "0s"},
		{"-version", "1.20.6", "-attempt-timeout", "0s"},
		{"-version", "1.20.6", "-retry-interval", "-1s"},
	} {
		err := run(args, &strings.Builder{}, nil, nil)
		if err == nil {
			t.Fatalf("run(%v) succeeded", args)
		}
	}
}

func TestVerifyRetriesWholeNettyAttemptStatusBeforeJoin(t *testing.T) {
	version, err := catalogueVersion("1.7.10")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{
		address:        "localhost:25565",
		overallTimeout: time.Second,
		attemptTimeout: 100 * time.Millisecond,
		retryInterval:  0,
	}
	var events []string
	pingCalls := 0
	ping := func(ctx context.Context, _ string, options bot.PingOptions) ([]byte, time.Duration, error) {
		pingCalls++
		events = append(events, fmt.Sprintf("status-%d", pingCalls))
		if options.Version.Name != "1.7.10" || options.Version.Protocol != 5 {
			t.Fatalf("status options version = %+v", options.Version)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("status attempt has no deadline")
		}
		return statusJSON(t, "1.7.10", 5), time.Millisecond, nil
	}
	joinCalls := 0
	join := func(ctx context.Context, address string, profile *protocol.Profile) error {
		joinCalls++
		events = append(events, fmt.Sprintf("join-%d", joinCalls))
		if address != cfg.address || profile != protocol.MustByName("1.7.10") {
			t.Fatalf("join = address %q profile %p", address, profile)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > cfg.attemptTimeout {
			t.Fatalf("join deadline = %v, ok=%v", deadline, ok)
		}
		if joinCalls == 1 {
			return errors.New("server still preparing spawn")
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.overallTimeout)
	defer cancel()
	got, err := verify(ctx, cfg, version, ping, join)
	if err != nil {
		t.Fatal(err)
	}
	if !got.enteredPlay || got.attempts != 2 {
		t.Fatalf("result = %+v", got)
	}
	if want := "status-1,join-1,status-2,join-2"; strings.Join(events, ",") != want {
		t.Fatalf("events = %v, want %s", events, want)
	}
}

func TestVerifyJoinIsBoundedByAttemptDeadline(t *testing.T) {
	version, err := catalogueVersion("1.7.10")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{
		address:        "localhost:25565",
		overallTimeout: 35 * time.Millisecond,
		attemptTimeout: 10 * time.Millisecond,
		retryInterval:  0,
	}
	ping := func(context.Context, string, bot.PingOptions) ([]byte, time.Duration, error) {
		return statusJSON(t, "1.7.10", 5), 0, nil
	}
	join := func(ctx context.Context, _ string, _ *protocol.Profile) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.overallTimeout)
	defer cancel()
	started := time.Now()
	_, err = verify(ctx, cfg, version, ping, join)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("deadline took %s", elapsed)
	}
}

type mcDialFunc func(context.Context, string) (*mcnet.Conn, error)

func (f mcDialFunc) DialMCContext(ctx context.Context, address string) (*mcnet.Conn, error) {
	return f(ctx, address)
}

type recordingDeadlineConn struct {
	net.Conn
	deadline time.Time
	closed   bool
	setErr   error
}

func (c *recordingDeadlineConn) SetDeadline(deadline time.Time) error {
	c.deadline = deadline
	return c.setErr
}

func (c *recordingDeadlineConn) Close() error {
	c.closed = true
	return c.Conn.Close()
}

func TestDeadlineDialerAppliesContextDeadlineToSocket(t *testing.T) {
	clientSocket, serverSocket := net.Pipe()
	t.Cleanup(func() { _ = serverSocket.Close() })
	recorded := &recordingDeadlineConn{Conn: clientSocket}
	base := mcDialFunc(func(_ context.Context, address string) (*mcnet.Conn, error) {
		if address != "localhost:25565" {
			t.Fatalf("address = %q", address)
		}
		return mcnet.WrapConn(recorded), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()

	conn, err := (deadlineDialer{base: base}).DialMCContext(ctx, "localhost:25565")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if !recorded.deadline.Equal(wantDeadline) {
		t.Fatalf("socket deadline = %v, want %v", recorded.deadline, wantDeadline)
	}
}

func TestDeadlineDialerClosesSocketWhenDeadlineCannotBeSet(t *testing.T) {
	clientSocket, serverSocket := net.Pipe()
	t.Cleanup(func() { _ = serverSocket.Close() })
	recorded := &recordingDeadlineConn{Conn: clientSocket, setErr: errors.New("deadline unsupported")}
	base := mcDialFunc(func(context.Context, string) (*mcnet.Conn, error) {
		return mcnet.WrapConn(recorded), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := (deadlineDialer{base: base}).DialMCContext(ctx, "localhost:25565")
	if err == nil || !strings.Contains(err.Error(), "set Minecraft connection deadline") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !recorded.closed {
		t.Fatal("socket was not closed after SetDeadline failure")
	}
}

func statusJSON(t *testing.T, name string, protocolNumber int32) []byte {
	t.Helper()
	value := statusResponse{}
	value.Version.Name = name
	value.Version.Protocol = protocolNumber
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
