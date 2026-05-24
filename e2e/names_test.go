//go:build e2e

package e2e_test

import (
	"fmt"
	"os"
)

// pairNames derives all resource names for one pair from the configured prefix.
// PAIR_PREFIX env var (default "tr") controls all names.
//
// In GatewayNamespace mode EG places the proxy in the Gateway's namespace.
// The Gateway lives in SystemNS, so controller + proxy + tenant HTTPRoutes
// all live in SystemNS. There is no separate dataplane namespace.
//
// Naming:
//   releaseNS  = {prefix}-release-{index}  (Helm release Secret only)
//   systemNS   = {prefix}-system-{index}   (everything else)
//   gatewayClass = {prefix}-{index}
type pairNames struct {
	prefix    string
	index     int
	ReleaseNS string
	SystemNS  string
	GWClass   string
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
	p.GWClass = fmt.Sprintf("%s%s%d", pfx, sep, index)
	return p
}

// helmSetPrefix returns the --set flags for pair.namePrefix.
func (p pairNames) helmSetPrefix() []string {
	return []string{"--set", fmt.Sprintf("pair.namePrefix=%s", p.prefix)}
}
