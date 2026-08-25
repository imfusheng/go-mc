package basic

import (
	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
)

func (p *Player) handlePingPacket(packet pk.Packet) error {
	var pingID pk.Int
	if err := packet.Scan(&pingID); err != nil {
		return Error{err}
	}

	// Response. Do not enqueue packet.Data: receive buffers are returned to a
	// pool immediately after the handler exits, while writes are asynchronous.
	err := p.c.Conn.WritePacket(pingResponse(pingID))
	if err != nil {
		return Error{err}
	}
	return nil
}

func pingResponse(id pk.Int) pk.Packet {
	return pk.Marshal(packetid.ServerboundPong, id)
}
