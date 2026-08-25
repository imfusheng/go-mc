// Package server provide a minecraft server framework.
// You can build the server you want by combining the various functional modules provided here.
// An example can be found in examples/frameworkServer.
//
// # This package is under rapid development, and any API may be subject to break changes
//
// A server is roughly divided into two parts: Gate and GamePlay
//
//	+------------------------------------------------------------------------------+
//	|                             Go-MC Server Framework                           |
//	|--------------------------------------+---------------------------------------|
//	|               Gate                   |                GamePlay               |
//	|--------------------+-----------------+---------------+-----------------------|
//	|    LoginHandler    |         ListPingHandler         |        Others..       |
//	|--------------------|------------+----+---------------|-----------------------+
//	| MojangLoginHandler |  PingInfo  |     PlayerList     |  [go-mc/server], etc. |
//	+--------------------+------------+--------------------+-----------------------+
//
// Gate, which is used to respond to the client login request, provide login verification,
// respond to the List Ping Request and providing the online players' information.
//
// Gameplay, which is used to handle all things after a player successfully logs in
// (that is, after the LoginSuccess package is sent),
// and is responsible for functions including player status, chunk management, keep alive, chat, etc.
//
// The implement of Gameplay is provided at [go-mc/server]. You can also write your version.
//
// [go-mc/server]: https://github.com/go-mc/server
package server

import (
	"errors"
	"log"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/net"
	"github.com/imfusheng/go-mc/protocol"
)

const (
	ProtocolName    = "1.21.1"
	ProtocolVersion = 767
)

type Server struct {
	*log.Logger
	ListPingHandler
	LoginHandler
	ConfigHandler
	GamePlay
}

func (s *Server) Listen(addr string) error {
	listener, err := net.ListenMC(addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.AcceptConn(&conn)
	}
}

func (s *Server) AcceptConn(conn *net.Conn) {
	if conn == nil || conn.Socket == nil {
		return
	}
	defer conn.Close()
	protocolNumber, intention, err := s.handshake(conn)
	if err != nil {
		return
	}

	switch intention {
	case 1: // list ping
		if s.ListPingHandler == nil {
			return
		}
		s.acceptListPing(conn, protocolNumber)
	case 2: // login
		profile, ok := protocol.ByProtocol(protocolNumber)
		if !ok || profile.Capabilities().Login == protocol.Unsupported {
			if s.Logger != nil {
				s.Logger.Printf("client %v requested unsupported protocol %d", conn.Socket.RemoteAddr(), protocolNumber)
			}
			_ = writeLoginDisconnect(conn, protocolNumber, chat.Text("Unsupported Minecraft protocol"))
			return
		}
		if profile.Capabilities().PlayCore == protocol.Unsupported {
			if s.Logger != nil {
				s.Logger.Printf("client %v play state for Minecraft %s is not supported", conn.Socket.RemoteAddr(), profile.Version().Name)
			}
			_ = writeLoginDisconnect(conn, protocolNumber, chat.Text("This Minecraft version's play state is not supported"))
			return
		}
		if profile.HasConfigurationState() && profile.Capabilities().Configuration == protocol.Unsupported {
			if s.Logger != nil {
				s.Logger.Printf("client %v configuration for Minecraft %s is not supported", conn.Socket.RemoteAddr(), profile.Version().Name)
			}
			_ = writeLoginDisconnect(conn, protocolNumber, chat.Text("This Minecraft version's configuration state is not supported"))
			return
		}
		if s.LoginHandler == nil || s.GamePlay == nil || (profile.HasConfigurationState() && s.ConfigHandler == nil) {
			if s.Logger != nil {
				s.Logger.Printf("client %v cannot log in: server handlers are incomplete", conn.Socket.RemoteAddr())
			}
			_ = writeLoginDisconnect(conn, protocolNumber, chat.Text("Server login handlers are not configured"))
			return
		}
		name, id, profilePubKey, properties, err := s.AcceptLogin(conn, protocolNumber)
		if err != nil {
			var loginErr LoginFailErr
			if errors.As(err, &loginErr) {
				_ = writeLoginDisconnect(conn, protocolNumber, loginErr.reason)
			}
			if s.Logger != nil {
				s.Logger.Printf("client %v login error: %v", conn.Socket.RemoteAddr(), err)
			}
			return
		}
		if profile.HasConfigurationState() {
			err = s.acceptConfigForProfile(conn, profile)
		}
		if err != nil {
			var configErr ConfigFailErr
			if errors.As(err, &configErr) {
				if disconnectErr := writeConfigDisconnect(conn, profile, configErr.reason); disconnectErr != nil && s.Logger != nil {
					s.Logger.Printf("client %v configuration disconnect encoding error: %v", conn.Socket.RemoteAddr(), disconnectErr)
				}
			}
			if s.Logger != nil {
				s.Logger.Printf("client %v config error: %v", conn.Socket.RemoteAddr(), err)
			}
			return
		}
		s.AcceptPlayer(name, id, profilePubKey, properties, protocolNumber, conn)
	}
}
