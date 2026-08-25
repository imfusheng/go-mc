package bot

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	mcnet "github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
)

// PingOptions selects the wire protocol and dialer used for a server-list ping.
// The zero value uses the package's current Netty protocol. A legacy Version
// must include its Major family because protocol numbers alone are ambiguous.
type PingOptions struct {
	Version  protocol.Version
	MCDialer mcnet.MCDialer
}

var defaultPingVersion = protocol.LatestRelease()

// PingAndList check server status and list online player.
// Returns a JSON data with server status, and the delay.
//
// For more information for JSON format, see https://wiki.vg/Server_List_Ping#Response
func PingAndList(addr string) ([]byte, time.Duration, error) {
	conn, err := mcnet.DialMC(addr)
	if err != nil {
		return nil, 0, LoginErr{"dial connection", err}
	}
	defer conn.Close()
	return pingAndList(context.Background(), addr, conn, defaultPingVersion)
}

// PingAndListWithOptions is PingAndList with an explicit protocol version and
// optional dialer.
func PingAndListWithOptions(addr string, options PingOptions) ([]byte, time.Duration, error) {
	return PingAndListContextWithOptions(context.Background(), addr, options)
}

// PingAndListTimeout is the version of PingAndList with max request time.
func PingAndListTimeout(addr string, timeout time.Duration) ([]byte, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return PingAndListContext(ctx, addr)
}

// PingAndListTimeoutWithOptions is PingAndListWithOptions with a maximum
// request duration.
func PingAndListTimeoutWithOptions(addr string, timeout time.Duration, options PingOptions) ([]byte, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return PingAndListContextWithOptions(ctx, addr, options)
}

func PingAndListContext(ctx context.Context, addr string) ([]byte, time.Duration, error) {
	return PingAndListContextWithOptions(ctx, addr, PingOptions{})
}

// PingAndListContextWithOptions is PingAndListWithOptions with cancellation
// and deadline support.
func PingAndListContextWithOptions(ctx context.Context, addr string, options PingOptions) ([]byte, time.Duration, error) {
	if options.Version == (protocol.Version{}) {
		options.Version = defaultPingVersion
	}
	if options.MCDialer == nil {
		options.MCDialer = &mcnet.DefaultDialer
	}
	if err := validatePingVersion(options.Version); err != nil {
		return nil, 0, err
	}

	conn, err := options.MCDialer.DialMCContext(ctx, addr)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()
	return pingAndList(ctx, addr, conn, options.Version)
}

func pingAndList(ctx context.Context, addr string, conn *mcnet.Conn, version protocol.Version) (data []byte, delay time.Duration, err error) {
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		if err := conn.Socket.SetDeadline(deadline); err != nil {
			return nil, 0, err
		}
		defer func() {
			// Reset deadline
			if err2 := conn.Socket.SetDeadline(time.Time{}); err2 != nil {
				if err == nil {
					err = err2
				}
				return
			}
			// Map error type
			if errors.Is(err, os.ErrDeadlineExceeded) {
				err = context.DeadlineExceeded
			}
		}()
	}

	host, port, err := splitPingAddress(addr)
	if err != nil {
		return nil, 0, err
	}

	switch version.Transport {
	case protocol.TransportNetty:
		return pingAndListNetty(conn, host, port, version)
	case protocol.TransportLegacy:
		return pingAndListLegacy(conn, host, port, version)
	default:
		return nil, 0, fmt.Errorf("bot: unsupported ping transport %s", version.Transport)
	}
}

func splitPingAddress(addr string) (host string, port uint16, err error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		var addrErr *net.AddrError
		const missingPort = "missing port in address"
		if errors.As(err, &addrErr) && addrErr.Err == missingPort {
			return addr, DefaultPort, nil
		} else {
			return "", 0, LoginErr{"split address", err}
		}
	}
	parsedPort, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", 0, LoginErr{"parse port", err}
	}
	return host, uint16(parsedPort), nil
}

