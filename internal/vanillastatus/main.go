// Command vanillastatus verifies interoperability with one exact Minecraft
// Java Edition release from the generated protocol catalogue. Legacy releases
// are verified through status; Netty releases must additionally enter Play.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/imfusheng/go-mc/bot"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

const (
	defaultOverallTimeout = 3 * time.Minute
	defaultAttemptTimeout = 5 * time.Second
	defaultRetryInterval  = time.Second
)

type config struct {
	address        string
	versionName    string
	overallTimeout time.Duration
	attemptTimeout time.Duration
	retryInterval  time.Duration
}

type statusResponse struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int32  `json:"protocol"`
	} `json:"version"`
}

type result struct {
	version     protocol.Version
	reported    statusResponse
	latency     time.Duration
	attempts    int
	enteredPlay bool
}

type pingFunc func(context.Context, string, bot.PingOptions) ([]byte, time.Duration, error)
type joinFunc func(context.Context, string, *protocol.Profile) error

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:], os.Stdout, bot.PingAndListContextWithOptions, enterPlay); err != nil {
		log.Printf("vanilla interoperability verification failed: %v", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, ping pingFunc, join joinFunc) error {
	flags := flag.NewFlagSet("vanillastatus", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg config
	flags.StringVar(&cfg.address, "address", "127.0.0.1:25565", "Minecraft server address")
	flags.StringVar(&cfg.versionName, "version", "", "exact stable Minecraft Java Edition release")
	flags.DurationVar(&cfg.overallTimeout, "timeout", defaultOverallTimeout, "overall retry deadline")
	flags.DurationVar(&cfg.attemptTimeout, "attempt-timeout", defaultAttemptTimeout, "deadline for one status/login attempt")
	flags.DurationVar(&cfg.retryInterval, "retry-interval", defaultRetryInterval, "delay between attempts")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if err := cfg.validate(); err != nil {
		return err
	}

	version, err := catalogueVersion(cfg.versionName)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.overallTimeout)
	defer cancel()

	verified, err := verify(ctx, cfg, version, ping, join)
	if err != nil {
		return err
	}
	mode := "status-only"
	if verified.enteredPlay {
		mode = "entered-play"
	}
	fmt.Fprintf(
		stdout,
		"verified Minecraft %s %s at %s: protocol=%d, reported-name=%q, transport=%s, status-latency=%s, attempts=%d\n",
		verified.version.Name,
		mode,
		cfg.address,
		verified.reported.Version.Protocol,
		verified.reported.Version.Name,
		verified.version.Transport,
		verified.latency.Round(time.Millisecond),
		verified.attempts,
	)
	return nil
}

func (c config) validate() error {
	if c.versionName == "" {
		return errors.New("-version is required")
	}
	if c.address == "" {
		return errors.New("-address must not be empty")
	}
	if c.overallTimeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	if c.attemptTimeout <= 0 {
		return errors.New("-attempt-timeout must be positive")
	}
	if c.retryInterval < 0 {
		return errors.New("-retry-interval must not be negative")
	}
	return nil
}

func catalogueVersion(name string) (protocol.Version, error) {
	profile, ok := protocol.ByName(name)
	if !ok {
		return protocol.Version{}, fmt.Errorf("Minecraft release %q is not in the protocol catalogue", name)
	}
	if profile.Capabilities().Status == protocol.Unsupported {
		return protocol.Version{}, protocol.UnsupportedCapabilityError{
			Version:    name,
			Capability: "server-list status",
		}
	}
	for _, version := range profile.Versions() {
		if version.Name == name {
			return version, nil
		}
	}
	return protocol.Version{}, fmt.Errorf("protocol catalogue invariant: profile for %q does not contain that release", name)
}

func verify(ctx context.Context, cfg config, version protocol.Version, ping pingFunc, join joinFunc) (result, error) {
	if ping == nil {
		return result{}, errors.New("nil status verifier")
	}
	profile, ok := protocol.ByName(version.Name)
	if !ok {
		return result{}, fmt.Errorf("protocol catalogue invariant: profile for %q disappeared", version.Name)
	}
	if version.Transport == protocol.TransportNetty && join == nil {
		return result{}, errors.New("nil enter-Play verifier")
	}

	var (
		attempts int
		lastErr  error
	)
	for {
		if err := ctx.Err(); err != nil {
			return result{}, verificationTimeout(version, cfg, attempts, err, lastErr)
		}

		attempts++
		attemptCtx, cancel := context.WithTimeout(ctx, cfg.attemptTimeout)
		data, latency, err := ping(attemptCtx, cfg.address, bot.PingOptions{Version: version})
		if err == nil {
			var status statusResponse
			if decodeErr := json.Unmarshal(data, &status); decodeErr != nil {
				cancel()
				return result{}, fmt.Errorf("attempt %d returned invalid status JSON: %w", attempts, decodeErr)
			}
			if err := validateStatus(status, version); err != nil {
				cancel()
				return result{}, fmt.Errorf("attempt %d returned an incompatible status: %w", attempts, err)
			}
			if version.Transport == protocol.TransportLegacy {
				cancel()
				return result{
					version:  version,
					reported: status,
					latency:  latency,
					attempts: attempts,
				}, nil
			}
			if joinErr := join(attemptCtx, cfg.address, profile); joinErr == nil {
				cancel()
				return result{
					version:     version,
					reported:    status,
					latency:     latency,
					attempts:    attempts,
					enteredPlay: true,
				}, nil
			} else {
				err = fmt.Errorf("status passed but enter Play failed: %w", joinErr)
			}
		}
		cancel()
		lastErr = fmt.Errorf("attempt %d: %w", attempts, err)

		timer := time.NewTimer(cfg.retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return result{}, verificationTimeout(version, cfg, attempts, ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

// deadlineDialer turns the attempt context deadline into a socket deadline.
// JoinServerWithOptions uses its context while dialing, but Minecraft login and
// configuration are synchronous reads and writes after DialMCContext returns;
// the socket deadline keeps those operations inside the same hard attempt
// budget too.
type deadlineDialer struct {
	base mcnet.MCDialer
}

func (d deadlineDialer) DialMCContext(ctx context.Context, address string) (*mcnet.Conn, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("vanilla verifier requires a connection deadline")
	}
	base := d.base
	if base == nil {
		base = &mcnet.DefaultDialer
	}
	conn, err := base.DialMCContext(ctx, address)
	if err != nil {
		return nil, err
	}
	if conn == nil || conn.Socket == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, errors.New("Minecraft dialer returned a nil socket")
	}
	if err := conn.Socket.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set Minecraft connection deadline: %w", err)
	}
	return conn, nil
}

func enterPlay(ctx context.Context, address string, profile *protocol.Profile) error {
	if profile == nil {
		return errors.New("nil protocol profile")
	}
	if profile.Key().Transport != protocol.TransportNetty {
		return fmt.Errorf("profile %s is not Netty", profile.Key())
	}

	client := bot.NewClient()
	client.Auth.Name = "GoMCCompat"
	// Interoperability automation must never consent to server-authored terms.
	// A nil handler makes the bot return ConsentRequiredError if a server sends
	// a Code of Conduct packet.
	client.CodeOfConduct = nil
	defer func() { _ = client.Close() }()

	if err := client.JoinServerWithOptions(address, bot.JoinOptions{
		Context:     ctx,
		MCDialer:    deadlineDialer{},
		Profile:     profile,
		NoPublicKey: true,
	}); err != nil {
		return fmt.Errorf("join Minecraft %s: %w", profile.Version().Name, err)
	}
	if client.Conn == nil {
		return errors.New("join returned without a Play connection")
	}

	var first pk.Packet
	if err := client.Conn.ReadPacket(&first); err != nil {
		return fmt.Errorf("read first Play packet: %w", err)
	}
	kind, err := protocol.RequirePacketKind(profile, protocol.StatePlay, protocol.Clientbound, first.ID)
	if err != nil {
		return fmt.Errorf("resolve first Play packet ID %#x: %w", first.ID, err)
	}
	const joinGame protocol.PacketKind = "login"
	if kind != joinGame {
		return fmt.Errorf("first Play packet kind is %q (ID %#x), want %q (Join Game)", kind, first.ID, joinGame)
	}
	return nil
}

var _ mcnet.MCDialer = deadlineDialer{}

func validateStatus(status statusResponse, expected protocol.Version) error {
	if status.Version.Protocol != expected.Protocol {
		return fmt.Errorf(
			"server reported protocol %d, want %d for Minecraft %s",
			status.Version.Protocol,
			expected.Protocol,
			expected.Name,
		)
	}
	if status.Version.Name != expected.Name {
		return fmt.Errorf(
			"server reported version name %q, want %q",
			status.Version.Name,
			expected.Name,
		)
	}
	return nil
}

func verificationTimeout(version protocol.Version, cfg config, attempts int, deadlineErr, lastErr error) error {
	capability := "status"
	if version.Transport == protocol.TransportNetty {
		capability = "status/enter-Play interoperability"
	}
	if lastErr == nil {
		return fmt.Errorf(
			"Minecraft %s %s at %s was not verified after %d attempts within %s: %w",
			version.Name,
			capability,
			cfg.address,
			attempts,
			cfg.overallTimeout,
			deadlineErr,
		)
	}
	return fmt.Errorf(
		"Minecraft %s %s at %s was not verified after %d attempts within %s: %w; last error: %v",
		version.Name,
		capability,
		cfg.address,
		attempts,
		cfg.overallTimeout,
		deadlineErr,
		lastErr,
	)
}
