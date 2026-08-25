package main

import (
	"testing"

	"github.com/imfusheng/go-mc/protocol"
)

func TestPingOptionsUsesProtocolFlag(t *testing.T) {
	options, err := pingOptions(340, "netty", "1.12.2", "1.12")
	if err != nil {
		t.Fatalf("pingOptions: %v", err)
	}
	if options.Version.Protocol != 340 {
		t.Fatalf("protocol = %d, want flag value 340", options.Version.Protocol)
	}
	if options.Version.Transport != protocol.TransportNetty {
		t.Fatalf("transport = %s, want netty", options.Version.Transport)
	}
}

func TestPingOptionsLegacyRequiresMajor(t *testing.T) {
	if _, err := pingOptions(78, "legacy", "1.6.4", ""); err == nil {
		t.Fatal("pingOptions accepted a legacy version without -major")
	}

	options, err := pingOptions(78, "legacy", "1.6.4", "1.6")
	if err != nil {
		t.Fatalf("pingOptions: %v", err)
	}
	if options.Version.Protocol != 78 || options.Version.Major != "1.6" || options.Version.Transport != protocol.TransportLegacy {
		t.Fatalf("legacy options = %+v", options.Version)
	}
}