func pingAndListNetty(conn *mcnet.Conn, host string, port uint16, version protocol.Version) ([]byte, time.Duration, error) {
	profile, knownProfile := protocol.ByKey(version.Key())
	packetID := func(state protocol.State, direction protocol.Direction, kind protocol.PacketKind, invariant int32) (int32, error) {
		if !knownProfile {
			return invariant, nil
		}
		return protocol.RequirePacketID(profile, state, direction, kind)
	}
	handshakeID, err := packetID(protocol.StateHandshake, protocol.Serverbound, protocol.PacketHandshakeSetProtocol, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: resolve handshake packet: %w", err)
	}
	err = conn.WritePacket(pk.Marshal(
		handshakeID,
		pk.VarInt(version.Protocol),
		pk.String(host),
		pk.UnsignedShort(port),
		pk.VarInt(1),
	))
	if err != nil {
		return nil, 0, fmt.Errorf("bot: send handshake packet: %w", err)
	}

	statusRequestID, err := packetID(protocol.StateStatus, protocol.Serverbound, protocol.PacketStatusRequest, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: resolve status request packet: %w", err)
	}
	err = conn.WritePacket(pk.Marshal(statusRequestID))
	if err != nil {
		return nil, 0, fmt.Errorf("bot: send status request: %w", err)
	}

	var p pk.Packet
	if err := conn.ReadPacket(&p); err != nil {
		return nil, 0, fmt.Errorf("bot: receive status response: %w", err)
	}
	responseKind, err := resolveStatusPacketKind(profile, knownProfile, p.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: receive status response: %w", err)
	}
	if responseKind != protocol.PacketStatusResponse {
		return nil, 0, fmt.Errorf(
			"bot: receive status response: packet ID %d resolved to %q, want %q",
			p.ID,
			responseKind,
			protocol.PacketStatusResponse,
		)
	}
	var s pk.String
	err = p.Scan(&s)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: decode status response: %w", err)
	}

	startTime := time.Now()
	nonce := pk.Long(startTime.Unix())
	pingID, err := packetID(protocol.StateStatus, protocol.Serverbound, protocol.PacketStatusPing, 1)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: resolve ping request packet: %w", err)
	}
	err = conn.WritePacket(pk.Marshal(pingID, nonce))
	if err != nil {
		return nil, 0, fmt.Errorf("bot: send ping request: %w", err)
	}

	if err = conn.ReadPacket(&p); err != nil {
		return nil, 0, fmt.Errorf("bot: receive pong response: %w", err)
	}
	pongKind, err := resolveStatusPacketKind(profile, knownProfile, p.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: receive pong response: %w", err)
	}
	if pongKind != protocol.PacketStatusPing {
		return nil, 0, fmt.Errorf(
			"bot: receive pong response: packet ID %d resolved to %q, want %q",
			p.ID,
			pongKind,
			protocol.PacketStatusPing,
		)
	}
	var t pk.Long
	err = p.Scan(&t)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: decode pong response: %w", err)
	}
	if t != nonce {
		return nil, 0, fmt.Errorf("bot: pong response nonce %d, want %d", t, nonce)
	}

	return []byte(s), time.Since(startTime), nil
}

func resolveStatusPacketKind(profile *protocol.Profile, knownProfile bool, id int32) (protocol.PacketKind, error) {
	if knownProfile {
		return protocol.RequirePacketKind(profile, protocol.StateStatus, protocol.Clientbound, id)
	}
	// Status packet IDs 0/1 are invariant across the Netty protocol family.
	// Keeping this fallback permits probing proxy-specific or future protocol
	// numbers that are not yet present in the release catalogue.
	switch id {
	case 0:
		return protocol.PacketStatusResponse, nil
	case 1:
		return protocol.PacketStatusPing, nil
	default:
		return "", &protocol.PacketMappingError{
			Version:   "unlisted Netty profile",
			State:     protocol.StateStatus,
			Direction: protocol.Clientbound,
			ID:        id,
			ByID:      true,
		}
	}
}

type legacyPingFamily uint8

const (
	legacyPingFE legacyPingFamily = iota
	legacyPingFE01
	legacyPingExtended
)

func validatePingVersion(version protocol.Version) error {
	switch version.Transport {
	case protocol.TransportNetty:
		return nil
	case protocol.TransportLegacy:
		_, err := legacyFamily(version)
		return err
	default:
		return fmt.Errorf("bot: unsupported ping transport %s", version.Transport)
	}
}

// legacyFamily deliberately recognizes only stable Java 1.0-1.6 marketing
// families. Snapshot and proxy-specific variants are rejected rather than
// guessed from a protocol number, which Mojang has reused across transports.
func legacyFamily(version protocol.Version) (legacyPingFamily, error) {
	switch version.Major {
	case "1.0", "1.1", "1.2", "1.3":
		return legacyPingFE, nil
	case "1.4", "1.5":
		return legacyPingFE01, nil
	case "1.6":
		if version.Protocol < 0 || version.Protocol > math.MaxUint8 {
			return 0, fmt.Errorf("bot: legacy 1.6 ping protocol %d does not fit one byte", version.Protocol)
		}
		return legacyPingExtended, nil
	default:
		name := version.Name
		if name == "" {
			name = version.Major
		}
		return 0, protocol.UnsupportedCapabilityError{
			Version:    name,
			Capability: "legacy server-list ping family",
		}
	}
}

func pingAndListLegacy(conn *mcnet.Conn, host string, port uint16, version protocol.Version) ([]byte, time.Duration, error) {
	family, err := legacyFamily(version)
	if err != nil {
		return nil, 0, err
	}
	request, err := legacyPingRequest(family, host, port, version.Protocol)
	if err != nil {
		return nil, 0, err
	}

	startTime := time.Now()
	if err := writeAll(conn.Writer, request); err != nil {
		return nil, 0, fmt.Errorf("bot: send legacy status request: %w", err)
	}
	response, err := readLegacyString(conn.Reader)
	if err != nil {
		return nil, 0, fmt.Errorf("bot: receive legacy status response: %w", err)
	}
	delay := time.Since(startTime)

	data, err := normalizeLegacyStatus(response, family, version)
	if err != nil {
		return nil, 0, err
	}
	return data, delay, nil
}

