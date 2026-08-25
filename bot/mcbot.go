// Package bot implements a simple Minecraft client that can join a server
// or just ping it for getting information.
//
// Runnable example could be found at examples/ .
package bot

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/nbt"
	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/net/queue"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

// ProtocolVersion is the default Minecraft protocol number.
//
// Deprecated: select a protocol with JoinOptions.Profile. This constant is
// retained for source compatibility and represents Minecraft 1.21/1.21.1.
const (
	ProtocolVersion          = 767
	DefaultPort              = mcnet.DefaultPort
	defaultReadQueuePackets  = 256
	defaultReadQueueBytes    = 32 << 20
	defaultWriteQueuePackets = 256
	defaultWriteQueueBytes   = 8 << 20
)

type JoinOptions struct {
	MCDialer mcnet.MCDialer
	Context  context.Context

	// Profile selects the Minecraft wire protocol. Nil uses Minecraft 1.21.1.
	Profile *protocol.Profile

	// Indicate not to fetch and sending player's PubKey
	NoPublicKey bool

	// Specify the player PubKey to use.
	// If nil, it will be obtained from Mojang when joining
	KeyPair *user.KeyPairResp

	// QueueRead and QueueWrite transfer queue ownership to the Client. Custom
	// implementations must permit Close concurrently with Push/Pull and must
	// unblock pending operations when closed. Nil uses the bounded defaults.
	QueueRead  queue.Queue[pk.Packet]
	QueueWrite queue.Queue[pk.Packet]
}

// JoinServer connect a Minecraft server for playing the game.
// Using roughly the same way to parse address as minecraft.
func (c *Client) JoinServer(addr string) (err error) {
	return c.JoinServerWithOptions(addr, JoinOptions{})
}

// JoinServerWithDialer is similar to JoinServer but using a net.Dialer.
func (c *Client) JoinServerWithDialer(dialer *net.Dialer, addr string) (err error) {
	return c.JoinServerWithOptions(addr, JoinOptions{
		MCDialer: (*mcnet.Dialer)(dialer),
	})
}

