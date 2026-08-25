package protocol

import (
	"reflect"
	"testing"
)

func TestGeneratedCatalogueCardinality(t *testing.T) {
	versions := Versions()
	profiles := Profiles()
	majors := MajorVersions()
	if got, want := len(versions), 103; got != want {
		t.Fatalf("stable artifacts = %d, want %d", got, want)
	}
	if got, want := len(profiles), 64; got != want {
		t.Fatalf("wire profiles = %d, want %d", got, want)
	}
	if got, want := len(majors), 24; got != want {
		t.Fatalf("major release lines = %d, want %d", got, want)
	}

	protocolNumbers := make(map[int32]struct{})
	var legacyProfiles, nettyProfiles int
	var legacyVersions, nettyVersions int
	for _, profile := range profiles {
		protocolNumbers[profile.Key().Protocol] = struct{}{}
		switch profile.Key().Transport {
		case TransportLegacy:
			legacyProfiles++
			legacyVersions += len(profile.Versions())
		case TransportNetty:
			nettyProfiles++
			nettyVersions += len(profile.Versions())
		default:
			t.Fatalf("profile %v has unknown transport", profile.Key())
		}
	}
	if got, want := len(protocolNumbers), 63; got != want {
		t.Fatalf("distinct protocol integers = %d, want %d", got, want)
	}
	if legacyProfiles != 13 || nettyProfiles != 51 {
		t.Fatalf("profile transports = legacy:%d netty:%d, want legacy:13 netty:51", legacyProfiles, nettyProfiles)
	}
	if legacyVersions != 20 || nettyVersions != 83 {
		t.Fatalf("artifact transports = legacy:%d netty:%d, want legacy:20 netty:83", legacyVersions, nettyVersions)
	}
}

func TestGeneratedCatalogueProtocol47Collision(t *testing.T) {
	legacy, ok := ByKey(Key{Transport: TransportLegacy, Protocol: 47})
	if !ok {
		t.Fatal("legacy protocol 47 is missing")
	}
	netty, ok := ByKey(Key{Transport: TransportNetty, Protocol: 47})
	if !ok {
		t.Fatal("Netty protocol 47 is missing")
	}
	if legacy == netty {
		t.Fatal("legacy 1.4.2 and Netty 1.8 share a profile")
	}
	if got, want := legacy.Version().Name, "1.4.2"; got != want {
		t.Fatalf("legacy/47 canonical release = %q, want %q", got, want)
	}
	if got, want := netty.Version().Name, "1.8.9"; got != want {
		t.Fatalf("netty/47 canonical release = %q, want %q", got, want)
	}
	if got, ok := ByProtocol(47); !ok || got != netty {
		t.Fatalf("ByProtocol(47) = %v, %v; want the Netty profile", got, ok)
	}
}

func TestGeneratedCatalogueExplicitSourceRules(t *testing.T) {
	tests := []struct {
		name        string
		major       string
		protocol    int32
		dataVersion int32
		transport   Transport
	}{
		{name: "1.0", major: "1.0", protocol: 22, dataVersion: UnknownDataVersion, transport: TransportLegacy},
		{name: "1.5", major: "1.5", protocol: 60, dataVersion: UnknownDataVersion, transport: TransportLegacy},
		{name: "1.7.3", major: "1.7", protocol: 4, dataVersion: UnknownDataVersion, transport: TransportNetty},
		{name: "1.8.9", major: "1.8", protocol: 47, dataVersion: UnknownDataVersion, transport: TransportNetty},
		{name: "1.9", major: "1.9", protocol: 107, dataVersion: 169, transport: TransportNetty},
		{name: "26.2", major: "26.2", protocol: 776, dataVersion: 4903, transport: TransportNetty},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, ok := ByName(test.name)
			if !ok {
				t.Fatalf("ByName(%q) did not find the release", test.name)
			}
			var got Version
			for _, candidate := range profile.Versions() {
				if candidate.Name == test.name {
					got = candidate
					break
				}
			}
			want := Version{
				Name:        test.name,
				Major:       test.major,
				Protocol:    test.protocol,
				DataVersion: test.dataVersion,
				Transport:   test.transport,
			}
			if got != want {
				t.Fatalf("version = %+v, want %+v", got, want)
			}
		})
	}
}

func TestGeneratedCatalogueReleaseOrder(t *testing.T) {
	versions := Versions()
	if versions[0].Name != "1.0" || versions[len(versions)-1].Name != "26.2" {
		t.Fatalf("release endpoints = %q..%q, want 1.0..26.2", versions[0].Name, versions[len(versions)-1].Name)
	}
	positions := make(map[string]int, len(versions))
	for i, version := range versions {
		positions[version.Name] = i
	}
	for _, pair := range [][2]string{{"1.4.4", "1.4.5"}, {"1.4.5", "1.4.6"}, {"1.4.6", "1.4.7"}, {"1.21.11", "26.1"}} {
		if positions[pair[0]] >= positions[pair[1]] {
			t.Fatalf("release order has %s after %s", pair[0], pair[1])
		}
	}

	wantMajors := []string{
		"1.0", "1.1", "1.2.5", "1.3.2", "1.4.7", "1.5.2", "1.6.4", "1.7.10",
		"1.8.9", "1.9.4", "1.10.2", "1.11.2", "1.12.2", "1.13.2", "1.14.4", "1.15.2",
		"1.16.5", "1.17.1", "1.18.2", "1.19.4", "1.20.6", "1.21.11", "26.1.2", "26.2",
	}
	gotMajors := make([]string, 0, len(wantMajors))
	for _, version := range MajorVersions() {
		gotMajors = append(gotMajors, version.Name)
	}
	if !reflect.DeepEqual(gotMajors, wantMajors) {
		t.Fatalf("major representatives = %v, want %v", gotMajors, wantMajors)
	}
}

func TestGeneratedCatalogueCapabilitiesAreConservative(t *testing.T) {
	for _, profile := range Profiles() {
		capabilities := profile.Capabilities()
		if capabilities.Status != Experimental {
			t.Errorf("%v status = %v, want experimental", profile.Key(), capabilities.Status)
		}
		if profile.Key().Transport == TransportNetty {
			if capabilities.Login != Experimental || capabilities.PlayCore != Experimental {
				t.Errorf("%v Netty lifecycle capabilities = %+v, want experimental Login and PlayCore", profile.Key(), capabilities)
			}
			wantConfiguration := Unsupported
			if profile.HasConfigurationState() {
				wantConfiguration = Experimental
			}
			if capabilities.Configuration != wantConfiguration {
				t.Errorf("%v Configuration = %v, want %v", profile.Key(), capabilities.Configuration, wantConfiguration)
			}
		} else if capabilities.Login != Unsupported || capabilities.Configuration != Unsupported || capabilities.PlayCore != Unsupported {
			t.Errorf("%v overstates login/play support: %+v", profile.Key(), capabilities)
		}
		if capabilities.Chat != Unsupported || capabilities.Inventory != Unsupported || capabilities.World != Unsupported || capabilities.Server != Unsupported {
			t.Errorf("%v overstates subsystem support: %+v", profile.Key(), capabilities)
		}
	}
}
