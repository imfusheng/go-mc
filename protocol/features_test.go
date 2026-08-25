package protocol

import "testing"

func TestLoginWireFamilies(t *testing.T) {
	tests := []struct {
		protocol   int32
		style      LoginStartStyle
		config     bool
		stringID   bool
		shortLen   bool
		properties bool
		strict     bool
		sessionID  bool
		verifyOpt  bool
	}{
		{4, LoginStartNameOnly, false, true, true, false, false, false, false},
		{47, LoginStartNameOnly, false, true, false, false, false, false, false},
		{578, LoginStartNameOnly, false, true, false, false, false, false, false},
		{735, LoginStartNameOnly, false, false, false, false, false, false, false},
		{758, LoginStartNameOnly, false, false, false, false, false, false, false},
		{759, LoginStartNameAndOptionalSignature, false, false, false, true, false, false, true},
		{760, LoginStartNameSignatureAndOptionalUUID, false, false, false, true, false, false, true},
		{761, LoginStartNameAndOptionalUUID, false, false, false, true, false, false, false},
		{763, LoginStartNameAndOptionalUUID, false, false, false, true, false, false, false},
		{764, LoginStartNameAndUUID, true, false, false, true, false, false, false},
		{766, LoginStartNameAndUUID, true, false, false, true, true, false, false},
		{767, LoginStartNameAndUUID, true, false, false, true, true, false, false},
		{768, LoginStartNameAndUUID, true, false, false, true, false, false, false},
		{775, LoginStartNameAndUUID, true, false, false, true, false, false, false},
		{776, LoginStartNameAndUUID, true, false, false, true, false, true, false},
	}

	for _, test := range tests {
		p := &Profile{key: Key{Transport: TransportNetty, Protocol: test.protocol}}
		if got := p.LoginStartStyle(); got != test.style {
			t.Errorf("protocol %d LoginStartStyle() = %d, want %d", test.protocol, got, test.style)
		}
		if got := p.HasConfigurationState(); got != test.config {
			t.Errorf("protocol %d HasConfigurationState() = %v, want %v", test.protocol, got, test.config)
		}
		if got := p.LoginSuccessUsesStringUUID(); got != test.stringID {
			t.Errorf("protocol %d LoginSuccessUsesStringUUID() = %v, want %v", test.protocol, got, test.stringID)
		}
		if got := p.LoginByteArraysUseShortLength(); got != test.shortLen {
			t.Errorf("protocol %d LoginByteArraysUseShortLength() = %v, want %v", test.protocol, got, test.shortLen)
		}
		if got := p.LoginSuccessHasProperties(); got != test.properties {
			t.Errorf("protocol %d LoginSuccessHasProperties() = %v, want %v", test.protocol, got, test.properties)
		}
		if got := p.LoginSuccessHasStrictErrorHandling(); got != test.strict {
			t.Errorf("protocol %d LoginSuccessHasStrictErrorHandling() = %v, want %v", test.protocol, got, test.strict)
		}
		if got := p.LoginSuccessHasSessionID(); got != test.sessionID {
			t.Errorf("protocol %d LoginSuccessHasSessionID() = %v, want %v", test.protocol, got, test.sessionID)
		}
		if got := p.EncryptionResponseUsesVerifyTokenOption(); got != test.verifyOpt {
			t.Errorf("protocol %d EncryptionResponseUsesVerifyTokenOption() = %v, want %v", test.protocol, got, test.verifyOpt)
		}
	}
}

func TestLegacyLoginIsNotClaimedAsNetty(t *testing.T) {
	p := &Profile{key: Key{Transport: TransportLegacy, Protocol: 78}}
	if p.LoginStartStyle() != LoginStartUnsupported {
		t.Fatal("legacy login was classified as a Netty login family")
	}
	if p.HasConfigurationState() {
		t.Fatal("legacy protocol unexpectedly has Configuration")
	}
}

func TestLoginSuccessStringUUIDDashBoundary(t *testing.T) {
	for _, test := range []struct {
		protocol int32
		want     bool
	}{
		{protocol: 4, want: false},
		{protocol: 5, want: true},
		{protocol: 734, want: true},
		{protocol: 735, want: false},
	} {
		p := &Profile{key: Key{Transport: TransportNetty, Protocol: test.protocol}}
		if got := p.LoginSuccessStringUUIDUsesDashes(); got != test.want {
			t.Errorf("protocol %d LoginSuccessStringUUIDUsesDashes() = %v, want %v", test.protocol, got, test.want)
		}
	}
}

func TestConfigurationRegistryDataStyleBoundaries(t *testing.T) {
	for _, test := range []struct {
		protocol int32
		want     ConfigurationRegistryDataStyle
	}{
		{protocol: 763, want: ConfigurationRegistryDataUnsupported},
		{protocol: 764, want: ConfigurationRegistryDataCompound},
		{protocol: 765, want: ConfigurationRegistryDataCompound},
		{protocol: 766, want: ConfigurationRegistryDataPerRegistry},
		{protocol: 767, want: ConfigurationRegistryDataPerRegistry},
		{protocol: 776, want: ConfigurationRegistryDataPerRegistry},
	} {
		p := &Profile{key: Key{Transport: TransportNetty, Protocol: test.protocol}}
		if got := p.ConfigurationRegistryDataStyle(); got != test.want {
			t.Errorf("protocol %d ConfigurationRegistryDataStyle() = %d, want %d", test.protocol, got, test.want)
		}
	}

	legacy := &Profile{key: Key{Transport: TransportLegacy, Protocol: 78}}
	if got := legacy.ConfigurationRegistryDataStyle(); got != ConfigurationRegistryDataUnsupported {
		t.Fatalf("legacy ConfigurationRegistryDataStyle() = %d, want unsupported", got)
	}
}
