//go:build e2e

package e2e_test

import (
	"fmt"
	"os"
)

// pairNames derives all resource names for one pair from the configured prefix.
// PAIR_PREFIX env var (default "tr") controls all names.
//
// One namespace per pair: systemNS is both the Helm release namespace and the
// workload namespace. Install with:
//   helm upgrade --install eg-pair-{i} ./charts/eg-pair \
//     --namespace {systemNS} --create-namespace \
//     --set pair.index={i}
//
// Naming:
//   systemNS     = {prefix}-system-{index}   (everything: release Secret + workloads)
//   gatewayClass = {prefix}-{index}
type pairNames struct {
	prefix   string
	index    int
	SystemNS string
	GWClass  string
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
		prefix:   pfx,
		index:    index,
		SystemNS: fmt.Sprintf("%s%ssystem-%d", pfx, sep, index),
		GWClass:  fmt.Sprintf("%s%s%d", pfx, sep, index),
	}
}

// helmSetPrefix returns --set flags for pair.namePrefix.
func (p pairNames) helmSetPrefix() []string {
	return []string{"--set", fmt.Sprintf("pair.namePrefix=%s", p.prefix)}
}
