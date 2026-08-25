package protocol

// LoginStartStyle describes the fields following the Login Start packet ID.
// It is deliberately a wire-format family rather than a Minecraft version
// check so login codecs can remain table-driven.
type LoginStartStyle uint8

const (
	LoginStartUnsupported LoginStartStyle = iota
	LoginStartNameOnly
	LoginStartNameAndOptionalSignature
	LoginStartNameSignatureAndOptionalUUID
	LoginStartNameAndOptionalUUID
	LoginStartNameAndUUID
)

// ConfigurationRegistryDataStyle describes the two Registry Data payload
// families used by the Configuration state. Protocols 764 and 765
// (Minecraft 1.20.2 through 1.20.4) send the complete registry codec as one
// anonymous-NBT payload. Protocol 766 and later send one
// identifier-and-entries packet per registry.
type ConfigurationRegistryDataStyle uint8

const (
	ConfigurationRegistryDataUnsupported ConfigurationRegistryDataStyle = iota
	ConfigurationRegistryDataCompound
	ConfigurationRegistryDataPerRegistry
)

// LoginStartStyle returns the Login Start wire family for this profile.
func (p *Profile) LoginStartStyle() LoginStartStyle {
	if p == nil || p.key.Transport != TransportNetty {
		return LoginStartUnsupported
	}
	switch p.key.Protocol {
	case 0, 1, 2, 3:
		return LoginStartUnsupported
	case 759:
		return LoginStartNameAndOptionalSignature
	case 760:
		return LoginStartNameSignatureAndOptionalUUID
	case 761, 762, 763:
		return LoginStartNameAndOptionalUUID
	default:
		if p.key.Protocol >= 764 {
			return LoginStartNameAndUUID
		}
		return LoginStartNameOnly
	}
}

// HasLoginAcknowledgement reports whether Login Success must be acknowledged.
func (p *Profile) HasLoginAcknowledgement() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol >= 764
}

// HasConfigurationState reports whether Configuration occurs between Login
// and Play. Mojang introduced it in Java Edition 1.20.2 (protocol 764).
func (p *Profile) HasConfigurationState() bool {
	return p.HasLoginAcknowledgement()
}

// ConfigurationRegistryDataStyle returns the Registry Data wire family for
// this profile. Callers must still resolve the numeric packet ID through the
// profile because IDs change independently from the payload family.
func (p *Profile) ConfigurationRegistryDataStyle() ConfigurationRegistryDataStyle {
	if p == nil || !p.HasConfigurationState() {
		return ConfigurationRegistryDataUnsupported
	}
	if p.key.Protocol <= 765 {
		return ConfigurationRegistryDataCompound
	}
	return ConfigurationRegistryDataPerRegistry
}

// LoginSuccessUsesStringUUID reports whether Login Success carries the UUID as
// text instead of the 16-byte UUID used since Java Edition 1.16. Protocol 4
// uses 32 hexadecimal digits; protocols 5 through 734 use canonical dashes.
func (p *Profile) LoginSuccessUsesStringUUID() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol < 735
}

// LoginSuccessStringUUIDUsesDashes distinguishes the protocol-4 textual UUID
// from the canonical form introduced by protocol 5. It is meaningful only
// when LoginSuccessUsesStringUUID reports true.
func (p *Profile) LoginSuccessStringUUIDUsesDashes() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol >= 5 && p.key.Protocol < 735
}

// LoginSuccessHasProperties reports whether Login Success appends the signed
// profile property array introduced with Java Edition 1.19.
func (p *Profile) LoginSuccessHasProperties() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol >= 759
}

// LoginSuccessHasStrictErrorHandling reports the short-lived trailing boolean
// used by protocols 766 and 767. It was removed again in protocol 768.
func (p *Profile) LoginSuccessHasStrictErrorHandling() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol >= 766 && p.key.Protocol <= 767
}

// LoginSuccessHasSessionID reports whether Login Success appends the server
// session UUID introduced with Java Edition 26.2 (protocol 776).
func (p *Profile) LoginSuccessHasSessionID() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol >= 776
}

// LoginByteArraysUseShortLength reports the 1.7 encryption packet exception.
// All later Netty profiles use VarInt-prefixed byte arrays.
func (p *Profile) LoginByteArraysUseShortLength() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol <= 5
}

// EncryptionRequestHasShouldAuthenticate reports the boolean appended in
// Java Edition 1.20.5 (protocol 766).
func (p *Profile) EncryptionRequestHasShouldAuthenticate() bool {
	return p != nil && p.key.Transport == TransportNetty && p.key.Protocol >= 766
}

// EncryptionResponseUsesVerifyTokenOption reports the 1.19/1.19.1 protocol
// family where the encrypted token is preceded by a choice boolean.
func (p *Profile) EncryptionResponseUsesVerifyTokenOption() bool {
	return p != nil && p.key.Transport == TransportNetty && (p.key.Protocol == 759 || p.key.Protocol == 760)
}
