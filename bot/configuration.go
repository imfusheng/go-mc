package bot

import (
	"bytes"
	"fmt"
	"io"

	"github.com/imfusheng/go-mc/chat"
	"github.com/imfusheng/go-mc/nbt"
	"github.com/imfusheng/go-mc/net"
	pk "github.com/imfusheng/go-mc/net/packet"
	"github.com/imfusheng/go-mc/protocol"
	"github.com/imfusheng/go-mc/registry"
)

type ConfigHandler interface {
	EnableFeature(features []pk.Identifier)

	PushResourcePack(res ResourcePack)
	PopResourcePack(id pk.UUID)
	PopAllResourcePack()

	SelectDataPacks(packs []DataPack) []DataPack
}

type ResourcePack struct {
	ID            pk.UUID
	URL           string
	Hash          string
	Forced        bool
	PromptMessage *chat.Message // Optional
}

// ConfigurationSettings is encoded as the Configuration-state Client
// Information packet introduced in Minecraft 1.20.2. ParticleStatus is sent
// only by protocol 768 and later.
type ConfigurationSettings struct {
	Locale              string
	ViewDistance        int8
	ChatMode            int32
	ChatColors          bool
	DisplayedSkinParts  uint8
	MainHand            int32
	EnableTextFiltering bool
	AllowServerListings bool
	ParticleStatus      int32
}

// DefaultConfigurationSettings is a conservative vanilla-like client
// configuration. Copy it before changing fields.
var DefaultConfigurationSettings = ConfigurationSettings{
	Locale:              "en_us",
	ViewDistance:        8,
	ChatMode:            0,
	ChatColors:          true,
	DisplayedSkinParts:  0x7f,
	MainHand:            1,
	EnableTextFiltering: false,
	AllowServerListings: true,
	ParticleStatus:      0,
}

// CodeOfConductHandler decides whether the client explicitly accepts text
// presented by the server. Returning false leaves the connection without
// sending an acceptance packet.
type CodeOfConductHandler func(text string) bool

// ConsentRequiredError reports a code of conduct that requires an explicit
// application decision. It is returned when CodeOfConduct is nil or rejects
// the text.
type ConsentRequiredError struct {
	Text string
}

func (e *ConsentRequiredError) Error() string {
	return "bot: server code of conduct requires explicit consent"
}

// TransferError reports that the server requested a new connection. The
// current connection cannot continue into Play and callers may reconnect to
// Host:Port after applying their own trust policy.
type TransferError struct {
	Host string
	Port int32
}

func (e *TransferError) Error() string {
	return fmt.Sprintf("bot: server requested transfer to %s:%d", e.Host, e.Port)
}

type ConfigErr struct {
	Stage string
	Err   error
}

func (l ConfigErr) Error() string {
	return "bot: configuration error: [" + l.Stage + "] " + l.Err.Error()
}

func (l ConfigErr) Unwrap() error {
	return l.Err
}