func legacyPingRequest(family legacyPingFamily, host string, port uint16, protocolNumber int32) ([]byte, error) {
	switch family {
	case legacyPingFE:
		return []byte{0xFE}, nil
	case legacyPingFE01:
		return []byte{0xFE, 0x01}, nil
	case legacyPingExtended:
		if protocolNumber < 0 || protocolNumber > math.MaxUint8 {
			return nil, fmt.Errorf("bot: legacy ping protocol %d does not fit one byte", protocolNumber)
		}

		var payload bytes.Buffer
		payload.WriteByte(byte(protocolNumber))
		if err := writeLegacyString(&payload, host); err != nil {
			return nil, err
		}
		if err := binary.Write(&payload, binary.BigEndian, uint32(port)); err != nil {
			return nil, err
		}
		if payload.Len() > math.MaxInt16 {
			return nil, fmt.Errorf("bot: legacy ping payload is %d bytes, maximum is %d", payload.Len(), math.MaxInt16)
		}

		var request bytes.Buffer
		request.Write([]byte{0xFE, 0x01, 0xFA})
		if err := writeLegacyString(&request, "MC|PingHost"); err != nil {
			return nil, err
		}
		if err := binary.Write(&request, binary.BigEndian, uint16(payload.Len())); err != nil {
			return nil, err
		}
		request.Write(payload.Bytes())
		return request.Bytes(), nil
	default:
		return nil, errors.New("bot: unknown legacy ping family")
	}
}

func writeLegacyString(w io.Writer, value string) error {
	units := utf16.Encode([]rune(value))
	if len(units) > math.MaxInt16 {
		return fmt.Errorf("bot: legacy string has %d UTF-16 code units, maximum is %d", len(units), math.MaxInt16)
	}

	var encoded bytes.Buffer
	if err := binary.Write(&encoded, binary.BigEndian, uint16(len(units))); err != nil {
		return err
	}
	for _, unit := range units {
		if err := binary.Write(&encoded, binary.BigEndian, unit); err != nil {
			return err
		}
	}
	return writeAll(w, encoded.Bytes())
}

func readLegacyString(r io.Reader) (string, error) {
	var packetID [1]byte
	if _, err := io.ReadFull(r, packetID[:]); err != nil {
		return "", err
	}
	if packetID[0] != 0xFF {
		return "", fmt.Errorf("legacy status packet ID %#02x, want 0xff", packetID[0])
	}

	var lengthBytes [2]byte
	if _, err := io.ReadFull(r, lengthBytes[:]); err != nil {
		return "", err
	}
	length := int(binary.BigEndian.Uint16(lengthBytes[:]))
	const maxLegacyStatusCodeUnits = 32767
	if length > maxLegacyStatusCodeUnits {
		return "", fmt.Errorf("legacy status string has %d UTF-16 code units, maximum is %d", length, maxLegacyStatusCodeUnits)
	}

	raw := make([]byte, length*2)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", err
	}
	units := make([]uint16, length)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(raw[i*2:])
	}
	return string(utf16.Decode(units)), nil
}

type legacyStatusJSON struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int32  `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description struct {
		Text string `json:"text"`
	} `json:"description"`
}

func normalizeLegacyStatus(response string, family legacyPingFamily, requested protocol.Version) ([]byte, error) {
	var status legacyStatusJSON
	if family == legacyPingFE {
		last := strings.LastIndex(response, "§")
		if last < 0 {
			return nil, errors.New("bot: malformed legacy status response: missing maximum player delimiter")
		}
		secondLast := strings.LastIndex(response[:last], "§")
		if secondLast < 0 {
			return nil, errors.New("bot: malformed legacy status response: missing online player delimiter")
		}

		status.Description.Text = response[:secondLast]
		// The original FE reply carries no version fields. Preserve that
		// limitation explicitly by reporting the profile the caller requested.
		status.Version.Name = requested.Name
		if status.Version.Name == "" {
			status.Version.Name = requested.Major
		}
		status.Version.Protocol = requested.Protocol
		var err error
		status.Players.Online, err = parseLegacyCount(response[secondLast+len("§"):last], "online")
		if err != nil {
			return nil, err
		}
		status.Players.Max, err = parseLegacyCount(response[last+len("§"):], "maximum")
		if err != nil {
			return nil, err
		}
	} else {
		parts := strings.Split(response, "\x00")
		if len(parts) != 6 || parts[0] != "§1" {
			return nil, errors.New("bot: malformed extended legacy status response")
		}
		parsedProtocol, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("bot: parse legacy status protocol: %w", err)
		}
		status.Version.Protocol = int32(parsedProtocol)
		status.Version.Name = parts[2]
		status.Description.Text = parts[3]
		status.Players.Online, err = parseLegacyCount(parts[4], "online")
		if err != nil {
			return nil, err
		}
		status.Players.Max, err = parseLegacyCount(parts[5], "maximum")
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(status)
}

func parseLegacyCount(value, name string) (int, error) {
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("bot: parse legacy %s player count: %w", name, err)
	}
	return count, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
