// Command generateprotocol builds the stable Minecraft Java Edition release
// catalogue and version-scoped packet identity maps. It generates metadata,
// not packet payload codecs: catalogue membership is not a claim that go-mc
// can log in to or play on every listed release.
package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	mojangManifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

	minecraftDataCommit      = "105097328f99a4f45cb6dca0fbef97db0cbd1cfd"
	minecraftDataURL         = "https://raw.githubusercontent.com/PrismarineJS/minecraft-data/" + minecraftDataCommit + "/data/pc/common/protocolVersions.json"
	minecraftDataSHA256      = "43feef73789dd2c0f03cdbeda08dc3814ab236beb3dadfe12e51b0335bd13595"
	minecraftDataPathsURL    = "https://raw.githubusercontent.com/PrismarineJS/minecraft-data/" + minecraftDataCommit + "/data/dataPaths.json"
	minecraftDataPathsSHA256 = "f4b7bca4fe57fd2574de3ccd08b3cd47e7d0d9857f02b92107e6edc73819438d"
	minecraftDataRawBase     = "https://raw.githubusercontent.com/PrismarineJS/minecraft-data/" + minecraftDataCommit + "/data/"

	mojang262Protocol     int32 = 776
	mojang262ReportSHA256       = "02ab88951f242b28cc91e77c9f05815982b155ce8ab8b93af54aab48f4d4cb57"

	goMC1142Protocol          int32 = 485
	goMC1142Repository              = "https://github.com/Tnze/go-mc"
	goMC1142Commit                  = "ff7439c28ed8c36720363bec67879c980c880ba0c"
	goMC1142PacketIDsBlobSHA1       = "7ea34b3b361ee7c38e04bee524df0c43d7299de4"
	goMC1142MCBotBlobSHA1           = "9abf86668cd075187778d9f5affc93d62fe5b5d3"
	goMC1142LoginBlobSHA1           = "731fb28802eda936624bef0e7fcc6325c4701193"

	mojangProtocol4              int32 = 4
	mojangProtocol4FixtureSHA256       = "9712f0fc237c0894e96b32bf9feaefdd9ce337b3936681f0b53c2c2e59b18143"

	unknownDataVersion int32 = -1
	maxSourceSize            = 2 << 20
)

var (
	check  = flag.Bool("check", false, "verify that protocol/catalog_data.go is current without writing files")
	online = flag.Bool("online", false, "with -check, fetch the live Mojang manifest and the pinned minecraft-data source")
	update = flag.Bool("update", false, "fetch sources, refresh the offline snapshots, and regenerate protocol/catalog_data.go")
)

// The snapshots contain only fields and rows used by this generator. They are
// refreshed by -update; ordinary generation and -check never require network
// access.

//go:embed sources/mojang_versions.json
var offlineManifest []byte

//go:embed sources/protocol_versions.json
var offlineProtocolVersions []byte

//go:embed sources/packet_mappings.json
var offlinePacketMappings []byte

//go:embed sources/mojang_26_2_packets.json
var mojang262PacketReport []byte

//go:embed sources/go_mc_v1_14_2_packets.json
var goMC1142PacketMappings []byte

//go:embed sources/mojang_protocol_4_verified.json
var mojangProtocol4Research []byte

type manifest struct {
	Versions []manifestVersion `json:"versions"`
}

type manifestVersion struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	ReleaseTime string `json:"releaseTime"`
}

type protocolVersion struct {
	MinecraftVersion string `json:"minecraftVersion"`
	Protocol         int32  `json:"version"`
	DataVersion      int32  `json:"dataVersion"`
	UsesNetty        bool   `json:"usesNetty"`
	MajorVersion     string `json:"majorVersion"`
	ReleaseType      string `json:"releaseType,omitempty"`
}

type release struct {
	Name        string
	Major       string
	Protocol    int32
	DataVersion int32
	UsesNetty   bool
	ReleaseTime time.Time
}

type wireKey struct {
	UsesNetty bool
	Protocol  int32
}

type generatedProfile struct {
	Key      wireKey
	Versions []release
	Packets  []packetMapping
}

type packetMappingFixture struct {
	Commit          string                 `json:"commit"`
	DataPathsSHA256 string                 `json:"dataPathsSHA256"`
	Profiles        []packetMappingProfile `json:"profiles"`
	Gaps            []packetMappingGap     `json:"gaps"`
}

type packetMappingProfile struct {
	Protocol     int32           `json:"protocol"`
	Source       string          `json:"source"`
	SourceSHA256 string          `json:"sourceSHA256"`
	Packets      []packetMapping `json:"packets"`
}

type packetMapping struct {
	State     string `json:"state"`
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	ID        int32  `json:"id"`
}

type packetMappingGap struct {
	Protocol int32  `json:"protocol"`
	Version  string `json:"version"`
	Reason   string `json:"reason"`
}

type minecraftDataPaths struct {
	PC map[string]struct {
		Protocol string `json:"protocol"`
	} `json:"pc"`
}

type historicalPacketFixture struct {
	Repository           string                    `json:"repository"`
	Tag                  string                    `json:"tag"`
	Commit               string                    `json:"commit"`
	Protocol             int32                     `json:"protocol"`
	PacketIDsBlobSHA1    string                    `json:"packetIDsBlobSHA1"`
	MCBotBlobSHA1        string                    `json:"mcbotBlobSHA1"`
	LoginBlobSHA1        string                    `json:"loginBlobSHA1"`
	MatchingPacketIDTags []string                  `json:"matchingPacketIDTags"`
	Canonicalization     string                    `json:"canonicalization"`
	Packets              []historicalPacketMapping `json:"packets"`
}

type historicalPacketMapping struct {
	State      string `json:"state"`
	Direction  string `json:"direction"`
	Kind       string `json:"kind"`
	ID         int32  `json:"id"`
	SourceKind string `json:"sourceKind"`
	SourceRef  string `json:"sourceRef"`
}

