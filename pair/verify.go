package pair

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/dio/gateway-pairs/names"
)

// VerifyCheck is one health check result inside a VerifyResult.
type VerifyCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// VerifyResult is the output of Verify.
type VerifyResult struct {
	Index   int           `json:"index"`
	Names   names.Pair    `json:"names"`
	Checks  []VerifyCheck `json:"checks"`
	Healthy bool          `json:"healthy"`
}

// VerifyOptions controls Verify behaviour.
type VerifyOptions struct {
	Prefix    string
	Suffix    string
	UseSuffix bool
	// Diagnose, when true, appends diagnostic output on failure:
	// ConfigMap watch list, tokenreviews binding, last controller log lines.
	Diagnose bool
	Out      io.Writer
}

// Verify re-runs post-install health checks for pair index without reinstalling.
// Returns a VerifyResult; result.Healthy is true only when all checks pass.
// Exits are not controlled here -- callers decide exit code based on Healthy.
func Verify(ctx context.Context, kubeClient Kubectl, index int, opts VerifyOptions) (*VerifyResult, error) {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	var n names.Pair
	if opts.UseSuffix {
		n = names.ForSuffix(opts.Prefix, opts.Suffix)
	} else {
		n = names.For(opts.Prefix, index)
	}

	res := &VerifyResult{Index: index, Names: n, Healthy: true}

	// Check 1: controller Available.
	ready, _ := kubeClient.Output(ctx,
		"get", "deployment", "envoy-gateway", "-n", n.SystemNS,
		"-o", "jsonpath={.status.availableReplicas}/{.status.replicas}",
		"--ignore-not-found")
	ready = strings.TrimSpace(ready)
	controllerOK := ready != "" && strings.HasPrefix(ready, "1/")
	res.Checks = append(res.Checks, VerifyCheck{
		Name:    "controller-available",
		OK:      controllerOK,
		Message: fmt.Sprintf("%s/envoy-gateway replicas: %s", n.SystemNS, ready),
	})

	// Check 2: GatewayClass Accepted.
	gcConds, _ := kubeClient.Output(ctx,
		"get", "gatewayclass", n.GatewayClass,
		"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}",
		"--ignore-not-found")
	gcOK := strings.Contains(gcConds, "Accepted=True")
	gwcMsg := n.GatewayClass
	if !gcOK {
		reason, _ := kubeClient.Output(ctx,
			"get", "gatewayclass", n.GatewayClass,
			"-o", `jsonpath={range .status.conditions[?(@.type=="Accepted")]}{.reason}{end}`,
			"--ignore-not-found")
		gwcMsg = fmt.Sprintf("%s Accepted=False (%s)", n.GatewayClass, strings.TrimSpace(reason))
	}
	res.Checks = append(res.Checks, VerifyCheck{
		Name:    "gatewayclass-accepted",
		OK:      gcOK,
		Message: gwcMsg,
	})

	// Check 3: L3 Gateways Programmed (if any exist).
	gwNames, _ := kubeClient.Output(ctx,
		"get", "gateways", "-n", n.DataplaneNS,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\" \"}{end}",
		"--ignore-not-found")
	for _, gw := range strings.Fields(gwNames) {
		conds, _ := kubeClient.Output(ctx,
			"get", "gateway", gw, "-n", n.DataplaneNS,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}",
			"--ignore-not-found")
		gwOK := strings.Contains(conds, "Programmed=True")
		res.Checks = append(res.Checks, VerifyCheck{
			Name:    fmt.Sprintf("gateway-%s-programmed", gw),
			OK:      gwOK,
			Message: fmt.Sprintf("%s/%s Programmed=%v", n.DataplaneNS, gw, gwOK),
		})
	}

	// Determine overall health.
	for _, c := range res.Checks {
		if !c.OK {
			res.Healthy = false
			break
		}
	}

	// Diagnose on failure.
	if !res.Healthy && opts.Diagnose {
		diagnose(ctx, kubeClient, n, opts.Out)
	}

	return res, nil
}

// diagnose prints diagnostic context on verify failure.
func diagnose(ctx context.Context, kube Kubectl, n names.Pair, w io.Writer) {
	fmt.Fprintf(w, "\n--- Diagnostics for pair %s ---\n", n.GatewayClass)

	// ConfigMap watch list.
	cm, _ := kube.Output(ctx, "get", "configmap", "envoy-gateway-config",
		"-n", n.SystemNS,
		"-o", "jsonpath={.data.envoy-gateway\\.yaml}",
		"--ignore-not-found")
	if cm != "" {
		fmt.Fprintf(w, "\nenvoy-gateway-config (relevant fields):\n")
		for _, line := range strings.Split(cm, "\n") {
			if strings.Contains(line, "controllerName") ||
				strings.Contains(line, "namespaces") ||
				strings.Contains(line, "watch") {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
	}

	// tokenreviews ClusterRoleBinding.
	crb, _ := kube.Output(ctx, "get", "clusterrolebinding",
		n.ReleaseName+"-tokenreviews",
		"-o", "jsonpath={.subjects[0].namespace}",
		"--ignore-not-found")
	if crb != "" {
		fmt.Fprintf(w, "\ntokenreviews ClusterRoleBinding subject namespace: %s\n", crb)
	} else {
		fmt.Fprintf(w, "\ntokenreviews ClusterRoleBinding: not found\n")
	}

	// Last controller log lines.
	logs, _ := kube.Output(ctx, "logs",
		"-n", n.SystemNS,
		"deploy/envoy-gateway",
		"--tail=20",
		"--ignore-errors")
	if logs != "" {
		fmt.Fprintf(w, "\nController logs (last 20 lines):\n%s\n", logs)
	}
}

// PrintVerifyResult writes a human-readable verify result to w.
func PrintVerifyResult(w io.Writer, r *VerifyResult) {
	fmt.Fprintf(w, "Verifying pair %d (%s)...\n", r.Index, r.Names.GatewayClass)
	for _, c := range r.Checks {
		status := "ok"
		if !c.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "  %-40s %s\n", c.Name, status)
		if !c.OK && c.Message != "" {
			fmt.Fprintf(w, "    %s\n", c.Message)
		}
	}
	if r.Healthy {
		fmt.Fprintf(w, "\nPair %d healthy.\n", r.Index)
	} else {
		fmt.Fprintf(w, "\nPair %d NOT healthy.\n", r.Index)
	}
}
