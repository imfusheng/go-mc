package basic

import (
	pk "github.com/imfusheng/go-mc/net/packet"
)

func (p *Player) handlePingPacket(packet pk.Packet) error {
	response, err := playResponsePacket(p.c.Profile, playPongKind, packet.Data)
	if err != nil {
		return Error{err}
	}

	if err := p.c.Conn.WritePacket(response); err != nil {
		return Error{err}
	}
	return nil
}
