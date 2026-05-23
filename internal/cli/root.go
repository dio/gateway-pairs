// Package cli is the entry point for the gwp command-line interface.
// Subcommands: preflight, crds, pair, charts, version.
// This is a stub -- implementation tracked in docs/cli.md.
package cli

import (
	"fmt"
	"io/fs"
)

// BuildInfo carries version metadata baked in at link time.
type BuildInfo struct {
	Version   string
	EGVersion string
	Commit    string
	Date      string
}

// Assets is the root embed.FS holding charts and pre-rendered CRDs.
// Set by the assets package via init(); declared here to break import cycles.
var Assets fs.FS

// Execute is the CLI entry point.
func Execute(info BuildInfo) error {
	// TODO: wire cobra command tree (preflight, crds, pair, charts, version).
	// For now, print version so goreleaser test block passes.
	fmt.Printf("gwp %s (eg %s, commit %s, built %s)\n",
		info.Version, info.EGVersion, info.Commit, info.Date)
	return nil
}
