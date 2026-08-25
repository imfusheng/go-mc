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
	ProtocolVersion = 767
	DefaultPort     = mcnet.DefaultPort
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
		options.QueueRead = queue.NewLinkedQueue[pk.Packet]()
	}
	if options.QueueWrite == nil {
		options.QueueWrite = queue.NewLinkedQueue[pk.Packet]()
	}
	if options.Profile == nil {
		options.Profile = protocol.MustByName("1.21.1")
	}
	return c.join(addr, options)
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
		return err
	}

	if profile.HasConfigurationState() {
		if err := c.joinConfiguration(conn, profile); err != nil {
			return err
		}
	}
	c.Profile = profile
	c.Conn = warpConn(conn, options.QueueRead, options.QueueWrite)
	joined = true
	return nil
}

type DisconnectErr chat.Message

func (d DisconnectErr) Error() string {
	return "disconnect because: " + chat.Message(d).String()
}
