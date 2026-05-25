// Package preflight runs pre-install cluster readiness checks.
package preflight

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dio/gateway-pairs/crd"
	"github.com/dio/gateway-pairs/names"
)
type CheckStatus string

const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

// Check is the result of one preflight check.
type Check struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
	Hint    string      `json:"hint,omitempty"`
}

// Result is the aggregate output of Run.
type Result struct {
	Checks   []Check `json:"checks"`
	Warnings int     `json:"warnings"`
	Failures int     `json:"failures"`
}

func (r *Result) add(c Check) {
	r.Checks = append(r.Checks, c)
	switch c.Status {
	case StatusWarn:
		r.Warnings++
	case StatusFail:
		r.Failures++
	}
}

// Options controls which checks to run.
type Options struct {
	// PairIndex, when > 0, enables pair-specific checks (6-8).
	PairIndex int
	// Prefix is the pair name prefix. Default: "tr".
	Prefix string
	// Suffix and UseSuffix match the global --suffix / --no-suffix flags.
	Suffix    string
	UseSuffix bool
	// UnsafeContext suppresses the hard block on non-k3d contexts but still warns.
	UnsafeContext bool
	// Out receives progress output. Default: io.Discard.
	Out io.Writer
}

// Kubectl is the subset of kube.Client used by preflight.
// Matches crd.Kubectl so Detect can be called directly.
type Kubectl interface {
	Output(ctx context.Context, args ...string) (string, error)
	RunWithStdin(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error
}

// Run executes all preflight checks in order. Stops at the first hard failure
// (unless UnsafeContext overrides check 1). Returns the accumulated result.
func Run(ctx context.Context, kube Kubectl, opts Options) (*Result, error) {
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	res := &Result{}

	// Check 1: context safety.
	checkContextSafety(ctx, kube, opts, res)
	if res.Failures > 0 && !opts.UnsafeContext {
		return res, nil
	}

	// Check 2: server reachable.
	if !checkServerReachable(ctx, kube, res) {
		return res, nil
	}

	// Check 3: RBAC.
	checkRBAC(ctx, kube, res)
	if res.Failures > 0 {
		return res, nil
	}

	// Checks 4-5: CRD state.
	checkCRDs(ctx, kube, res)

	// Checks 6-8: pair-specific (only when --pair index given).
	if opts.PairIndex > 0 {
		var n names.Pair
		if opts.UseSuffix {
			n = names.ForSuffix(opts.Prefix, opts.Suffix)
		} else {
			n = names.For(opts.Prefix, opts.PairIndex)
		}
		checkGatewayClassConflict(ctx, kube, n, res)
		checkControllerNameConflict(ctx, kube, n, res)
		checkNamespaceConflict(ctx, kube, n, res)
	}

	return res, nil
}

// ── individual checks ──────────────────────────────────────────────────────────

func checkContextSafety(ctx context.Context, kube Kubectl, opts Options, res *Result) {
	out, _ := kube.Output(ctx, "config", "current-context")
	ctx_ := strings.TrimSpace(out)
	if strings.HasPrefix(ctx_, "k3d-") {
		res.add(Check{Name: "context-safety", Status: StatusOK,
			Message: fmt.Sprintf("context: %s (k3d)", ctx_)})
		return
	}
	msg := fmt.Sprintf("context: %s is not a k3d cluster", ctx_)
	if opts.UnsafeContext {
		res.add(Check{Name: "context-safety", Status: StatusWarn,
			Message: msg,
			Hint:    "--unsafe-context passed; proceeding anyway"})
	} else {
		res.add(Check{Name: "context-safety", Status: StatusFail,
			Message: msg,
			Hint:    "pass --unsafe-context to target a non-k3d cluster"})
	}
}

func checkServerReachable(ctx context.Context, kube Kubectl, res *Result) bool {
	out, err := kube.Output(ctx, "version", "--output=json")
	if err != nil || strings.TrimSpace(out) == "" {
		res.add(Check{Name: "server-reachable", Status: StatusFail,
			Message: "server unreachable",
			Hint:    "check kubeconfig and cluster health"})
		return false
	}
	// Extract serverVersion from the JSON without importing encoding/json.
	version := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `"gitVersion"`) && strings.Contains(line, "Server") {
			// rough extract
		}
		if strings.Contains(line, `"gitVersion"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				version = strings.Trim(strings.TrimSpace(parts[1]), `",`)
			}
		}
	}
	if version == "" {
		version = "unknown"
	}
	res.add(Check{Name: "server-reachable", Status: StatusOK,
		Message: fmt.Sprintf("server reachable: %s", version)})
	return true
}

func checkRBAC(ctx context.Context, kube Kubectl, res *Result) {
	type check struct{ verb, resource string }
	required := []check{
		{"create", "namespaces"},
		{"create", "clusterroles"},
		{"create", "clusterrolebindings"},
	}
	allOK := true
	for _, c := range required {
		out, err := kube.Output(ctx, "auth", "can-i", c.verb, c.resource)
		allowed := err == nil && strings.TrimSpace(out) == "yes"
		if !allowed {
			allOK = false
			res.add(Check{Name: "rbac", Status: StatusFail,
				Message: fmt.Sprintf("cannot %s %s: forbidden", c.verb, c.resource),
				Hint:    "ensure the kubeconfig user has cluster-admin or equivalent"})
			return
		}
	}
	if allOK {
		res.add(Check{Name: "rbac", Status: StatusOK,
			Message: "can create namespaces, clusterroles, clusterrolebindings"})
	}
}