type protocol4ResearchFixture struct {
	SchemaVersion     int      `json:"schemaVersion"`
	Protocol          int32    `json:"protocol"`
	Transport         string   `json:"transport"`
	MinecraftVersions []string `json:"minecraftVersions"`
	Verification      struct {
		Conclusion         string `json:"conclusion"`
		FramedPacketCounts struct {
			Handshake       int `json:"handshake"`
			Status          int `json:"status"`
			Login           int `json:"login"`
			PlayClientbound int `json:"playClientbound"`
			PlayServerbound int `json:"playServerbound"`
		} `json:"framedPacketCounts"`
		SpecialCases []string `json:"specialCases"`
		LoginToPlay  struct {
			LoginSuccess struct {
				State     string `json:"state"`
				Direction string `json:"direction"`
				ID        int32  `json:"id"`
			} `json:"loginSuccess"`
			FirstVanillaPlayPacket struct {
				State      string `json:"state"`
				Direction  string `json:"direction"`
				ID         int32  `json:"id"`
				Kind       string `json:"kind"`
				CommonName string `json:"commonName"`
			} `json:"firstVanillaPlayPacket"`
			Evidence string `json:"evidence"`
		} `json:"loginToPlay"`
	} `json:"verification"`
	OfficialArtifacts []protocol4OfficialArtifact  `json:"officialArtifacts"`
	ClassEntries      []protocol4ClassEntry        `json:"classEntries"`
	CanonicalEvidence []protocol4CanonicalEvidence `json:"canonicalNameEvidence"`
	Extraction        struct {
		Method string `json:"method"`
		Tools  []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			URL     string `json:"url"`
			SHA256  string `json:"sha256"`
			Purpose string `json:"purpose"`
		} `json:"tools"`
	} `json:"extraction"`
	Packets []protocol4Packet `json:"packets"`
}

type protocol4OfficialArtifact struct {
	Version   string `json:"version"`
	Role      string `json:"role"`
	Metadata  string `json:"metadata"`
	ServerJar string `json:"serverJar"`
	SHA1      string `json:"sha1"`
	SHA256    string `json:"sha256"`
}

type protocol4ClassEntry struct {
	Version string `json:"version"`
	Entry   string `json:"entry"`
	Role    string `json:"role"`
	SHA256  string `json:"sha256"`
}

type protocol4CanonicalEvidence struct {
	Repository      string   `json:"repository"`
	Commit          string   `json:"commit"`
	Protocol4Commit string   `json:"protocol4Commit"`
	Protocol5Commit string   `json:"protocol5Commit"`
	File            string   `json:"file"`
	Files           []string `json:"files"`
	Note            string   `json:"note"`
}

type protocol4Packet struct {
	State     string `json:"state"`
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	ID        int32  `json:"id"`
	Framing   string `json:"framing"`
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "generateprotocol:", err)
		os.Exit(1)
	}
}

func run() error {
	if *update && (*check || *online) {
		return errors.New("-update cannot be combined with -check or -online")
	}
	if *online && !*check {
		return errors.New("-online is a drift check; use it together with -check (or use -update to refresh sources)")
	}

	manifestData := offlineManifest
	protocolData := offlineProtocolVersions
	packetMappingData := offlinePacketMappings
	if *online || *update {
		var err error
		manifestData, err = download(mojangManifestURL)
		if err != nil {
			return fmt.Errorf("download Mojang manifest: %w", err)
		}
		protocolData, err = download(minecraftDataURL)
		if err != nil {
			return fmt.Errorf("download minecraft-data: %w", err)
		}
		if err := verifySHA256(protocolData, minecraftDataSHA256); err != nil {
			return fmt.Errorf("verify minecraft-data commit %s: %w", minecraftDataCommit, err)
		}
	}

	m, versions, err := decodeSources(manifestData, protocolData)
	if err != nil {
		return err
	}
	releases, err := buildReleases(m, versions)
	if err != nil {
		return err
	}
	profiles, err := groupProfiles(releases)
	if err != nil {
		return err
	}
	if *online || *update {
		packetMappingData, err = downloadPacketMappings(profiles, versions)
		if err != nil {
			return err
		}
		if *online && !bytes.Equal(packetMappingData, offlinePacketMappings) {
			return errors.New("internal/generateprotocol/sources/packet_mappings.json is stale; run go run ./internal/generateprotocol -update")
		}
	}
	fixture, err := decodePacketMappings(packetMappingData)
	if err != nil {
		return err
	}
	mojang262Packets, err := decodeMojang262PacketReport(mojang262PacketReport)
	if err != nil {
		return err
	}
	goMC1142Packets, err := decodeGoMC1142PacketMappings(goMC1142PacketMappings)
	if err != nil {
		return err
	}
	protocol4Packets, err := decodeMojangProtocol4Research(mojangProtocol4Research)
	if err != nil {
		return err
	}
	if err := attachPacketMappings(profiles, fixture, mojang262Packets, goMC1142Packets, protocol4Packets); err != nil {
		return err
	}
	generated, err := render(profiles)
	if err != nil {
		return err
	}

	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	outputPath := filepath.Join(root, "protocol", "catalog_data.go")
	if *check {
		return checkFile(outputPath, generated)
	}

	if *update {
		manifestSnapshot, protocolSnapshot, err := snapshots(m, versions, releases)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(root, "internal", "generateprotocol", "sources", "mojang_versions.json"), manifestSnapshot); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(root, "internal", "generateprotocol", "sources", "protocol_versions.json"), protocolSnapshot); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(root, "internal", "generateprotocol", "sources", "packet_mappings.json"), packetMappingData); err != nil {
			return err
		}
	}
	return writeFile(outputPath, generated)
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		data, err := downloadOnce(client, url)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
		}
	}
	return nil, lastErr
}

func downloadOnce(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "go-mc-protocol-catalogue-generator")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceSize {
		return nil, fmt.Errorf("GET %s: response exceeds %d bytes", url, maxSourceSize)
	}
	return data, nil
}

func verifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("SHA-256 is %s, want %s", got, want)
	}
	return nil
}

func decodeSources(manifestData, protocolData []byte) (manifest, []protocolVersion, error) {
	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return manifest{}, nil, fmt.Errorf("decode Mojang manifest: %w", err)
	}
	if len(m.Versions) == 0 {
		return manifest{}, nil, errors.New("Mojang manifest contains no versions")
	}
	var versions []protocolVersion
	if err := json.Unmarshal(protocolData, &versions); err != nil {
		return manifest{}, nil, fmt.Errorf("decode minecraft-data protocol versions: %w", err)
	}
	if len(versions) == 0 {
		return manifest{}, nil, errors.New("minecraft-data contains no protocol versions")
	}
	return m, versions, nil
}

