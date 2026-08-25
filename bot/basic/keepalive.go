package basic

import (
	"time"

	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

const keepAliveDuration = time.Second * 20

const (
	playKeepAliveKind protocol.PacketKind = "keep_alive"
	playPingKind      protocol.PacketKind = "ping"
	playPongKind      protocol.PacketKind = "pong"
)

func (p *Player) resetKeepAliveDeadline() {
	newDeadline := time.Now().Add(keepAliveDuration)
	p.c.Conn.Socket.SetDeadline(newDeadline)
}

func (p *Player) handleKeepAlivePacket(packet pk.Packet) error {
	response, err := playResponsePacket(p.c.Profile, playKeepAliveKind, packet.Data)
	if err != nil {
		return Error{err}
	}

	p.resetKeepAliveDeadline()

	if err := p.c.Conn.WritePacket(response); err != nil {
		return Error{err}
	}
	return nil
}

func playResponsePacket(profile *protocol.Profile, kind protocol.PacketKind, payload []byte) (pk.Packet, error) {
	id, err := protocol.RequirePacketID(profile, protocol.StatePlay, protocol.Serverbound, kind)
	if err != nil {
		return pk.Packet{}, err
	}
	return pk.Packet{ID: id, Data: append([]byte(nil), payload...)}, nil
}
