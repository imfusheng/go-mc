// Command mccompat prints the generated Minecraft Java Edition compatibility
// catalogue. Use -json for a machine-readable report.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/imfusheng/go-mc/protocol"
)

type capabilityReport struct {
	Status        string `json:"status"`
	Login         string `json:"login"`
	Configuration string `json:"configuration"`
	PlayCore      string `json:"playCore"`
	Chat          string `json:"chat"`
	Inventory     string `json:"inventory"`
	World         string `json:"world"`
	Server        string `json:"server"`
}

type profileReport struct {
	Key          string           `json:"key"`
	Transport    string           `json:"transport"`
	Protocol     int32            `json:"protocol"`
	Canonical    string           `json:"canonical"`
	Versions     []string         `json:"versions"`
	Capabilities capabilityReport `json:"capabilities"`
}

func collectCompatibility() []profileReport {
	profiles := protocol.Profiles()
	reports := make([]profileReport, 0, len(profiles))
	for _, profile := range profiles {
		versions := profile.Versions()
		names := make([]string, len(versions))
		for i, version := range versions {
			names[i] = version.Name
		}
		capabilities := profile.Capabilities()
		reports = append(reports, profileReport{
			Key:       profile.Key().String(),
			Transport: profile.Key().Transport.String(),
			Protocol:  profile.Key().Protocol,
			Canonical: profile.Version().Name,
			Versions:  names,
			Capabilities: capabilityReport{
				Status:        capabilities.Status.String(),
				Login:         capabilities.Login.String(),
				Configuration: capabilities.Configuration.String(),
				PlayCore:      capabilities.PlayCore.String(),
				Chat:          capabilities.Chat.String(),
				Inventory:     capabilities.Inventory.String(),
				World:         capabilities.World.String(),
				Server:        capabilities.Server.String(),
			},
		})
	}
	return reports
}

func writeText(reports []profileReport) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "KEY\tVERSIONS\tSTATUS\tLOGIN\tCONFIG\tPLAY\tCHAT\tINVENTORY\tWORLD\tSERVER"); err != nil {
		return err
	}
	for _, report := range reports {
		capabilities := report.Capabilities
		if _, err := fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			report.Key,
			strings.Join(report.Versions, ","),
			capabilities.Status,
			capabilities.Login,
			capabilities.Configuration,
			capabilities.PlayCore,
			capabilities.Chat,
			capabilities.Inventory,
			capabilities.World,
			capabilities.Server,
		); err != nil {
			return err
		}
	}
	return w.Flush()
}

func main() {
	jsonOutput := flag.Bool("json", false, "emit the compatibility catalogue as JSON")
	flag.Parse()

	reports := collectCompatibility()
	var err error
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(reports)
	} else {
		err = writeText(reports)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mccompat: %v\n", err)
		os.Exit(1)
	}
}
