package server

import (
	"fmt"

	"github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

func (s *Server) handshake(conn *net.Conn) (protocolNumber int32, intention int32, err error) {
	var (
		Protocol, Intention pk.VarInt
		ServerAddress       pk.String        // ignored
		ServerPort          pk.UnsignedShort // ignored
	)
	// receive handshake packet
	var p pk.Packet
	err = conn.ReadPacket(&p)
	if err != nil {
		return 0, 0, err
	}
	err = p.Scan(&Protocol, &ServerAddress, &ServerPort, &Intention)
	if err != nil {
		return int32(Protocol), int32(Intention), err
	}

	protocolNumber = int32(Protocol)
	profile, knownProfile := protocol.ByProtocol(protocolNumber)
	if !knownProfile {
		// Packet ID 0 is invariant for the Netty handshake and must remain
		// available to status probes for future and proxy-specific protocols.
		if p.ID != 0 {
			return protocolNumber, int32(Intention), &protocol.PacketMappingError{
				Version:   fmt.Sprintf("protocol %d", protocolNumber),
				State:     protocol.StateHandshake,
				Direction: protocol.Serverbound,
				ID:        p.ID,
				ByID:      true,
			}
		}
		return protocolNumber, int32(Intention), nil
	}

	kind, resolveErr := protocol.RequirePacketKind(profile, protocol.StateHandshake, protocol.Serverbound, p.ID)
	if resolveErr != nil {
		return protocolNumber, int32(Intention), resolveErr
	}
	if kind != protocol.PacketHandshakeSetProtocol {
		expected, resolveErr := protocol.RequirePacketID(profile, protocol.StateHandshake, protocol.Serverbound, protocol.PacketHandshakeSetProtocol)
		if resolveErr != nil {
			return protocolNumber, int32(Intention), resolveErr
		}
		return protocolNumber, int32(Intention), wrongPacketErr{expect: expected, get: p.ID}
	}
	return protocolNumber, int32(Intention), nil
}