func buildReleases(m manifest, protocolVersions []protocolVersion) ([]release, error) {
	byMinecraftVersion := make(map[string]protocolVersion, len(protocolVersions))
	for _, version := range protocolVersions {
		if version.MinecraftVersion == "" {
			return nil, errors.New("minecraft-data contains an empty minecraftVersion")
		}
		if _, exists := byMinecraftVersion[version.MinecraftVersion]; exists {
			return nil, fmt.Errorf("minecraft-data contains duplicate version %q", version.MinecraftVersion)
		}
		byMinecraftVersion[version.MinecraftVersion] = version
	}

	seen := make(map[string]struct{})
	result := make([]release, 0, len(m.Versions))
	for _, candidate := range m.Versions {
		// Mojang's current manifest misclassifies the final 1.5 artifact as a
		// snapshot. It is the sole stable-release inclusion override.
		if candidate.Type != "release" && candidate.ID != "1.5" {
			continue
		}
		if _, exists := seen[candidate.ID]; exists {
			return nil, fmt.Errorf("Mojang manifest contains duplicate stable release %q", candidate.ID)
		}
		seen[candidate.ID] = struct{}{}

		releaseTime, err := time.Parse(time.RFC3339, candidate.ReleaseTime)
		if err != nil {
			return nil, fmt.Errorf("parse releaseTime for %s: %w", candidate.ID, err)
		}

		major, err := majorVersion(candidate.ID)
		if err != nil {
			return nil, err
		}
		record := release{
			Name:        candidate.ID,
			Major:       major,
			DataVersion: unknownDataVersion,
			ReleaseTime: releaseTime,
		}

		if candidate.ID == "1.7.3" {
			// minecraft-data has 1.7.3-pre and 1.7.4, but no exact 1.7.3
			// row. Protocol History records no wire change: it belongs to
			// the Netty protocol-4 profile. Its DataVersion stays unknown.
			record.Protocol = 4
			record.UsesNetty = true
			result = append(result, record)
			continue
		}

		lookupName := candidate.ID
		if lookupName == "1.0" {
			// Mojang calls the artifact 1.0; minecraft-data calls it 1.0.0.
			lookupName = "1.0.0"
		}
		protocol, ok := byMinecraftVersion[lookupName]
		if !ok {
			return nil, fmt.Errorf("stable release %q has no minecraft-data mapping", candidate.ID)
		}
		if protocol.MajorVersion != "" && protocol.MajorVersion != major {
			return nil, fmt.Errorf("stable release %q has major %q in minecraft-data, want %q", candidate.ID, protocol.MajorVersion, major)
		}
		record.Protocol = protocol.Protocol
		record.UsesNetty = protocol.UsesNetty
		// DataVersion did not exist for these early releases. minecraft-data
		// uses values below 100 as internal historical ordering markers, so
		// they must not be exposed as Mojang DataVersion values.
		if protocol.DataVersion >= 100 {
			record.DataVersion = protocol.DataVersion
		}
		result = append(result, record)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ReleaseTime.Equal(result[j].ReleaseTime) {
			return compareVersionNames(result[i].Name, result[j].Name) < 0
		}
		return result[i].ReleaseTime.Before(result[j].ReleaseTime)
	})
	return result, nil
}

func majorVersion(name string) (string, error) {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("stable release %q does not have a two-component version", name)
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err != nil {
			return "", fmt.Errorf("stable release %q is not numeric: %w", name, err)
		}
	}
	return parts[0] + "." + parts[1], nil
}

