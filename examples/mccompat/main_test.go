package main

import (
	"testing"

	"github.com/imfusheng/go-mc/protocol"
)

func TestCollectCompatibilityMatchesCatalogue(t *testing.T) {
	reports := collectCompatibility()
	profiles := protocol.Profiles()
	if len(reports) != len(profiles) {
		t.Fatalf("got %d reports, want %d", len(reports), len(profiles))
	}

	for i, report := range reports {
		profile := profiles[i]
		if report.Key != profile.Key().String() {
			t.Fatalf("report %d key = %q, want %q", i, report.Key, profile.Key())
		}
		if report.Canonical != profile.Version().Name {
			t.Fatalf("report %d canonical = %q, want %q", i, report.Canonical, profile.Version().Name)
		}
		if len(report.Versions) != len(profile.Versions()) {
			t.Fatalf("report %d has %d versions, want %d", i, len(report.Versions), len(profile.Versions()))
		}
	}
}
