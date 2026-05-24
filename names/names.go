// Package names derives all resource names for a gateway-pairs deployment from
// a prefix and an index. It mirrors the naming rules in charts/eg-pair/templates/_helpers.tpl.
package names

import "fmt"

// Pair holds all derived names for a single eg-pair installation.
type Pair struct {
	// Prefix is the base prefix (e.g. "tr").
	Prefix string `json:"prefix"`
	// Index is the pair number (e.g. 1). Zero when Suffix is set.
	Index int `json:"index"`
	// Suffix is an optional string override for the index (e.g. "prod").
	// When non-empty, Index must be 0 and Suffix is used verbatim.
	Suffix string `json:"suffix,omitempty"`

	// SystemNS is the Helm release namespace and controller namespace.
	// e.g. "tr-system-1" or "tr-system-prod"
	SystemNS string `json:"systemNamespace"`
	// DataplaneNS is the namespace for Gateways, proxies, and HTTPRoutes.
	// e.g. "tr-dataplane-1" or "tr-dataplane-prod"
	DataplaneNS string `json:"dataplaneNamespace"`
	// GatewayClass is the cluster-scoped GatewayClass name.
	// e.g. "tr-1" or "tr-prod"
	GatewayClass string `json:"gatewayClass"`
	// ControllerName is the unique EG controller identifier.
	// e.g. "gateway.envoyproxy.io/tr-1" or "gateway.envoyproxy.io/tr-prod"
	ControllerName string `json:"controllerName"`
	// ReleaseName is the Helm release name.
	// e.g. "eg-pair-1" or "eg-pair-prod"
	ReleaseName string `json:"releaseName"`
}

// For returns all derived names for a numeric pair (prefix + index).
// Mirrors: namePrefix=tr, index=1 → tr-system-1, tr-dataplane-1, GatewayClass tr-1.
// prefix="" is valid (produces "system-1", "dataplane-1", "1").
func For(prefix string, index int) Pair {
	return forInternal(prefix, fmt.Sprintf("%d", index), index, "")
}

// ForSuffix returns all derived names for a string-suffixed pair (prefix + suffix).
// Mirrors: namePrefix=tr, index=0, nameSuffix=prod → tr-system-prod, tr-dataplane-prod, GatewayClass tr-prod.
// Use this when you want named pairs (e.g. "prod", "staging") instead of numeric ones.
func ForSuffix(prefix, suffix string) Pair {
	return forInternal(prefix, suffix, 0, suffix)
}

func forInternal(prefix, idStr string, index int, suffix string) Pair {
	// joinParts joins non-empty strings with "-".
	// joinParts("tr", "system") → "tr-system"
	// joinParts("",   "system") → "system"
	// joinParts("tr", "")       → "tr"
	// joinParts("",   "")       → ""

	// idFrag is the trailing part after the role word: "-1", "-prod", or "".
	idFrag := ""
	if idStr != "" {
		idFrag = "-" + idStr
	}

	// release uses the idStr when present; falls back to numeric index for
	// --no-suffix where idStr is "" but we still need a unique release name.
	release := "eg-pair-"
	if idStr != "" {
		release += idStr
	} else {
		release += fmt.Sprintf("%d", index)
	}

	gc := joinParts(prefix, idStr)

	return Pair{
		Prefix:         prefix,
		Index:          index,
		Suffix:         suffix,
		SystemNS:       joinParts(prefix, "system") + idFrag,
		DataplaneNS:    joinParts(prefix, "dataplane") + idFrag,
		GatewayClass:   gc,
		ControllerName: "gateway.envoyproxy.io/" + gc,
		ReleaseName:    release,
	}
}

// joinParts joins two strings with "-", omitting the separator when either is empty.
func joinParts(a, b string) string {
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "-" + b
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