func checkCRDs(ctx context.Context, kube Kubectl, res *Result) {
	detected, err := crd.Detect(ctx, kube)
	if err != nil {
		res.add(Check{Name: "gateway-api-crds", Status: StatusWarn,
			Message: fmt.Sprintf("CRD detection failed: %v", err)})
		return
	}

	// Gateway API CRDs.
	switch detected.GatewayAPI.State {
	case crd.NotInstalled:
		res.add(Check{Name: "gateway-api-crds", Status: StatusOK,
			Message: "gateway-api CRDs not installed -- will install",
			Hint:    "run: gwp crds install"})
	case crd.SelfManaged:
		res.add(Check{Name: "gateway-api-crds", Status: StatusOK,
			Message: fmt.Sprintf("gateway-api CRDs %s %s installed",
				detected.GatewayAPI.BundleVersion, detected.GatewayAPI.Channel)})
	case crd.ProviderManaged:
		res.add(Check{Name: "gateway-api-crds", Status: StatusWarn,
			Message: fmt.Sprintf("gateway-api CRDs managed by %s (%s %s) -- skipping",
				detected.GatewayAPI.ProviderManager,
				detected.GatewayAPI.BundleVersion, detected.GatewayAPI.Channel)})
	}

	// EG CRDs.
	switch detected.EG.State {
	case crd.NotInstalled:
		res.add(Check{Name: "eg-crds", Status: StatusWarn,
			Message: "envoy-gateway CRDs not installed",
			Hint:    "run: gwp crds install"})
	case crd.SelfManaged, crd.ProviderManaged:
		res.add(Check{Name: "eg-crds", Status: StatusOK,
			Message: "envoy-gateway CRDs installed"})
	}
}

func checkGatewayClassConflict(ctx context.Context, kube Kubectl, n names.Pair, res *Result) {
	out, err := kube.Output(ctx, "get", "gatewayclass", n.GatewayClass,
		"-o", "jsonpath={.metadata.labels.app\\.kubernetes\\.io/managed-by}",
		"--ignore-not-found")
	if err != nil || strings.TrimSpace(out) == "" {
		res.add(Check{Name: "gatewayclass-conflict", Status: StatusOK,
			Message: fmt.Sprintf("GatewayClass %s not found", n.GatewayClass)})
		return
	}
	res.add(Check{Name: "gatewayclass-conflict", Status: StatusFail,
		Message: fmt.Sprintf("GatewayClass %s already exists", n.GatewayClass),
		Hint:    fmt.Sprintf("choose a different index/suffix, or: kubectl delete gatewayclass %s", n.GatewayClass)})
}

func checkControllerNameConflict(ctx context.Context, kube Kubectl, n names.Pair, res *Result) {
	// List all envoy-gateway-config ConfigMaps cluster-wide.
	out, err := kube.Output(ctx, "get", "configmaps", "--all-namespaces",
		"--field-selector=metadata.name=envoy-gateway-config",
		"-o", "jsonpath={range .items[*]}{.metadata.namespace}{\" \"}{.data.envoy-gateway\\.yaml}{\"---\"}{end}")
	if err != nil {
		res.add(Check{Name: "controller-name-conflict", Status: StatusWarn,
			Message: fmt.Sprintf("could not list ConfigMaps: %v", err)})
		return
	}
	if strings.Contains(out, n.ControllerName) {
		res.add(Check{Name: "controller-name-conflict", Status: StatusFail,
			Message: fmt.Sprintf("controllerName %s already in use by another controller", n.ControllerName),
			Hint:    "choose a different prefix/suffix or remove the conflicting pair first"})
		return
	}
	res.add(Check{Name: "controller-name-conflict", Status: StatusOK,
		Message: fmt.Sprintf("controllerName %s not in use", n.ControllerName)})
}

func checkNamespaceConflict(ctx context.Context, kube Kubectl, n names.Pair, res *Result) {
	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		out, _ := kube.Output(ctx, "get", "namespace", ns,
			"-o", "jsonpath={.metadata.labels.app\\.kubernetes\\.io/managed-by}",
			"--ignore-not-found")
		out = strings.TrimSpace(out)
		if out == "" {
			res.add(Check{Name: "namespace-conflict", Status: StatusOK,
				Message: fmt.Sprintf("namespace %s does not exist", ns)})
		} else if out == "Helm" {
			res.add(Check{Name: "namespace-conflict", Status: StatusWarn,
				Message: fmt.Sprintf("namespace %s exists (Helm-managed, will be adopted)", ns)})
		} else {
			res.add(Check{Name: "namespace-conflict", Status: StatusFail,
				Message: fmt.Sprintf("namespace %s exists and is not Helm-managed", ns),
				Hint:    fmt.Sprintf("delete manually: kubectl delete namespace %s", ns)})
		}
	}
}

// Print writes a human-readable preflight result to w.
func Print(w io.Writer, r *Result, nextCommands []string) {
	for _, c := range r.Checks {
		tag := "[OK]  "
		switch c.Status {
		case StatusWarn:
			tag = "[WARN]"
		case StatusFail:
			tag = "[FAIL]"
		}
		fmt.Fprintf(w, "%s %s\n", tag, c.Message)
		if c.Hint != "" {
			fmt.Fprintf(w, "       %s\n", c.Hint)
		}
	}
	fmt.Fprintln(w)
	if r.Failures > 0 {
		fmt.Fprintf(w, "%d warning(s), %d failure(s). NOT ready to install.\n",
			r.Warnings, r.Failures)
		return
	}
	fmt.Fprintf(w, "%d warning(s), 0 failures. Ready to install.\n", r.Warnings)
	if len(nextCommands) > 0 {
		fmt.Fprintln(w)
		for _, cmd := range nextCommands {
			fmt.Fprintf(w, "  %s\n", cmd)
		}
	}
}