func (c *Client) joinConfiguration(conn *net.Conn, profile *protocol.Profile) (err error) {
	if c == nil {
		return ConfigErr{"initialize", fmt.Errorf("nil client")}
	}
	if conn == nil {
		return ConfigErr{"initialize", fmt.Errorf("nil connection")}
	}
	if profile == nil {
		_, mappingErr := protocol.RequirePacketID(nil, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
		return ConfigErr{"initialize", mappingErr}
	}
	if _, mappingErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Clientbound, protocol.PacketConfigFinish); mappingErr != nil {
		return ConfigErr{"initialize", mappingErr}
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
	if c.ConfigHandler == nil {
		c.ConfigHandler = NewDefaultConfigHandler()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = ConfigErr{"decode packet", fmt.Errorf("malformed configuration packet: %v", recovered)}
		}
	}()

	settings := DefaultConfigurationSettings
	if c.ConfigurationSettings != nil {
		settings = *c.ConfigurationSettings
	}
	if err := settings.validate(profile); err != nil {
		return ConfigErr{"client information", err}
	}
	settingsID, mappingErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigSettings)
	if mappingErr != nil {
		return ConfigErr{"client information", mappingErr}
	}
	if err := conn.WritePacket(pk.Marshal(settingsID, settings.fields(profile)...)); err != nil {
		return ConfigErr{"client information", err}
	}

	for {
		var p pk.Packet
		if err := conn.ReadPacket(&p); err != nil {
			return ConfigErr{"config custom payload", err}
		}

		kind, resolveErr := protocol.RequirePacketKind(profile, protocol.StateConfiguration, protocol.Clientbound, p.ID)
		if resolveErr != nil {
			return ConfigErr{"packet", resolveErr}
		}

		switch kind {
		case protocol.PacketConfigCookieRequest:
			var key pk.Identifier
			err := scanConfigurationPacket(p, &key)
			if err != nil {
				return ConfigErr{"cookie request", err}
			}
			cookieContent := c.Cookies[string(key)]
			if len(cookieContent) > maxCookiePayloadBytes {
				return ConfigErr{"cookie response", fmt.Errorf("cookie %q payload is %d bytes (maximum %d)", key, len(cookieContent), maxCookiePayloadBytes)}
			}
			responseID, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigCookieResponse)
			if resolveErr != nil {
				return ConfigErr{"cookie response", resolveErr}
			}
			err = conn.WritePacket(pk.Marshal(
				responseID,
				key, pk.OptionEncoder[pk.ByteArray]{
					Has: cookieContent != nil,
					Val: pk.ByteArray(cookieContent),
				},
			))
			if err != nil {
				return ConfigErr{"cookie response", err}
			}

		case protocol.PacketConfigCustomPayload:
			var channel pk.Identifier
			var data pk.PluginMessageData
			err := scanConfigurationPacket(p, &channel, &data)
			if err != nil {
				return ConfigErr{"custom payload", err}
			}
			// TODO: Provide configuration custom data handling interface
			//
			// There are two types of Custom packet.
			// One for Login stage, the other for config and play stage.
			// The first one called "Custom Query", and the second one called "Custom Payload".
			// We can know the different by their name, the "query" is one request to one response, paired.
			// But the second one can be sent in any order.
			//
			// And the custome payload packet seems to be same in config stage and play stage.
			// How do we provide API for that?

		case protocol.PacketConfigDisconnect:
			const ErrStage = "disconnect"
			var reason chat.Message
			var err error
			if profile.Key().Protocol == 764 {
				var jsonReason chat.JsonMessage
				err = scanConfigurationPacket(p, &jsonReason)
				reason = chat.Message(jsonReason)
			} else {
				err = scanConfigurationPacket(p, &reason)
			}
			if err != nil {
				return ConfigErr{ErrStage, err}
			}
			return ConfigErr{ErrStage, DisconnectErr(reason)}

		case protocol.PacketConfigFinish:
			if err := requireEmptyConfigurationPacket(p); err != nil {
				return ConfigErr{"finish config", err}
			}
			finishID, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigFinish)
			if resolveErr != nil {
				return ConfigErr{"finish config", resolveErr}
			}
			err := conn.WritePacket(pk.Marshal(finishID))
			if err != nil {
				return ConfigErr{"finish config", err}
			}
			return nil

		case protocol.PacketConfigKeepAlive:
			const ErrStage = "keep alive"
			var keepAliveID pk.Long
			err := scanConfigurationPacket(p, &keepAliveID)
			if err != nil {
				return ConfigErr{ErrStage, err}
			}
			// send it back
			responseID, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigKeepAlive)
			if resolveErr != nil {
				return ConfigErr{ErrStage, resolveErr}
			}
			err = conn.WritePacket(pk.Marshal(
				responseID,
				keepAliveID,
			))
			if err != nil {
				return ConfigErr{ErrStage, err}
			}

		case protocol.PacketConfigPing:
			var pingID pk.Int
			err := scanConfigurationPacket(p, &pingID)
			if err != nil {
				return ConfigErr{"ping", err}
			}
			// send it back
			responseID, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigPong)
			if resolveErr != nil {
				return ConfigErr{"pong", resolveErr}
			}
			err = conn.WritePacket(pk.Marshal(
				responseID,
				pingID,
			))
			if err != nil {
				return ConfigErr{"pong", err}
			}

		case protocol.PacketConfigResetChat:
			if profile.Key().Protocol == 767 {
				if err := requireEmptyConfigurationPacket(p); err != nil {
					return ConfigErr{"reset chat", err}
				}
			}

		case protocol.PacketConfigRegistryData:
			// Registry payloads have changed repeatedly since Configuration was
			// introduced. Keep the existing audited p767 decoder; other profiles
			// can safely skip this complete framed packet while still answering
			// every packet that gates entry to Play.
			if profile.Key().Protocol != 767 {
				continue
			}
			const ErrStage = "registry"
			var registryID pk.Identifier

			r := bytes.NewReader(p.Data)
			_, err := registryID.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, err}
			}
			if err := validateNextConfigCount(r, "registry entry", maxConfigurationEntries); err != nil {
				return ConfigErr{ErrStage, err}
			}

			codec := c.Registries.Registry(string(registryID))
			if codec == nil {
				unknown := registry.NewRegistry[nbt.RawMessage]()
				_, err = unknown.ReadFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, fmt.Errorf("failed to read unknown registry %s: %w", registryID, err)}
				}
				if r.Len() != 0 {
					return ConfigErr{ErrStage, fmt.Errorf("unknown registry %s contains %d trailing bytes", registryID, r.Len())}
				}
				c.UnknownRegistries[string(registryID)] = &unknown
				continue
			}

			_, err = codec.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, fmt.Errorf("failed to read registry %s: %w", registryID, err)}
			}
			if r.Len() != 0 {
				return ConfigErr{ErrStage, fmt.Errorf("registry %s contains %d trailing bytes", registryID, r.Len())}
			}

		case protocol.PacketConfigResourcePackSend:
			var url, hash pk.String
			var forced pk.Boolean
			var prompt pk.Option[chat.JsonMessage, *chat.JsonMessage]
			if err := scanConfigurationPacket(p, &url, &hash, &forced, &prompt); err != nil {
				return ConfigErr{"resource pack", err}
			}
			res := ResourcePack{
				URL:    string(url),
				Hash:   string(hash),
				Forced: bool(forced),
			}
			if prompt.Has {
				message := chat.Message(prompt.Val)
				res.PromptMessage = &message
			}
			c.ConfigHandler.PushResourcePack(res)
			if err := writeResourcePackDeclined(conn, profile, nil); err != nil {
				return ConfigErr{"resource pack response", err}
			}

		case protocol.PacketConfigResourcePackPop:
			if profile.Key().Protocol != 767 {
				continue
			}
			var id pk.Option[pk.UUID, *pk.UUID]
			err := scanConfigurationPacket(p, &id)
			if err != nil {
				return ConfigErr{"resource pack pop", err}
			}
			if id.Has {
				c.ConfigHandler.PopResourcePack(id.Val)
			} else {
				c.ConfigHandler.PopAllResourcePack()
			}

		case protocol.PacketConfigResourcePackPush:
			var id pk.UUID
			var Url, Hash pk.String
			var Forced pk.Boolean
			var PromptMessage pk.Option[chat.Message, *chat.Message]
			err := scanConfigurationPacket(p,
				&id,
				&Url,
				&Hash,
				&Forced,
				&PromptMessage,
			)
			if err != nil {
				return ConfigErr{"resource pack", err}
			}
			res := ResourcePack{
				ID:     id,
				URL:    string(Url),
				Hash:   string(Hash),
				Forced: bool(Forced),
			}
			if PromptMessage.Has {
				res.PromptMessage = &PromptMessage.Val
			}
			c.ConfigHandler.PushResourcePack(res)
			if err := writeResourcePackDeclined(conn, profile, &id); err != nil {
				return ConfigErr{"resource pack response", err}
			}

		case protocol.PacketConfigStoreCookie:
			var key pk.Identifier
			var payload pk.ByteArray
			err := scanConfigurationPacket(p, &key, &payload)
			if err != nil {
				return ConfigErr{"store cookie", err}
			}
			if len(payload) > maxCookiePayloadBytes {
				return ConfigErr{"store cookie", fmt.Errorf("cookie %q payload is %d bytes (maximum %d)", key, len(payload), maxCookiePayloadBytes)}
			}
			c.Cookies[string(key)] = []byte(payload)

		case protocol.PacketConfigTransfer:
			var host pk.String
			var port pk.VarInt
			err := scanConfigurationPacket(p, &host, &port)
			if err != nil {
				return ConfigErr{"transfer", err}
			}
			if port <= 0 || port > 65535 {
				return ConfigErr{"transfer", fmt.Errorf("invalid transfer port %d", port)}
			}
			return ConfigErr{"transfer", &TransferError{Host: string(host), Port: int32(port)}}

		case protocol.PacketConfigFeatureFlags:
			if profile.Key().Protocol != 767 {
				continue
			}
			features := []pk.Identifier{}
			err := scanConfigurationPacket(p, pk.Array(&features))
			if err != nil {
				return ConfigErr{"update enabled features", err}
			}
			c.ConfigHandler.EnableFeature(features)

		case protocol.PacketConfigTags:
			if profile.Key().Protocol != 767 {
				continue
			}
			const ErrStage = "update tags"
			r := bytes.NewReader(p.Data)

			var length pk.VarInt
			_, err := length.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, err}
			}
			if err := validateConfigCount(length, "tag registry", maxConfigurationEntries); err != nil {
				return ConfigErr{ErrStage, err}
			}

			var registryID pk.Identifier
			for i := 0; i < int(length); i++ {
				_, err = registryID.ReadFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, err}
				}

				registry := c.Registries.Registry(string(registryID))
				if registry == nil {
					// TODO: Sice our registry system is incompelted, ignore all tags bind to non-exist registry
					_, err = idleTagsDecoder{}.ReadFrom(r)
					if err != nil {
						return ConfigErr{ErrStage, err}
					}
					continue
					// return ConfigErr{ErrStage, errors.New("unknown registry: " + string(registryID))}
				}

				_, err = registry.ReadTagsFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, err}
				}
			}
			if r.Len() != 0 {
				return ConfigErr{ErrStage, fmt.Errorf("tag packet contains %d trailing bytes", r.Len())}
			}

		case protocol.PacketConfigSelectKnownPacks:
			const ErrStage = "select known packs"
			packs := []DataPack{}
			err := scanConfigurationPacket(p, pk.Array(&packs))
			if err != nil {
				return ConfigErr{ErrStage, err}
			}
			knwonPacks := c.ConfigHandler.SelectDataPacks(packs)
			responseID, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigSelectKnownPacks)
			if resolveErr != nil {
				return ConfigErr{ErrStage, resolveErr}
			}
			err = conn.WritePacket(pk.Marshal(
				responseID,
				pk.Array(knwonPacks),
			))
			if err != nil {
				return ConfigErr{ErrStage, err}
			}

		case protocol.PacketConfigCustomReportDetails:
			if profile.Key().Protocol != 767 {
				continue
			}
			const ErrStage = "custom report details"
			var length pk.VarInt
			var title, description pk.String
			r := bytes.NewReader(p.Data)
			_, err := length.ReadFrom(r)
			if err != nil {
				return ConfigErr{ErrStage, err}
			}
			if err := validateConfigCount(length, "custom report detail", maxConfigurationEntries); err != nil {
				return ConfigErr{ErrStage, err}
			}
			for i := 0; i < int(length); i++ {
				_, err = title.ReadFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, err}
				}
				_, err = description.ReadFrom(r)
				if err != nil {
					return ConfigErr{ErrStage, err}
				}
				c.CustomReportDetails[string(title)] = string(description)
			}
			if r.Len() != 0 {
				return ConfigErr{ErrStage, fmt.Errorf("custom report details contain %d trailing bytes", r.Len())}
			}

		case protocol.PacketConfigServerLinks:
			// TODO

		case protocol.PacketConfigCodeOfConduct:
			var text pk.String
			if err := scanConfigurationPacket(p, &text); err != nil {
				return ConfigErr{"code of conduct", err}
			}
			if c.CodeOfConduct == nil || !c.CodeOfConduct(string(text)) {
				return ConfigErr{"code of conduct", &ConsentRequiredError{Text: string(text)}}
			}
			acceptID, resolveErr := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigAcceptCodeOfConduct)
			if resolveErr != nil {
				return ConfigErr{"code of conduct", resolveErr}
			}
			if err := conn.WritePacket(pk.Marshal(acceptID)); err != nil {
				return ConfigErr{"code of conduct", err}
			}

		default:
			if profile.Key().Protocol == 767 {
				return ConfigErr{"packet", fmt.Errorf("unexpected clientbound configuration packet %q (%#02x)", kind, p.ID)}
			}
			// Packet framing has already isolated the complete payload, so an
			// audited non-lifecycle packet can be skipped without desynchronizing
			// the stream. Required request/response packets are all handled above.
			continue
		}
	}
}

