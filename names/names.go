// Package names derives all resource names for a gateway-pairs deployment from
// a prefix and an index. It mirrors the naming rules in charts/eg-pair/templates/_helpers.tpl.
package names

import "fmt"

// Pair holds all derived names for a single eg-pair installation.
type Pair struct {
	// Prefix is the base prefix (e.g. "tr").
	Prefix string `json:"prefix"`
	// Index is the pair number (e.g. 1).
	Index int `json:"index"`

	// SystemNS is the Helm release namespace and controller namespace.
	// e.g. "tr-system-1"
	SystemNS string `json:"systemNamespace"`
	// DataplaneNS is the namespace for Gateways, proxies, and HTTPRoutes.
	// e.g. "tr-dataplane-1"
	DataplaneNS string `json:"dataplaneNamespace"`
	// GatewayClass is the cluster-scoped GatewayClass name.
	// e.g. "tr-1"
	GatewayClass string `json:"gatewayClass"`
	// ControllerName is the unique EG controller identifier.
	// e.g. "gateway.envoyproxy.io/tr-1"
	ControllerName string `json:"controllerName"`
	// ReleaseName is the Helm release name.
	// e.g. "eg-pair-1"
	ReleaseName string `json:"releaseName"`
}

// For returns all derived names for a pair given prefix and index.
// prefix="" is valid (produces "system-1", "dataplane-1", "1").
func For(prefix string, index int) Pair {
	sep := ""
	if prefix != "" {
		sep = "-"
	}
	gc := fmt.Sprintf("%s%s%d", prefix, sep, index)
	return Pair{
		Prefix:         prefix,
		Index:          index,
		SystemNS:       fmt.Sprintf("%s%ssystem-%d", prefix, sep, index),
		DataplaneNS:    fmt.Sprintf("%s%sdataplane-%d", prefix, sep, index),
		GatewayClass:   gc,
		ControllerName: "gateway.envoyproxy.io/" + gc,
		ReleaseName:    fmt.Sprintf("eg-pair-%d", index),
	}
}

// WatchNamespaces returns the two namespaces the EG controller must watch.
func (p Pair) WatchNamespaces() []string {
	return []string{p.SystemNS, p.DataplaneNS}
}

// ClusterRoleNames returns the cluster-scoped RBAC resource names owned by this pair's release.
func (p Pair) ClusterRoleNames() []string {
	return []string{
		p.ReleaseName + "-tokenreviews",
		p.ReleaseName + "-gateway-controller",
	}
}

// ClusterRoleBindingNames returns the ClusterRoleBinding names owned by this pair's release.
func (p Pair) ClusterRoleBindingNames() []string {
	return p.ClusterRoleNames() // same names by convention
}