func compareVersionNames(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		var av, bv int
		if i < len(aParts) {
			av, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bv, _ = strconv.Atoi(bParts[i])
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func groupProfiles(releases []release) ([]generatedProfile, error) {
	profiles := make([]generatedProfile, 0)
	index := make(map[wireKey]int)
	for _, version := range releases {
		key := wireKey{UsesNetty: version.UsesNetty, Protocol: version.Protocol}
		position, ok := index[key]
		if !ok {
			position = len(profiles)
			index[key] = position
			profiles = append(profiles, generatedProfile{Key: key})
		}
		profiles[position].Versions = append(profiles[position].Versions, version)
	}
	if _, ok := index[wireKey{UsesNetty: false, Protocol: 47}]; !ok {
		return nil, errors.New("catalogue is missing legacy protocol 47")
	}
	if _, ok := index[wireKey{UsesNetty: true, Protocol: 47}]; !ok {
		return nil, errors.New("catalogue is missing Netty protocol 47")
	}
	return profiles, nil
}

func downloadPacketMappings(profiles []generatedProfile, protocolVersions []protocolVersion) ([]byte, error) {
	dataPathsData, err := download(minecraftDataPathsURL)
	if err != nil {
		return nil, fmt.Errorf("download minecraft-data dataPaths: %w", err)
	}
	if err := verifySHA256(dataPathsData, minecraftDataPathsSHA256); err != nil {
		return nil, fmt.Errorf("verify minecraft-data dataPaths at commit %s: %w", minecraftDataCommit, err)
	}

	var paths minecraftDataPaths
	if err := json.Unmarshal(dataPathsData, &paths); err != nil {
		return nil, fmt.Errorf("decode minecraft-data dataPaths: %w", err)
	}
	if len(paths.PC) == 0 {
		return nil, errors.New("minecraft-data dataPaths contains no Java Edition entries")
	}

	fixture := packetMappingFixture{
		Commit:          minecraftDataCommit,
		DataPathsSHA256: minecraftDataPathsSHA256,
	}
	type cachedSchema struct {
		checksum string
		packets  []packetMapping
	}
	cache := make(map[string]cachedSchema)

	for profileIndex, profile := range profiles {
		if !profile.Key.UsesNetty {
			continue
		}
		sources := make(map[string]struct{})
		for _, version := range profile.Versions {
			if path := paths.PC[version.Name].Protocol; path != "" {
				sources[path] = struct{}{}
			}
		}
		// Some early minecraft-data datasets use a marketing-line key rather
		// than individual patch-release keys (notably 1.7). Only use that
		// fallback for the last wire profile in the line, which prevents the
		// protocol-5 schema from being attributed to protocol 4.
		if len(sources) == 0 && isLatestProfileInMajor(profiles, profileIndex) {
			if path := paths.PC[profile.Versions[len(profile.Versions)-1].Major].Protocol; path != "" {
				sources[path] = struct{}{}
			}
		}
		// A final release can share its protocol with a pre-release dataset
		// even when minecraft-data has no final-release dataPaths key. Match
		// those datasets by the pinned protocolVersions table (for example,
		// protocol 108 uses the exact 1.9.1-pre2 schema).
		if len(sources) == 0 {
			for _, version := range protocolVersions {
				if version.UsesNetty && version.Protocol == profile.Key.Protocol {
					if path := paths.PC[version.MinecraftVersion].Protocol; path != "" {
						sources[path] = struct{}{}
					}
				}
			}
		}

		if len(sources) == 0 {
			fixture.Gaps = append(fixture.Gaps, packetMappingGap{
				Protocol: profile.Key.Protocol,
				Version:  profile.Versions[len(profile.Versions)-1].Name,
				Reason:   "pinned minecraft-data has no protocol schema path for this stable wire profile",
			})
			continue
		}

		sourceNames := make([]string, 0, len(sources))
		for source := range sources {
			sourceNames = append(sourceNames, source)
		}
		sort.Strings(sourceNames)
		var selected cachedSchema
		for sourceIndex, source := range sourceNames {
			schema, ok := cache[source]
			if !ok {
				data, err := download(minecraftDataRawBase + source + "/protocol.json")
				if err != nil {
					return nil, fmt.Errorf("download minecraft-data %s/protocol.json: %w", source, err)
				}
				packets, err := extractPacketMappings(data)
				if err != nil {
					return nil, fmt.Errorf("decode minecraft-data %s/protocol.json: %w", source, err)
				}
				schema = cachedSchema{checksum: sha256Hex(data), packets: packets}
				cache[source] = schema
			}
			if sourceIndex == 0 {
				selected = schema
				continue
			}
			if !equalPacketMappings(selected.packets, schema.packets) {
				return nil, fmt.Errorf("profile netty/%d aliases resolve to schemas with different packet mappings (%s and %s)", profile.Key.Protocol, sourceNames[0], source)
			}
		}
		fixture.Profiles = append(fixture.Profiles, packetMappingProfile{
			Protocol:     profile.Key.Protocol,
			Source:       sourceNames[0],
			SourceSHA256: selected.checksum,
			Packets:      selected.packets,
		})
	}

	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode packet mapping snapshot: %w", err)
	}
	return append(data, '\n'), nil
}

func isLatestProfileInMajor(profiles []generatedProfile, profileIndex int) bool {
	profile := profiles[profileIndex]
	major := profile.Versions[len(profile.Versions)-1].Major
	for i := profileIndex + 1; i < len(profiles); i++ {
		for _, version := range profiles[i].Versions {
			if version.Major == major {
				return false
			}
		}
	}
	return true
}

func extractPacketMappings(data []byte) ([]packetMapping, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}

	type directionDocument struct {
		Types map[string]json.RawMessage `json:"types"`
	}
	type stateDocument struct {
		ToClient directionDocument `json:"toClient"`
		ToServer directionDocument `json:"toServer"`
	}
	states := []struct {
		source string
		name   string
	}{
		{source: "handshaking", name: "handshake"},
		{source: "status", name: "status"},
		{source: "login", name: "login"},
		{source: "configuration", name: "configuration"},
		{source: "play", name: "play"},
	}
	directions := []struct {
		name            string
		selectDirection func(stateDocument) directionDocument
	}{
		{name: "clientbound", selectDirection: func(state stateDocument) directionDocument { return state.ToClient }},
		{name: "serverbound", selectDirection: func(state stateDocument) directionDocument { return state.ToServer }},
	}

	var result []packetMapping
	for _, state := range states {
		raw, ok := document[state.source]
		if !ok {
			continue
		}
		var stateData stateDocument
		if err := json.Unmarshal(raw, &stateData); err != nil {
			return nil, fmt.Errorf("decode %s state: %w", state.source, err)
		}
		for _, direction := range directions {
			packetType := direction.selectDirection(stateData).Types["packet"]
			if len(packetType) == 0 {
				continue
			}
			mappings, err := extractPacketMapper(packetType)
			if err != nil {
				return nil, fmt.Errorf("decode %s/%s packet mapper: %w", state.source, direction.name, err)
			}
			for encodedID, kind := range mappings {
				id, err := strconv.ParseInt(encodedID, 0, 32)
				if err != nil {
					return nil, fmt.Errorf("decode %s/%s packet ID %q: %w", state.source, direction.name, encodedID, err)
				}
				if isSpecialUnframedLegacyPing(state.name, direction.name, kind, int32(id)) {
					continue
				}
				result = append(result, packetMapping{State: state.name, Direction: direction.name, Kind: kind, ID: int32(id)})
			}
		}
	}
	sortPacketMappings(result)
	return result, nil
}

func isSpecialUnframedLegacyPing(state, direction, kind string, id int32) bool {
	return state == "handshake" && direction == "serverbound" && kind == "legacy_server_list_ping" && id == 0xfe
}

func extractPacketMapper(raw json.RawMessage) (map[string]string, error) {
	var container []json.RawMessage
	if err := json.Unmarshal(raw, &container); err != nil {
		return nil, err
	}
	if len(container) != 2 || string(container[0]) != `"container"` {
		return nil, errors.New("packet type is not a container")
	}
	var fields []struct {
		Name string          `json:"name"`
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(container[1], &fields); err != nil {
		return nil, err
	}
	for _, field := range fields {
		if field.Name != "name" {
			continue
		}
		var mapper []json.RawMessage
		if err := json.Unmarshal(field.Type, &mapper); err != nil {
			return nil, err
		}
		if len(mapper) != 2 || string(mapper[0]) != `"mapper"` {
			return nil, errors.New("packet name field is not a mapper")
		}
		var definition struct {
			Mappings map[string]string `json:"mappings"`
		}
		if err := json.Unmarshal(mapper[1], &definition); err != nil {
			return nil, err
		}
		return definition.Mappings, nil
	}
	return nil, errors.New("packet container has no name field")
}

func decodePacketMappings(data []byte) (packetMappingFixture, error) {
	var fixture packetMappingFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return packetMappingFixture{}, fmt.Errorf("decode packet mapping snapshot: %w", err)
	}
	if fixture.Commit != minecraftDataCommit {
		return packetMappingFixture{}, fmt.Errorf("packet mapping snapshot commit is %q, want %q", fixture.Commit, minecraftDataCommit)
	}
	if fixture.DataPathsSHA256 != minecraftDataPathsSHA256 {
		return packetMappingFixture{}, fmt.Errorf("packet mapping dataPaths SHA-256 is %q, want %q", fixture.DataPathsSHA256, minecraftDataPathsSHA256)
	}

	seenProfiles := make(map[int32]struct{}, len(fixture.Profiles))
	for _, profile := range fixture.Profiles {
		if _, exists := seenProfiles[profile.Protocol]; exists {
			return packetMappingFixture{}, fmt.Errorf("packet mapping snapshot contains duplicate protocol %d", profile.Protocol)
		}
		seenProfiles[profile.Protocol] = struct{}{}
		if !strings.HasPrefix(profile.Source, "pc/") || !isSHA256(profile.SourceSHA256) {
			return packetMappingFixture{}, fmt.Errorf("packet mapping protocol %d has invalid source metadata", profile.Protocol)
		}
		if err := validatePacketMappings(profile.Protocol, profile.Packets); err != nil {
			return packetMappingFixture{}, err
		}
	}
	seenGaps := make(map[int32]struct{}, len(fixture.Gaps))
	for _, gap := range fixture.Gaps {
		if _, mapped := seenProfiles[gap.Protocol]; mapped {
			return packetMappingFixture{}, fmt.Errorf("packet mapping protocol %d is both mapped and marked as a gap", gap.Protocol)
		}
		if _, exists := seenGaps[gap.Protocol]; exists {
			return packetMappingFixture{}, fmt.Errorf("packet mapping snapshot contains duplicate gap for protocol %d", gap.Protocol)
		}
		if gap.Version == "" || gap.Reason == "" {
			return packetMappingFixture{}, fmt.Errorf("packet mapping gap for protocol %d lacks version or reason", gap.Protocol)
		}
		seenGaps[gap.Protocol] = struct{}{}
	}
	return fixture, nil
}