const (
	maxConfigurationEntries = 1 << 16
	maxCookiePayloadBytes   = 5120
	resourcePackDeclined    = 1
)

func (s ConfigurationSettings) validate(profile *protocol.Profile) error {
	if profile == nil || !profile.HasConfigurationState() {
		return fmt.Errorf("configuration settings require a configuration-state profile")
	}
	if len(s.Locale) == 0 || len(s.Locale) > 16 {
		return fmt.Errorf("locale byte length %d is outside range 1..16", len(s.Locale))
	}
	if s.ChatMode < 0 || s.ChatMode > 2 {
		return fmt.Errorf("chat mode %d is outside range 0..2", s.ChatMode)
	}
	if s.MainHand < 0 || s.MainHand > 1 {
		return fmt.Errorf("main hand %d is outside range 0..1", s.MainHand)
	}
	if profile.Key().Protocol >= 768 && (s.ParticleStatus < 0 || s.ParticleStatus > 2) {
		return fmt.Errorf("particle status %d is outside range 0..2", s.ParticleStatus)
	}
	return nil
}

func (s ConfigurationSettings) fields(profile *protocol.Profile) []pk.FieldEncoder {
	fields := []pk.FieldEncoder{
		pk.String(s.Locale),
		pk.Byte(s.ViewDistance),
		pk.VarInt(s.ChatMode),
		pk.Boolean(s.ChatColors),
		pk.UnsignedByte(s.DisplayedSkinParts),
		pk.VarInt(s.MainHand),
		pk.Boolean(s.EnableTextFiltering),
		pk.Boolean(s.AllowServerListings),
	}
	if profile != nil && profile.Key().Protocol >= 768 {
		fields = append(fields, pk.VarInt(s.ParticleStatus))
	}
	return fields
}

