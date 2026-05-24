// Package cli is the entry point for the gwp command-line interface.
// Subcommands: preflight, crds, pair, charts, version.
// This is a stub -- implementation tracked in docs/cli.md.
package cli

import (
	"fmt"

	// Import charts to ensure embedded assets are linked into the binary.
	_ "github.com/dio/gateway-pairs/charts"
)

// BuildInfo carries version metadata baked in at link time.
type BuildInfo struct {
	Version   string
	EGVersion string
	Commit    string
	Date      string
}

// Execute is the CLI entry point.
func Execute(info BuildInfo) error {
	// TODO: wire cobra command tree (preflight, crds, pair, charts, version).
	fmt.Printf("gwp %s (eg %s, commit %s, built %s)\n",
		info.Version, info.EGVersion, info.Commit, info.Date)
	return nil
}
