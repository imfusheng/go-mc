// Usage: go run examples/ping/ping.go localhost
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/imfusheng/go-mc/bot"
	"github.com/imfusheng/go-mc/chat"
	mcprotocol "github.com/imfusheng/go-mc/protocol"
)

var (
	protocolNumber = flag.Int("p", int(mcprotocol.LatestRelease().Protocol), "The protocol version number sent during ping")
	transportName  = flag.String("transport", "netty", "Wire transport: netty (1.7+) or legacy (1.0-1.6)")
	versionName    = flag.String("version", "", "Requested release name (for example 1.6.4)")
	majorVersion   = flag.String("major", "", "Release family; required for legacy pings (for example 1.6)")
	favicon        = flag.String("f", "", "If specified, the server's icon will be save to")
)

type status struct {
	Description chat.Message
	Players     struct {
		Max    int
		Online int
		Sample []struct {
			ID   uuid.UUID
			Name string
		}
	}
	Version struct {
		Name     string
		Protocol int
	}
	Favicon Icon
	Delay   time.Duration
}

// Icon should be a PNG image that is Base64 encoded
// (without newlines: \n, new lines no longer work since 1.13)
// and prepended with "data:image/png;base64,".
type Icon string

func (i Icon) ToImage() (icon image.Image, err error) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(string(i), prefix) {
		return nil, fmt.Errorf("server icon should prepended with %q", prefix)
	}
	base64png := strings.TrimPrefix(string(i), prefix)
	r := base64.NewDecoder(base64.StdEncoding, strings.NewReader(base64png))
	icon, err = png.Decode(r)
	return
}

var outTemp = template.Must(template.New("output").Parse(`
	Version: [{{ .Version.Protocol }}] {{ .Version.Name }}
	Description: 
{{ .Description }}
	Delay: {{ .Delay }}
	Players: {{ .Players.Online }}/{{ .Players.Max }}{{ range .Players.Sample }}
	- [{{ .Name }}] {{ .ID }}{{ end }}
`))

func (s *status) String() string {
	var sb strings.Builder
	err := outTemp.Execute(&sb, s)
	if err != nil {
		panic(err)
	}
	return sb.String()
}

func usage() {
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n%s [options] <address>[:port]\n", os.Args[0])
	flag.PrintDefaults()
}

func pingOptions(protocolNumber int, transportName, versionName, majorVersion string) (bot.PingOptions, error) {
	var transport mcprotocol.Transport
	switch strings.ToLower(transportName) {
	case "netty":
		transport = mcprotocol.TransportNetty
	case "legacy":
		transport = mcprotocol.TransportLegacy
		if majorVersion == "" {
			return bot.PingOptions{}, errors.New("-major is required when -transport=legacy")
		}
	default:
		return bot.PingOptions{}, fmt.Errorf("unsupported transport %q: use netty or legacy", transportName)
	}
	if protocolNumber < math.MinInt32 || protocolNumber > math.MaxInt32 {
		return bot.PingOptions{}, fmt.Errorf("protocol number %d is outside the signed 32-bit range", protocolNumber)
	}
	if versionName == "" {
		versionName = majorVersion
	}
	return bot.PingOptions{Version: mcprotocol.Version{
		Name:      versionName,
		Major:     majorVersion,
		Protocol:  int32(protocolNumber),
		Transport: transport,
	}}, nil
}

func main() {
	flag.Parse()
	flag.Usage = usage
	addr := flag.Arg(0)
	if addr == "" {
		fmt.Println("")
		flag.Usage()
		os.Exit(2)
	}

	options, err := pingOptions(*protocolNumber, *transportName, *versionName, *majorVersion)
	if err != nil {
		fmt.Printf("Invalid ping options: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("MCPING (%s):", addr)
	resp, delay, err := bot.PingAndListWithOptions(addr, options)
	if err != nil {
		fmt.Printf("Ping and list server fail: %v", err)
		os.Exit(1)
	}

	var s status
	err = json.Unmarshal(resp, &s)
	if err != nil {
		fmt.Print("Parse json response fail:", err)
		os.Exit(1)
	}
	s.Delay = delay

	fmt.Print(&s)
}