func scanConfigurationPacket(p pk.Packet, fields ...pk.FieldDecoder) error {
	r := bytes.NewReader(p.Data)
	for i, field := range fields {
		if field == nil {
			return fmt.Errorf("configuration field %d is nil", i)
		}
		if _, err := field.ReadFrom(r); err != nil {
			return fmt.Errorf("configuration field %d: %w", i, err)
		}
	}
	if r.Len() != 0 {
		return fmt.Errorf("configuration packet contains %d trailing bytes", r.Len())
	}
	return nil
}

func requireEmptyConfigurationPacket(p pk.Packet) error {
	if len(p.Data) != 0 {
		return fmt.Errorf("configuration packet contains %d trailing bytes", len(p.Data))
	}
	return nil
}

func writeResourcePackDeclined(conn *net.Conn, profile *protocol.Profile, id *pk.UUID) error {
	responseID, err := protocol.RequirePacketID(profile, protocol.StateConfiguration, protocol.Serverbound, protocol.PacketConfigResourcePackResponse)
	if err != nil {
		return err
	}
	if profile.Key().Protocol == 764 {
		if id != nil {
			return fmt.Errorf("protocol 764 resource-pack response unexpectedly has a UUID")
		}
		return conn.WritePacket(pk.Marshal(responseID, pk.VarInt(resourcePackDeclined)))
	}
	if id == nil {
		return fmt.Errorf("protocol %d resource-pack response requires a UUID", profile.Key().Protocol)
	}
	return conn.WritePacket(pk.Marshal(responseID, *id, pk.VarInt(resourcePackDeclined)))
}