func decodeMojang262PacketReport(data []byte) ([]packetMapping, error) {
	// The data generator omits a final newline; the checked-in JSON follows
	// repository text-file convention and adds one. Verify the authoritative
	// report bytes after removing exactly that repository newline.
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if got := sha256Hex(bytes.TrimSuffix(normalized, []byte{'\n'})); got != mojang262ReportSHA256 {
		return nil, fmt.Errorf("Mojang 26.2 packet report SHA-256 is %s, want %s", got, mojang262ReportSHA256)
	}
	type packet struct {
		ProtocolID int32 `json:"protocol_id"`
	}
	var document map[string]map[string]map[string]packet
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode Mojang 26.2 packet report: %w", err)
	}
	states := []string{"handshake", "status", "login", "configuration", "play"}
	directions := []string{"clientbound", "serverbound"}
	var result []packetMapping
	for _, state := range states {
		for _, direction := range directions {
			for resourceLocation, definition := range document[state][direction] {
				if !strings.HasPrefix(resourceLocation, "minecraft:") {
					return nil, fmt.Errorf("Mojang 26.2 report has non-Minecraft packet name %q", resourceLocation)
				}
				kind := strings.TrimPrefix(resourceLocation, "minecraft:")
				kind = normalizeMojangPacketKind(state, direction, kind)
				result = append(result, packetMapping{
					State:     state,
					Direction: direction,
					Kind:      kind,
					ID:        definition.ProtocolID,
				})
			}
		}
	}
	sortPacketMappings(result)
	if err := validatePacketMappings(mojang262Protocol, result); err != nil {
		return nil, fmt.Errorf("validate Mojang 26.2 packet report: %w", err)
	}
	return result, nil
}

func normalizeMojangPacketKind(state, direction, kind string) string {
	// Mojang's generated report and minecraft-data use different labels for
	// a small set of lifecycle packets. Normalize only direct, reviewed
	// equivalents; Play names remain Mojang's authoritative resource names.
	aliases := map[string]string{
		"handshake/serverbound/intention":                   "set_protocol",
		"status/clientbound/status_response":                "server_info",
		"status/clientbound/pong_response":                  "ping",
		"status/serverbound/status_request":                 "ping_start",
		"status/serverbound/ping_request":                   "ping",
		"login/clientbound/login_disconnect":                "disconnect",
		"login/clientbound/hello":                           "encryption_begin",
		"login/clientbound/login_finished":                  "success",
		"login/clientbound/login_compression":               "compress",
		"login/clientbound/custom_query":                    "login_plugin_request",
		"login/serverbound/hello":                           "login_start",
		"login/serverbound/key":                             "encryption_begin",
		"login/serverbound/custom_query_answer":             "login_plugin_response",
		"configuration/clientbound/resource_pack_pop":       "remove_resource_pack",
		"configuration/clientbound/resource_pack_push":      "add_resource_pack",
		"configuration/clientbound/update_enabled_features": "feature_flags",
		"configuration/clientbound/update_tags":             "tags",
		"configuration/serverbound/client_information":      "settings",
		"configuration/serverbound/resource_pack":           "resource_pack_receive",
	}
	if normalized, ok := aliases[state+"/"+direction+"/"+kind]; ok {
		return normalized
	}
	return kind
}

func decodeGoMC1142PacketMappings(data []byte) ([]packetMapping, error) {
	var fixture historicalPacketFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return nil, fmt.Errorf("decode go-mc v1.14.2 packet mapping snapshot: %w", err)
	}
	if fixture.Repository != goMC1142Repository || fixture.Tag != "v1.14.2" || fixture.Commit != goMC1142Commit || fixture.Protocol != goMC1142Protocol {
		return nil, errors.New("go-mc v1.14.2 packet mapping snapshot has invalid repository, tag, commit, or protocol provenance")
	}
	if fixture.PacketIDsBlobSHA1 != goMC1142PacketIDsBlobSHA1 || fixture.MCBotBlobSHA1 != goMC1142MCBotBlobSHA1 || fixture.LoginBlobSHA1 != goMC1142LoginBlobSHA1 {
		return nil, errors.New("go-mc v1.14.2 packet mapping snapshot has invalid Git blob provenance")
	}
	if fixture.Canonicalization == "" || len(fixture.MatchingPacketIDTags) != 2 || fixture.MatchingPacketIDTags[0] != "v1.14.1" || fixture.MatchingPacketIDTags[1] != "v1.14.3" {
		return nil, errors.New("go-mc v1.14.2 packet mapping snapshot lacks its canonicalization audit metadata")
	}
	packets := make([]packetMapping, 0, len(fixture.Packets))
	for _, packet := range fixture.Packets {
		if packet.SourceKind == "" || (packet.SourceRef != "data/packetIDs.go" && packet.SourceRef != "bot/mcbot.go" && packet.SourceRef != "bot/login.go") {
			return nil, fmt.Errorf("go-mc v1.14.2 packet mapping has invalid historical source metadata: %+v", packet)
		}
		packets = append(packets, packetMapping{
			State:     packet.State,
			Direction: packet.Direction,
			Kind:      packet.Kind,
			ID:        packet.ID,
		})
	}
	sortPacketMappings(packets)
	if err := validatePacketMappings(goMC1142Protocol, packets); err != nil {
		return nil, fmt.Errorf("validate go-mc v1.14.2 packet mappings: %w", err)
	}
	return packets, nil
}