func (c *Client) JoinServerWithOptions(addr string, options JoinOptions) (err error) {
	if c == nil {
		return LoginErr{"initialize client", errors.New("nil client")}
	}
	if options.MCDialer == nil {
		options.MCDialer = &mcnet.DefaultDialer
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.QueueRead == nil {
		options.QueueRead = newDefaultPacketQueue(defaultReadQueuePackets, defaultReadQueueBytes)
	}
	if options.QueueWrite == nil {
		options.QueueWrite = newDefaultPacketQueue(defaultWriteQueuePackets, defaultWriteQueueBytes)
	}
	if options.Profile == nil {
		options.Profile = protocol.MustByName("1.21.1")
	}
	return c.join(addr, options)
}

func newDefaultPacketQueue(maxPackets, maxBytes int) queue.Queue[pk.Packet] {
	return queue.NewBoundedQueue(maxPackets, maxBytes, func(p pk.Packet) int {
		// A short payload can still retain a much larger pooled backing array.
		// Charge capacity rather than length so the byte budget describes the
		// memory kept alive by queued packets. Ten bytes conservatively cover
		// the packet ID and outer frame-length VarInts.
		return cap(p.Data) + 10
	})
}

func (c *Client) join(addr string, options JoinOptions) error {
	profile := options.Profile
	if profile == nil {
		return LoginErr{"select protocol", errors.New("nil protocol profile")}
	}
	if profile.Key().Transport != protocol.TransportNetty {
		return LoginErr{"select protocol", protocol.UnsupportedCapabilityError{
			Version: profile.Version().Name, Capability: "Netty login",
		}}
	}
	if profile.Capabilities().PlayCore == protocol.Unsupported {
		return LoginErr{"select protocol", protocol.UnsupportedCapabilityError{
			Version: profile.Version().Name, Capability: "complete play session",
		}}
	}
	// Reject profiles whose post-login state is not implemented before opening
	// a socket. Otherwise the peer observes a successful login acknowledgement
	// followed by an unexplained disconnect in Configuration.
	if profile.HasConfigurationState() && profile.Capabilities().Configuration == protocol.Unsupported {
		return LoginErr{"select protocol", protocol.UnsupportedCapabilityError{
			Version: profile.Version().Name, Capability: "configuration state",
		}}
	}
	if c.Cookies == nil {
		c.Cookies = make(map[string][]byte)
	}
	if c.UnknownRegistries == nil {
		c.UnknownRegistries = make(map[string]*registry.Registry[nbt.RawMessage])
	}
	if c.CustomReportDetails == nil {
		c.CustomReportDetails = make(map[string]string)
	}
	if c.LoginPlugin == nil {
		c.LoginPlugin = make(map[string]CustomPayloadHandler)
	}
	if c.ConfigHandler == nil {
		c.ConfigHandler = NewDefaultConfigHandler()
	}

	// Split Host and Port. The DialMCContext will do this once,
	// but we need the result for sending handshake packet here.
	host, portStr, err := net.SplitHostPort(addr)
	var port uint64
	if err != nil {
		var addrErr *net.AddrError
		const missingPort = "missing port in address"
		if errors.As(err, &addrErr) && addrErr.Err == missingPort {
			host = addr
			port = 25565
		} else {
			return LoginErr{"split address", err}
		}
	} else {
		port, err = strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return LoginErr{"parse port", err}
		}
	}

	// Dial connection
	conn, err := options.MCDialer.DialMCContext(options.Context, addr)
	if err != nil {
		return LoginErr{"connect server", err}
	}
	stopContextWatch, err := watchJoinContext(conn.Socket, options.Context)
	if err != nil {
		_ = conn.Close()
		return LoginErr{"bind join context", err}
	}
	defer func() { _ = stopContextWatch() }()
	joined := false
	defer func() {
		if !joined {
			_ = conn.Close()
		}
	}()

	// Handshake
	handshakeID, err := protocol.RequirePacketID(profile, protocol.StateHandshake, protocol.Serverbound, protocol.PacketHandshakeSetProtocol)
	if err != nil {
		return LoginErr{"handshake", err}
	}
	err = conn.WritePacket(pk.Marshal(
		handshakeID,
		pk.VarInt(profile.Key().Protocol), // Protocol version
		pk.String(host),                   // Host
		pk.UnsignedShort(port),            // Port
		pk.VarInt(2),
	))
	if err != nil {
		return LoginErr{"handshake", err}
	}

	// Login Start
	if err := c.joinLogin(conn, profile, options); err != nil {
		if contextErr := options.Context.Err(); contextErr != nil {
			return LoginErr{"join context", contextErr}
		}
		return err
	}

	if profile.HasConfigurationState() {
		if err := c.joinConfiguration(conn, profile); err != nil {
			if contextErr := options.Context.Err(); contextErr != nil {
				return LoginErr{"join context", contextErr}
			}
			return err
		}
	}
	if err := stopContextWatch(); err != nil {
		return LoginErr{"clear join deadline", err}
	}
	c.Profile = profile
	c.Conn = warpConn(conn, options.QueueRead, options.QueueWrite)
	joined = true
	return nil
}

// watchJoinContext applies cancellation and deadlines to the synchronous
// Login/Configuration exchange. The returned function stops the watcher and
// clears the socket deadline before the established Play connection is handed
// to the asynchronous client.
func watchJoinContext(socket net.Conn, ctx context.Context) (func() error, error) {
	if socket == nil {
		return nil, errors.New("nil socket")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := socket.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if ctx.Done() == nil {
		return func() error { return socket.SetDeadline(time.Time{}) }, nil
	}

	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = socket.SetDeadline(time.Now())
		case <-stop:
		}
	}()

	var once sync.Once
	var stopErr error
	return func() error {
		once.Do(func() {
			close(stop)
			<-stopped
			stopErr = socket.SetDeadline(time.Time{})
		})
		return stopErr
	}, nil
}

type DisconnectErr chat.Message

func (d DisconnectErr) Error() string {
	return "disconnect because: " + chat.Message(d).String()
}
