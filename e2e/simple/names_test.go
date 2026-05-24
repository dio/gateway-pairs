package simple_test

import (
	"fmt"
	"os"
)

// pairNames derives all resource names for one pair from the configured prefix.
// PAIR_PREFIX env var (default "tr") controls all names.
//
// Two namespaces per pair:
//   systemNS    = {prefix}-system-{index}    -- Helm release Secret + EG controller
//   dataplaneNS = {prefix}-dataplane-{index} -- Gateway + proxy + tenant HTTPRoutes
//
// Install with: --namespace {systemNS} --create-namespace
// The system namespace doubles as the Helm release namespace.
type pairNames struct {
	prefix      string
	index       int
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
	return pairNames{
		prefix:      pfx,
		index:       index,
		SystemNS:    fmt.Sprintf("%s%ssystem-%d", pfx, sep, index),
		DataplaneNS: fmt.Sprintf("%s%sdataplane-%d", pfx, sep, index),
		GWClass:     fmt.Sprintf("%s%s%d", pfx, sep, index),
	}
}

// helmSetPrefix returns --set flags for pair.namePrefix.
func (p pairNames) helmSetPrefix() []string {
	return []string{"--set", fmt.Sprintf("pair.namePrefix=%s", p.prefix)}
}