func validateConfigCount(count pk.VarInt, field string, max int) error {
	if count < 0 || int64(count) > int64(max) {
		return fmt.Errorf("invalid %s count %d (maximum %d)", field, count, max)
	}
	return nil
}

func validateNextConfigCount(r *bytes.Reader, field string, max int) error {
	position, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	var count pk.VarInt
	_, readErr := count.ReadFrom(r)
	_, seekErr := r.Seek(position, io.SeekStart)
	if readErr != nil {
		return fmt.Errorf("read %s count: %w", field, readErr)
	}
	if seekErr != nil {
		return seekErr
	}
	return validateConfigCount(count, field, max)
}

type DataPack struct {
	Namespace string
	ID        string
	Version   string
}

func (d DataPack) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.String(d.Namespace).WriteTo(w)
	if err != nil {
		return n, err
	}
	n1, err := pk.String(d.ID).WriteTo(w)
	if err != nil {
		return n + n1, err
	}
	n2, err := pk.String(d.Version).WriteTo(w)
	return n + n1 + n2, err
}

func (d *DataPack) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = (*pk.String)(&d.Namespace).ReadFrom(r)
	if err != nil {
		return n, err
	}
	n1, err := (*pk.String)(&d.ID).ReadFrom(r)
	if err != nil {
		return n + n1, err
	}
	n2, err := (*pk.String)(&d.Version).ReadFrom(r)
	return n + n1 + n2, err
}