func decodeMojangProtocol4Research(data []byte) ([]packetMapping, error) {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if got := sha256Hex(normalized); got != mojangProtocol4FixtureSHA256 {
		return nil, fmt.Errorf("Mojang protocol-4 research fixture SHA-256 is %s, want %s", got, mojangProtocol4FixtureSHA256)
	}
	var fixture protocol4ResearchFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return nil, fmt.Errorf("decode Mojang protocol-4 research fixture: %w", err)
	}
	wantVersions := []string{"1.7.2", "1.7.3", "1.7.4", "1.7.5"}
	if fixture.SchemaVersion != 1 || fixture.Protocol != mojangProtocol4 || fixture.Transport != "netty" || !equalStrings(fixture.MinecraftVersions, wantVersions) {
		return nil, errors.New("Mojang protocol-4 research fixture has invalid schema, protocol, transport, or release scope")
	}
	counts := fixture.Verification.FramedPacketCounts
	if counts.Handshake != 1 || counts.Status != 4 || counts.Login != 5 || counts.PlayClientbound != 65 || counts.PlayServerbound != 24 {
		return nil, fmt.Errorf("Mojang protocol-4 research fixture has invalid declared framed-packet counts: %+v", counts)
	}
	transition := fixture.Verification.LoginToPlay
	if fixture.Verification.Conclusion == "" || len(fixture.Verification.SpecialCases) != 1 || transition.Evidence == "" ||
		transition.LoginSuccess.State != "login" || transition.LoginSuccess.Direction != "clientbound" || transition.LoginSuccess.ID != 2 ||
		transition.FirstVanillaPlayPacket.State != "play" || transition.FirstVanillaPlayPacket.Direction != "clientbound" ||
		transition.FirstVanillaPlayPacket.ID != 1 || transition.FirstVanillaPlayPacket.Kind != "login" || transition.FirstVanillaPlayPacket.CommonName != "Join Game" {
		return nil, errors.New("Mojang protocol-4 research fixture has invalid Login Success to Join Game transition evidence")
	}
	if err := validateProtocol4Provenance(fixture); err != nil {
		return nil, err
	}

	packets := make([]packetMapping, 0, 99)
	var special int
	for _, packet := range fixture.Packets {
		if packet.Framing != "" {
			if packet.Framing != "special_unframed_legacy_ping" || !isSpecialUnframedLegacyPing(packet.State, packet.Direction, packet.Kind, packet.ID) {
				return nil, fmt.Errorf("Mojang protocol-4 research fixture has unknown special framing: %+v", packet)
			}
			special++
			continue
		}
		packets = append(packets, packetMapping{State: packet.State, Direction: packet.Direction, Kind: packet.Kind, ID: packet.ID})
	}
	if special != 1 || len(fixture.Packets) != 100 || len(packets) != 99 {
		return nil, fmt.Errorf("Mojang protocol-4 research fixture contains %d raw, %d framed, and %d special packets; want 100, 99, and 1", len(fixture.Packets), len(packets), special)
	}
	sortPacketMappings(packets)
	if err := validatePacketMappings(mojangProtocol4, packets); err != nil {
		return nil, fmt.Errorf("validate Mojang protocol-4 packet mappings: %w", err)
	}
	if !containsPacketMapping(packets, packetMapping{State: "login", Direction: "clientbound", Kind: "success", ID: 2}) ||
		!containsPacketMapping(packets, packetMapping{State: "play", Direction: "clientbound", Kind: "login", ID: 1}) {
		return nil, errors.New("Mojang protocol-4 packet mappings do not contain the verified Login Success to Join Game boundary")
	}
	return packets, nil
}

func validateProtocol4Provenance(fixture protocol4ResearchFixture) error {
	wantArtifacts := map[string]bool{"1.7.2": true, "1.7.3": true, "1.7.4": true, "1.7.5": true, "1.7.6": true}
	if len(fixture.OfficialArtifacts) != len(wantArtifacts) {
		return fmt.Errorf("Mojang protocol-4 research fixture has %d official artifacts, want %d", len(fixture.OfficialArtifacts), len(wantArtifacts))
	}
	seenArtifacts := make(map[string]struct{}, len(fixture.OfficialArtifacts))
	for _, artifact := range fixture.OfficialArtifacts {
		if !wantArtifacts[artifact.Version] {
			return fmt.Errorf("Mojang protocol-4 research fixture has unexpected official artifact %q", artifact.Version)
		}
		if _, exists := seenArtifacts[artifact.Version]; exists {
			return fmt.Errorf("Mojang protocol-4 research fixture duplicates official artifact %q", artifact.Version)
		}
		seenArtifacts[artifact.Version] = struct{}{}
		if !strings.HasPrefix(artifact.Metadata, "https://piston-meta.mojang.com/") ||
			!strings.HasPrefix(artifact.ServerJar, "https://launcher.mojang.com/v1/objects/"+artifact.SHA1+"/") ||
			!isSHA1(artifact.SHA1) || !isSHA256(artifact.SHA256) {
			return fmt.Errorf("Mojang protocol-4 research fixture has invalid JAR provenance for %s", artifact.Version)
		}
		if artifact.Version == "1.7.6" && artifact.Role != "protocol-5 boundary comparison" {
			return errors.New("Mojang protocol-4 research fixture does not mark 1.7.6 as the protocol-5 boundary")
		}
	}

	wantClassCounts := map[string]int{"1.7.2": 8, "1.7.3": 1, "1.7.5": 3, "1.7.6": 3}
	if len(fixture.ClassEntries) != 15 {
		return fmt.Errorf("Mojang protocol-4 research fixture has %d class entries, want 15", len(fixture.ClassEntries))
	}
	seenClasses := make(map[string]struct{}, len(fixture.ClassEntries))
	gotClassCounts := make(map[string]int)
	for _, entry := range fixture.ClassEntries {
		key := entry.Version + "/" + entry.Entry
		if _, exists := seenClasses[key]; exists {
			return fmt.Errorf("Mojang protocol-4 research fixture duplicates class entry %s", key)
		}
		seenClasses[key] = struct{}{}
		gotClassCounts[entry.Version]++
		if !wantArtifacts[entry.Version] || !strings.HasSuffix(entry.Entry, ".class") || entry.Role == "" || !isSHA256(entry.SHA256) {
			return fmt.Errorf("Mojang protocol-4 research fixture has invalid class-entry provenance: %+v", entry)
		}
	}
	for version, want := range wantClassCounts {
		if gotClassCounts[version] != want {
			return fmt.Errorf("Mojang protocol-4 research fixture has %d class entries for %s, want %d", gotClassCounts[version], version, want)
		}
	}

	wantEvidence := map[string]bool{"retrooper/packetevents": true, "PrismarineJS/node-minecraft-protocol": true, "C4K3/wiki.vg": true}
	if len(fixture.CanonicalEvidence) != len(wantEvidence) {
		return fmt.Errorf("Mojang protocol-4 research fixture has %d canonical-name evidence records, want %d", len(fixture.CanonicalEvidence), len(wantEvidence))
	}
	for _, evidence := range fixture.CanonicalEvidence {
		if !wantEvidence[evidence.Repository] || evidence.Note == "" || (evidence.Commit == "" && (evidence.Protocol4Commit == "" || evidence.Protocol5Commit == "")) || (evidence.File == "" && len(evidence.Files) == 0) {
			return fmt.Errorf("Mojang protocol-4 research fixture has invalid canonical-name evidence: %+v", evidence)
		}
	}
	if fixture.Extraction.Method == "" || len(fixture.Extraction.Tools) != 2 {
		return errors.New("Mojang protocol-4 research fixture lacks extraction method or tool provenance")
	}
	for _, tool := range fixture.Extraction.Tools {
		if tool.Name == "" || (tool.Version == "" && tool.Purpose == "") || (tool.SHA256 != "" && !isSHA256(tool.SHA256)) {
			return fmt.Errorf("Mojang protocol-4 research fixture has invalid extraction-tool provenance: %+v", tool)
		}
	}
	return nil
}

