package server

import (
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

// ListPingHandler collect server running status info
// which is used to handle client ping and list progress.
type ListPingHandler interface {
	// Name of the server.
	// Vanilla server uses its version name, like "1.19.3".
	Name() string

	// The Protocol number.
	// Usually implemented as returning the protocol number the server currently used.
	// If the server supports multiple protocols, should be implemented as returning clientProtocol
	Protocol(clientProtocol int32) int

	MaxPlayer() int

	OnlinePlayer() int

	// PlayerSamples is a short list of players in the server.
	// Vanilla server returns up to 10 players in the list.
	PlayerSamples() []PlayerSample

	// Description also called MOTD, Message Of The Day.
	Description() *chat.Message

	// FavIcon should be a PNG image that is Base64 encoded
	// (without newlines: \n, new lines no longer work since 1.13)
	// and prepended with "data:image/png;base64,".
	//
	// This method can return empty string if no icon is set.
	FavIcon() string
}

type PlayerSample struct {
	Name string    `json:"name"`
	ID   uuid.UUID `json:"id"`
}

func (s *Server) acceptListPing(conn *net.Conn, clientProtocol int32) {
	profile, knownProfile := protocol.ByProtocol(clientProtocol)
	var p pk.Packet
	for i := 0; i < 2; i++ { // Ping or List. Only allow check twice
		err := conn.ReadPacket(&p)
		if err != nil {
			return
		}

		kind, resolveErr := resolveServerboundStatusKind(profile, knownProfile, p.ID)
		if resolveErr != nil {
			return
		}

		switch kind {
		case protocol.PacketStatusRequest:
			var resp []byte
			resp, err = s.listResp(clientProtocol)
			if err != nil {
				break
			}
			var responseID int32
			responseID, err = resolveClientboundStatusID(profile, knownProfile, protocol.PacketStatusResponse, 0)
			if err == nil {
				err = conn.WritePacket(pk.Marshal(responseID, pk.String(resp)))
			}
		case protocol.PacketStatusPing:
			var pongID int32
			pongID, err = resolveClientboundStatusID(profile, knownProfile, protocol.PacketStatusPing, 1)
			if err == nil {
				err = conn.WritePacket(pk.Packet{ID: pongID, Data: p.Data})
			}
		}
		if err != nil {
			return
		}
	}
}

func resolveServerboundStatusKind(profile *protocol.Profile, knownProfile bool, id int32) (protocol.PacketKind, error) {
	if knownProfile {
		return protocol.RequirePacketKind(profile, protocol.StateStatus, protocol.Serverbound, id)
	}
	switch id {
	case 0:
		return protocol.PacketStatusRequest, nil
	case 1:
		return protocol.PacketStatusPing, nil
	default:
		return "", &protocol.PacketMappingError{
			Version:   "unlisted Netty profile",
			State:     protocol.StateStatus,
			Direction: protocol.Serverbound,
			ID:        id,
			ByID:      true,
		}
	}
}

func resolveClientboundStatusID(profile *protocol.Profile, knownProfile bool, kind protocol.PacketKind, invariant int32) (int32, error) {
	if knownProfile {
		return protocol.RequirePacketID(profile, protocol.StateStatus, protocol.Clientbound, kind)
	}
	return invariant, nil
}

func (s *Server) listResp(clientProtocol int32) ([]byte, error) {
	type statusPlayerSample struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	var list struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    int                  `json:"max"`
			Online int                  `json:"online"`
			Sample []statusPlayerSample `json:"sample"`
		} `json:"players"`
		Description *chat.Message `json:"description"`
		FavIcon     string        `json:"favicon,omitempty"`
	}

	list.Version.Name = s.Name()
	list.Version.Protocol = s.Protocol(clientProtocol)
	list.Players.Max = s.MaxPlayer()
	list.Players.Online = s.OnlinePlayer()
	for _, sample := range s.PlayerSamples() {
		wireID := sample.ID.String()
		// Protocol 4 used 32 hexadecimal digits in both Login Success and
		// status sample UUIDs. Protocol 5 introduced canonical dashes.
		if clientProtocol == 4 {
			wireID = strings.ReplaceAll(wireID, "-", "")
		}
		list.Players.Sample = append(list.Players.Sample, statusPlayerSample{
			Name: sample.Name,
			ID:   wireID,
		})
	}
	list.Description = s.Description()
	list.FavIcon = s.FavIcon()

	return json.Marshal(list)
}

// PingInfo implement ListPingHandler.
type PingInfo struct {
	name        string
	protocol    int
	description chat.Message
	favicon     string
}

// NewPingInfo crate a new PingInfo, the icon can be nil.
// Panic if icon's size is not 64x64.
func NewPingInfo(name string, protocol int, motd chat.Message, icon image.Image) (p *PingInfo) {
	var favIcon string
	if icon != nil {
		if !icon.Bounds().Size().Eq(image.Point{X: 64, Y: 64}) {
			panic("icon size is not 64x64")
		}
		// Encode icon into string "data:image/png;base64,......" format
		var sb strings.Builder
		sb.WriteString("data:image/png;base64,")
		w := base64.NewEncoder(base64.StdEncoding, &sb)
		if err := png.Encode(w, icon); err != nil {
			panic(err)
		}
		if err := w.Close(); err != nil {
			panic(err)
		}
		favIcon = sb.String()
	}
	p = &PingInfo{
		name:        name,
		protocol:    protocol,
		description: motd,
		favicon:     favIcon,
	}
	return
}

func (p *PingInfo) Name() string {
	return p.name
}

func (p *PingInfo) Protocol(int32) int {
	return p.protocol
}

func (p *PingInfo) FavIcon() string {
	return p.favicon
}

func (p *PingInfo) Description() *chat.Message {
	return &p.description
}