type DefaultConfigHandler struct {
	resourcesPack []ResourcePack
}

func NewDefaultConfigHandler() *DefaultConfigHandler {
	return &DefaultConfigHandler{
		resourcesPack: make([]ResourcePack, 0),
	}
}

func (d *DefaultConfigHandler) EnableFeature(features []pk.Identifier) {}

func (d *DefaultConfigHandler) PushResourcePack(res ResourcePack) {
	d.resourcesPack = append(d.resourcesPack, res)
}

func (d *DefaultConfigHandler) PopResourcePack(id pk.UUID) {
	for i, v := range d.resourcesPack {
		if id == v.ID {
			d.resourcesPack = append(d.resourcesPack[:i], d.resourcesPack[i+1:]...)
			break
		}
	}
}

func (d *DefaultConfigHandler) PopAllResourcePack() {
	d.resourcesPack = d.resourcesPack[:0]
}

func (d *DefaultConfigHandler) SelectDataPacks(packs []DataPack) []DataPack {
	return []DataPack{}
}

type idleTagsDecoder struct{}

func (idleTagsDecoder) ReadFrom(r io.Reader) (int64, error) {
	var count pk.VarInt
	var tag pk.Identifier
	var length pk.VarInt
	n, err := count.ReadFrom(r)
	if err != nil {
		return n, err
	}
	if err := validateConfigCount(count, "tag", maxConfigurationEntries); err != nil {
		return n, err
	}
	for i := 0; i < int(count); i++ {
		var n1, n2, n3 int64
		n1, err = tag.ReadFrom(r)
		if err != nil {
			return n + n1, err
		}
		n2, err = length.ReadFrom(r)
		if err != nil {
			return n + n1 + n2, err
		}
		if err = validateConfigCount(length, "tag value", maxConfigurationEntries); err != nil {
			return n + n1 + n2, err
		}
		n += n1 + n2

		var id pk.VarInt
		for i := 0; i < int(length); i++ {
			n3, err = id.ReadFrom(r)
			if err != nil {
				return n + n3, err
			}
			n += n3
		}
	}
	return n, nil
}