func containsPacketMapping(packets []packetMapping, want packetMapping) bool {
	for _, packet := range packets {
		if packet == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validatePacketMappings(protocol int32, packets []packetMapping) error {
	if len(packets) == 0 {
		return fmt.Errorf("packet mapping protocol %d has no packets", protocol)
	}
	type scope struct {
		state     string
		direction string
	}
	byKind := make(map[scope]map[string]int32)
	byID := make(map[scope]map[int32]string)
	statePresent := make(map[string]bool)
	for _, packet := range packets {
		if packet.State != "handshake" && packet.State != "status" && packet.State != "login" && packet.State != "configuration" && packet.State != "play" {
			return fmt.Errorf("packet mapping protocol %d has unknown state %q", protocol, packet.State)
		}
		if packet.Direction != "clientbound" && packet.Direction != "serverbound" {
			return fmt.Errorf("packet mapping protocol %d has unknown direction %q", protocol, packet.Direction)
		}
		if packet.Kind == "" || packet.ID < 0 {
			return fmt.Errorf("packet mapping protocol %d has invalid %s/%s packet %+v", protocol, packet.State, packet.Direction, packet)
		}
		key := scope{state: packet.State, direction: packet.Direction}
		if byKind[key] == nil {
			byKind[key] = make(map[string]int32)
			byID[key] = make(map[int32]string)
		}
		if previous, exists := byKind[key][packet.Kind]; exists {
			return fmt.Errorf("packet mapping protocol %d duplicates %s/%s kind %q at IDs %d and %d", protocol, packet.State, packet.Direction, packet.Kind, previous, packet.ID)
		}
		if previous, exists := byID[key][packet.ID]; exists {
			return fmt.Errorf("packet mapping protocol %d duplicates %s/%s ID %d for %q and %q", protocol, packet.State, packet.Direction, packet.ID, previous, packet.Kind)
		}
		byKind[key][packet.Kind] = packet.ID
		byID[key][packet.ID] = packet.Kind
		statePresent[packet.State] = true
	}
	if !statePresent["handshake"] || !statePresent["status"] || !statePresent["login"] {
		return fmt.Errorf("packet mapping protocol %d lacks handshake, status, or login mappings", protocol)
	}
	if protocol >= 764 && !statePresent["configuration"] {
		return fmt.Errorf("packet mapping protocol %d lacks configuration mappings", protocol)
	}
	if protocol < 764 && statePresent["configuration"] {
		return fmt.Errorf("packet mapping protocol %d unexpectedly contains configuration mappings", protocol)
	}
	return nil
}

func attachPacketMappings(profiles []generatedProfile, fixture packetMappingFixture, mojang262Packets, goMC1142Packets, protocol4Packets []packetMapping) error {
	mapped := make(map[int32][]packetMapping, len(fixture.Profiles))
	for _, profile := range fixture.Profiles {
		mapped[profile.Protocol] = profile.Packets
	}
	if err := validateHistoricalCanonicalNames(goMC1142Packets, mapped[480], mapped[490]); err != nil {
		return err
	}
	if !equalPacketMappings(protocol4Packets, mapped[5]) {
		return errors.New("Mojang protocol-4 framed packet order does not match the independently corroborated pinned protocol-5 table")
	}
	gaps := make(map[int32]packetMappingGap, len(fixture.Gaps))
	for _, gap := range fixture.Gaps {
		gaps[gap.Protocol] = gap
	}
	seen := make(map[int32]struct{})
	for i := range profiles {
		if !profiles[i].Key.UsesNetty {
			continue
		}
		if profiles[i].Key.Protocol == mojang262Protocol {
			profiles[i].Packets = append([]packetMapping(nil), mojang262Packets...)
			seen[profiles[i].Key.Protocol] = struct{}{}
			continue
		}
		if profiles[i].Key.Protocol == mojangProtocol4 {
			profiles[i].Packets = append([]packetMapping(nil), protocol4Packets...)
			seen[profiles[i].Key.Protocol] = struct{}{}
			continue
		}
		if profiles[i].Key.Protocol == goMC1142Protocol {
			profiles[i].Packets = append([]packetMapping(nil), goMC1142Packets...)
			seen[profiles[i].Key.Protocol] = struct{}{}
			continue
		}
		if packets, ok := mapped[profiles[i].Key.Protocol]; ok {
			profiles[i].Packets = append([]packetMapping(nil), packets...)
			seen[profiles[i].Key.Protocol] = struct{}{}
			continue
		}
		if _, ok := gaps[profiles[i].Key.Protocol]; !ok {
			return fmt.Errorf("Netty profile %d is neither mapped nor recorded as an explicit packet-schema gap", profiles[i].Key.Protocol)
		}
		seen[profiles[i].Key.Protocol] = struct{}{}
	}
	for protocol := range mapped {
		if _, ok := seen[protocol]; !ok {
			return fmt.Errorf("packet mapping snapshot contains protocol %d outside the stable catalogue", protocol)
		}
	}
	for protocol := range gaps {
		if _, ok := seen[protocol]; !ok {
			return fmt.Errorf("packet mapping snapshot contains gap %d outside the stable catalogue", protocol)
		}
	}
	return nil
}

func validateHistoricalCanonicalNames(historical, before, after []packetMapping) error {
	type key struct {
		state     string
		direction string
		id        int32
	}
	index := func(packets []packetMapping) map[key]string {
		result := make(map[key]string, len(packets))
		for _, packet := range packets {
			result[key{state: packet.State, direction: packet.Direction, id: packet.ID}] = packet.Kind
		}
		return result
	}
	beforeByID := index(before)
	afterByID := index(after)
	for _, packet := range historical {
		packetKey := key{state: packet.State, direction: packet.Direction, id: packet.ID}
		beforeKind, beforeOK := beforeByID[packetKey]
		afterKind, afterOK := afterByID[packetKey]
		if !beforeOK || !afterOK || beforeKind != packet.Kind || afterKind != packet.Kind {
			return fmt.Errorf("go-mc v1.14.2 canonical packet %s/%s ID %d %q does not match both pinned protocol-480 and protocol-490 names", packet.State, packet.Direction, packet.ID, packet.Kind)
		}
	}
	return nil
}

func sortPacketMappings(packets []packetMapping) {
	stateRank := map[string]int{"handshake": 0, "status": 1, "login": 2, "configuration": 3, "play": 4}
	directionRank := map[string]int{"clientbound": 0, "serverbound": 1}
	sort.Slice(packets, func(i, j int) bool {
		if stateRank[packets[i].State] != stateRank[packets[j].State] {
			return stateRank[packets[i].State] < stateRank[packets[j].State]
		}
		if directionRank[packets[i].Direction] != directionRank[packets[j].Direction] {
			return directionRank[packets[i].Direction] < directionRank[packets[j].Direction]
		}
		if packets[i].ID != packets[j].ID {
			return packets[i].ID < packets[j].ID
		}
		return packets[i].Kind < packets[j].Kind
	})
}

func equalPacketMappings(a, b []packetMapping) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func isSHA1(value string) bool {
	if len(value) != sha1.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func render(profiles []generatedProfile) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("// Code generated by internal/generateprotocol; DO NOT EDIT.\n\n")
	out.WriteString("package protocol\n\n")
	out.WriteString("//go:generate go run ../internal/generateprotocol\n\n")
	out.WriteString("// UnknownDataVersion means that the release predates Mojang DataVersion metadata.\n")
	out.WriteString("const UnknownDataVersion int32 = -1\n\n")
	out.WriteString("// generatedProfileSpecs is catalogue metadata, not a gameplay compatibility claim.\n")
	out.WriteString("var generatedProfileSpecs = []profileSpec{\n")
	for _, profile := range profiles {
		transport := "TransportLegacy"
		transportName := "legacy"
		if profile.Key.UsesNetty {
			transport = "TransportNetty"
			transportName = "netty"
		}
		fmt.Fprintf(&out, "\t{ // %s/%d\n", transportName, profile.Key.Protocol)
		out.WriteString("\t\tversions: []Version{\n")
		for _, version := range profile.Versions {
			dataVersion := strconv.FormatInt(int64(version.DataVersion), 10)
			if version.DataVersion == unknownDataVersion {
				dataVersion = "UnknownDataVersion"
			}
			fmt.Fprintf(&out, "\t\t\t{Name: %s, Major: %s, Protocol: %d, DataVersion: %s, Transport: %s}, // %s\n",
				strconv.Quote(version.Name), strconv.Quote(version.Major), version.Protocol, dataVersion, transport, version.ReleaseTime.Format(time.RFC3339))
		}
		out.WriteString("\t\t},\n")
		out.WriteString("\t\tcapabilities: Capabilities{\n")
		out.WriteString("\t\t\tStatus: Experimental,\n")
		if profile.Key.UsesNetty {
			out.WriteString("\t\t\tLogin: Experimental,\n")
			out.WriteString("\t\t\tPlayCore: Experimental,\n")
			if profile.Key.Protocol >= 764 {
				out.WriteString("\t\t\tConfiguration: Experimental,\n")
			}
		}
		out.WriteString("\t\t},\n")
		if len(profile.Packets) != 0 {
			out.WriteString("\t\tpackets: []packetSpec{\n")
			for _, packet := range profile.Packets {
				fmt.Fprintf(&out, "\t\t\t{state: %s, direction: %s, kind: %s, id: %d},\n",
					stateConstant(packet.State), directionConstant(packet.Direction), strconv.Quote(packet.Kind), packet.ID)
			}
			out.WriteString("\t\t},\n")
		}
		out.WriteString("\t},\n")
	}
	out.WriteString("}\n")
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated catalogue: %w\n%s", err, out.Bytes())
	}
	return formatted, nil
}

func stateConstant(state string) string {
	switch state {
	case "handshake":
		return "StateHandshake"
	case "status":
		return "StateStatus"
	case "login":
		return "StateLogin"
	case "configuration":
		return "StateConfiguration"
	case "play":
		return "StatePlay"
	default:
		panic("unknown packet state " + state)
	}
}

func directionConstant(direction string) string {
	switch direction {
	case "clientbound":
		return "Clientbound"
	case "serverbound":
		return "Serverbound"
	default:
		panic("unknown packet direction " + direction)
	}
}

func snapshots(m manifest, protocolVersions []protocolVersion, releases []release) ([]byte, []byte, error) {
	manifestByID := make(map[string]manifestVersion, len(m.Versions))
	for _, version := range m.Versions {
		manifestByID[version.ID] = version
	}
	protocolByID := make(map[string]protocolVersion, len(protocolVersions))
	for _, version := range protocolVersions {
		protocolByID[version.MinecraftVersion] = version
	}

	trimmedManifest := manifest{Versions: make([]manifestVersion, 0, len(releases))}
	trimmedProtocols := make([]protocolVersion, 0, len(releases))
	seenProtocols := make(map[string]struct{})
	for _, version := range releases {
		manifestVersion, ok := manifestByID[version.Name]
		if !ok {
			return nil, nil, fmt.Errorf("cannot snapshot missing manifest version %q", version.Name)
		}
		trimmedManifest.Versions = append(trimmedManifest.Versions, manifestVersion)
		lookupName := version.Name
		if lookupName == "1.0" {
			lookupName = "1.0.0"
		}
		if lookupName == "1.7.3" {
			continue
		}
		if _, exists := seenProtocols[lookupName]; exists {
			continue
		}
		protocolVersion, ok := protocolByID[lookupName]
		if !ok {
			return nil, nil, fmt.Errorf("cannot snapshot missing protocol version %q", lookupName)
		}
		seenProtocols[lookupName] = struct{}{}
		trimmedProtocols = append(trimmedProtocols, protocolVersion)
	}

	manifestData, err := json.MarshalIndent(trimmedManifest, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	protocolData, err := json.MarshalIndent(trimmedProtocols, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(manifestData, '\n'), append(protocolData, '\n'), nil
}

func repositoryRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate generator source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("locate repository root: %w", err)
	}
	return root, nil
}

func checkFile(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s is stale; run go generate ./protocol", filepath.ToSlash(path))
	}
	return nil
}

func writeFile(path string, data []byte) error {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.ToSlash(path), err)
	}
	return nil
}
