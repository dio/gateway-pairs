//go:build e2e

package e2e_test

import (
	"fmt"
	"os"
)

// pairNames derives all resource names for one pair from the configured prefix.
// The prefix defaults to "tr" and can be overridden with PAIR_PREFIX env var.
// Naming mirrors the _helpers.tpl logic exactly:
//   namePrefix non-empty → prefix + "-"
//   releaseNS  = {prefix}-release-{index}
//   systemNS   = {prefix}-system-{index}
//   dataplaneNS = {prefix}-dataplane-{index}
//   gatewayClass = {prefix}-{index}
// When PAIR_PREFIX="" all names drop the prefix and hyphen:
//   release-{index}, system-{index}, dataplane-{index}, {index}
type pairNames struct {
	prefix      string
	index       int
	ReleaseNS   string
	SystemNS    string
	DataplaneNS string
	GWClass     string
}

func namesFor(index int) pairNames {
	pfx := os.Getenv("PAIR_PREFIX")
	if pfx == "" {
		pfx = "tr"
	}
	sep := ""
	if pfx != "" {
		sep = "-"
	}
	p := pairNames{prefix: pfx, index: index}
	p.ReleaseNS = fmt.Sprintf("%s%srelease-%d", pfx, sep, index)
	p.SystemNS = fmt.Sprintf("%s%ssystem-%d", pfx, sep, index)
	p.DataplaneNS = fmt.Sprintf("%s%sdataplane-%d", pfx, sep, index)
	p.GWClass = fmt.Sprintf("%s%s%d", pfx, sep, index)
	return p
}

// helmSetPrefix returns the --set flags for pair.namePrefix so helm uses
// the same prefix the suite derives names from.
func (p pairNames) helmSetPrefix() []string {
	return []string{"--set", fmt.Sprintf("pair.namePrefix=%s", p.prefix)}
}
