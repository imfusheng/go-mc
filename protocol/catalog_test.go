package protocol

import "testing"

func TestCatalogueLookup(t *testing.T) {
	p, ok := ByName("1.21.1")
	if !ok {
		t.Fatal("ByName(1.21.1) did not find the current profile")
	}
	if p.Key() != (Key{Transport: TransportNetty, Protocol: 767}) {
		t.Fatalf("profile key = %v", p.Key())
	}
	if got, ok := ByProtocol(767); !ok || got != p {
		t.Fatalf("ByProtocol(767) = %v, %v", got, ok)
	}
	if got := MustByName("1.21"); got != p {
		t.Fatal("release aliases did not share one wire profile")
	}
}

func TestBuildCatalogueRejectsDuplicatePacketIDs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("buildCatalogue did not reject a duplicate packet ID")
		}
	}()
	buildCatalogue([]profileSpec{{
		versions: []Version{{Name: "test", Major: "test", Protocol: 1, Transport: TransportNetty}},
		packets: []packetSpec{
			{state: StatePlay, direction: Clientbound, kind: "one", id: 0},
			{state: StatePlay, direction: Clientbound, kind: "two", id: 0},
		},
	}})
}

func TestCatalogueSlicesAreCopies(t *testing.T) {
	versions := Versions()
	profiles := Profiles()
	versions[0].Name = "changed"
	profiles[0] = nil
	if _, ok := ByName("1.21"); !ok {
		t.Fatal("Versions exposed catalogue storage")
	}
	if got, ok := ByProtocol(767); !ok || got == nil {
		t.Fatal("Profiles exposed catalogue storage")
	}
}
