package basic

import (
	"time"

	"github.com/imfusheng/go-mc/data/packetid"
	pk "github.com/imfusheng/go-mc/net/packet"
)

const keepAliveDuration = time.Second * 20

func (p *Player) resetKeepAliveDeadline() {
	newDeadline := time.Now().Add(keepAliveDuration)
	p.c.Conn.Socket.SetDeadline(newDeadline)
}

func (p *Player) handleKeepAlivePacket(packet pk.Packet) error {
	var KeepAliveID pk.Long
	if err := packet.Scan(&KeepAliveID); err != nil {
		return Error{err}
	}

	p.resetKeepAliveDeadline()

	// Response. The incoming packet buffer is borrowed from bot.Conn and is
	// returned to its pool as soon as this handler exits. Re-encode the value so
	// the asynchronous writer owns independent storage.
	err := p.c.Conn.WritePacket(keepAliveResponse(KeepAliveID))
	if err != nil {
		return Error{err}
	}
	return nil
}

func keepAliveResponse(id pk.Long) pk.Packet {
	return pk.Marshal(packetid.ServerboundKeepAlive, id)
}
