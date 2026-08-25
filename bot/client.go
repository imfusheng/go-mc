package bot

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/data/packetid"
	"github.com/imfusheng/go-mc/nbt"
	"github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/net/queue"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
	"github.com/imfusheng/go-mc/yggdrasil/user"
)

// Client is used to access Minecraft server
type Client struct {
	Conn *Conn
	Auth Auth

	// Profile is the wire protocol selected for the active connection. It is
	// set by JoinServerWithOptions. New clients default to Minecraft 1.21.1.
	Profile *protocol.Profile

	// These are filled when login process
	Name       string
	UUID       uuid.UUID
	Properties []user.Property
	Registries registry.Registries
	// UnknownRegistries preserves forward-compatible configuration registries
	// that this build does not yet model with a concrete Go type.
	UnknownRegistries map[string]*registry.Registry[nbt.RawMessage]
	Cookies           map[string][]byte

	// Ingame packet handlers
	Events Events

	// Login plugins
	LoginPlugin map[string]CustomPayloadHandler

	// Configuration handler
	ConfigHandler
	// ConfigurationSettings controls the Client Information packet sent on
	// entry to the Configuration state. Nil uses
	// DefaultConfigurationSettings.
	ConfigurationSettings *ConfigurationSettings
	// CodeOfConduct is invoked when a server presents a code of conduct.
	// The client sends acceptance only when this callback is non-nil and
	// returns true.
	CodeOfConduct CodeOfConductHandler

	CustomReportDetails map[string]string
}

// CustomPayloadHandler is a function handling custom payload
type CustomPayloadHandler func(data []byte) ([]byte, error)

func (c *Client) Close() error {
	if c == nil || c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

// NewClient init and return a new Client.
//
// A new Client has default name "Steve" and zero UUID.
// It is usable for an offline-mode game.
//
// For online-mode, you need login your Mojang account
// and load your Name, UUID and AccessToken to client.
func NewClient() *Client {
	return &Client{
		Auth:              Auth{Name: "Steve"},
		Profile:           protocol.MustByName("1.21.1"),
		Registries:        registry.NewNetworkCodec(),
		UnknownRegistries: make(map[string]*registry.Registry[nbt.RawMessage]),
		Cookies:           make(map[string][]byte),
		Events:            Events{handlers: make([][]PacketHandler, packetid.ClientboundPacketIDGuard)},
		LoginPlugin:       make(map[string]CustomPayloadHandler),
		ConfigHandler:     NewDefaultConfigHandler(),
		ConfigurationSettings: func() *ConfigurationSettings {
			settings := DefaultConfigurationSettings
			return &settings
		}(),
		CustomReportDetails: make(map[string]string),
	}
}

// Conn is a concurrently-safe warpper of net.Conn with packet queue.
// Note that not all methods are concurrently-safe.
type Conn struct {
	*net.Conn
	send, recv queue.Queue[pk.Packet]
	pool       sync.Pool // pool of recv packet data
	// releasePacketHook observes receive-buffer releases in tests without
	// relying on sync.Pool retaining values across garbage collection.
	releasePacketHook func([]byte)
	rerrMu            sync.RWMutex
	rerr              error
	closeOnce         sync.Once
	closeErr          error
}

func warpConn(c *net.Conn, qr, qw queue.Queue[pk.Packet]) *Conn {
	wc := Conn{
		Conn: c,
		send: qw,
		recv: qr,
		pool: sync.Pool{New: func() any { return []byte{} }},
		rerr: nil,
	}
	go func() {
		for {
			// take a buffer from pool, after the packet is handled we put it back
			p := pk.Packet{Data: wc.pool.Get().([]byte)}
			if err := c.ReadPacket(&p); err != nil {
				wc.pool.Put(p.Data)
				wc.setReadError(err)
				break
			}
			if ok := pushPacket(wc.recv, p); !ok {
				wc.pool.Put(p.Data)
				wc.setReadError(errors.New("receive queue is unavailable or full"))
				break
			}
		}
		_ = wc.Close()
		_ = closePacketQueue(wc.recv)
	}()
	go func() {
		for {
			p, ok := wc.send.Pull()
			if !ok {
				break
			}
			if err := c.WritePacket(p); err != nil {
				wc.setReadError(fmt.Errorf("write packet: %w", err))
				_ = wc.Close()
				break
			}
		}
	}()

	return &wc
}

func (c *Conn) ReadPacket(p *pk.Packet) error {
	if c == nil || c.recv == nil {
		return errors.New("bot: client is not connected")
	}
	packet, ok := c.recv.Pull()
	if !ok {
		c.rerrMu.RLock()
		err := c.rerr
		c.rerrMu.RUnlock()
		if err == nil {
			return io.EOF
		}
		return err
	}
	*p = packet
	return nil
}

// WritePacket queues p for asynchronous transmission. After a successful
// call, callers must treat p.Data as immutable and must not reuse its backing
// storage. In particular, re-encode or clone Data received by an event handler
// because handler input is borrowed from the receive-buffer pool.
func (c *Conn) WritePacket(p pk.Packet) error {
	if c == nil || c.send == nil {
		return errors.New("bot: client is not connected")
	}
	ok := pushPacket(c.send, p)
	if !ok {
		return errors.New("send queue is unavailable or full")
	}
	return nil
}

func (c *Conn) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		sendQueueErr := closePacketQueue(c.send)
		recvQueueErr := closePacketQueue(c.recv)
		var connErr error
		if c.Conn != nil {
			connErr = c.Conn.Close()
		}
		c.closeErr = errors.Join(sendQueueErr, recvQueueErr, connErr)
	})
	return c.closeErr
}

func (c *Conn) setReadError(err error) {
	c.rerrMu.Lock()
	if c.rerr == nil {
		c.rerr = err
	}
	c.rerrMu.Unlock()
}

// Queue implementations are supplied by callers and some (notably channel
// queues) panic when pushed to or closed after an owner has already closed
// them. A network API reports that state as an error instead of propagating a
// queue implementation panic.
func pushPacket(q queue.Queue[pk.Packet], p pk.Packet) (ok bool) {
	if q == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return q.Push(p)
}

func closePacketQueue(q queue.Queue[pk.Packet]) (err error) {
	if q == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("close packet queue: %v", recovered)
		}
	}()
	q.Close()
	return nil
}

// Position is a 3D vector.
type Position struct {
	X, Y, Z int
}
