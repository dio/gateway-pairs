// Package pair manages the lifecycle of gateway-pairs eg-pair Helm releases.
package pair

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dio/gateway-pairs/charts"
	"github.com/dio/gateway-pairs/internal/helm"
	"github.com/dio/gateway-pairs/names"
)

// Kubectl is the subset of kube.Client used by this package.
type Kubectl interface {
	Output(ctx context.Context, args ...string) (string, error)
}

// Helmer is the subset of helm.Client used by this package.
type Helmer interface {
	Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error
	List(ctx context.Context, filterRegex string) ([]helm.Release, error)
}

// Status summarises the health of one installed pair.
type Status struct {
	Index        int                `json:"index"`
	Names        names.Pair         `json:"names"`
	HelmStatus   string             `json:"helmStatus"`
	Controller   ControllerStatus   `json:"controller"`
	GatewayClass GatewayClassStatus `json:"gatewayClass"`
	L3Gateways   []GatewayStatus    `json:"l3Gateways"`
}

// ControllerStatus describes the EG controller Deployment.
type ControllerStatus struct {
	Available bool   `json:"available"`
	Ready     string `json:"ready"`
}

// GatewayClassStatus describes the cluster-scoped GatewayClass for this pair.
type GatewayClassStatus struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// GatewayStatus describes one operator-applied Gateway in the dataplane namespace.
type GatewayStatus struct {
	Name           string `json:"name"`
	Programmed     bool   `json:"programmed"`
	EnvoyProxyName string `json:"envoyProxyName,omitempty"`
	ProxyReady     string `json:"proxyReady"`
}

// InstallOptions controls pair installation.
type InstallOptions struct {
	// Prefix is the name prefix (e.g. "tr"). Default: "tr".
	Prefix string
	// ExtraSet are additional --set flags passed to helm.
	ExtraSet []string
	// HelmTimeout is the --timeout value for helm upgrade --install.
	HelmTimeout time.Duration
	// WaitTimeout is how long to poll for readiness after helm returns.
	WaitTimeout time.Duration
	// Out receives progress output.
	Out io.Writer
}

// Install installs or upgrades an eg-pair Helm release.
// It extracts the embedded chart to a temp dir, invokes helm upgrade --install
// with all required per-pair flags, then waits for the controller and GatewayClass
// to be ready.
func Install(ctx context.Context, helmClient Helmer, kubeClient Kubectl, index int, opts InstallOptions) error {
	if opts.Prefix == "" {
		opts.Prefix = "tr"
	}
	if opts.HelmTimeout == 0 {
		opts.HelmTimeout = 5 * time.Minute
	}
	if opts.WaitTimeout == 0 {
		opts.WaitTimeout = 3 * time.Minute
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}

	n := names.For(opts.Prefix, index)

	chartDir, cleanup, err := extractChart()
	if err != nil {
		return fmt.Errorf("extract chart: %w", err)
	}
	defer cleanup()

	watchNS := fmt.Sprintf("{%s,%s}", n.SystemNS, n.DataplaneNS)

	args := []string{
		"upgrade", "--install", n.ReleaseName, chartDir,
		"--namespace", n.SystemNS,
		"--create-namespace",
		"--set", fmt.Sprintf("pair.index=%d", index),
		"--set", "pair.namePrefix=" + opts.Prefix,
		"--set", "gateway-helm.config.envoyGateway.gateway.controllerName=" + n.ControllerName,
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.type=Namespaces",
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces=" + watchNS,
		"--skip-crds",
		"--timeout", helmTimeout(opts.HelmTimeout),
	}
	for _, s := range opts.ExtraSet {
		args = append(args, "--set", s)
	}

	fmt.Fprintf(opts.Out, "Installing %s into %s...\n", n.ReleaseName, n.SystemNS)
	if err := helmClient.Run(ctx, opts.Out, os.Stderr, args...); err != nil {
		return fmt.Errorf("helm upgrade --install %s: %w", n.ReleaseName, err)
	}

	fmt.Fprintf(opts.Out, "Waiting for controller (%s/envoy-gateway)... ", n.SystemNS)
	if err := waitController(ctx, kubeClient, n.SystemNS, opts.WaitTimeout); err != nil {
		return fmt.Errorf("controller not ready: %w", err)
	}
	fmt.Fprintln(opts.Out, "ok")

	fmt.Fprintf(opts.Out, "Waiting for GatewayClass %s to be Accepted... ", n.GatewayClass)
	if err := waitGatewayClass(ctx, kubeClient, n.GatewayClass, opts.WaitTimeout); err != nil {
		return fmt.Errorf("GatewayClass not Accepted: %w", err)
	}
	fmt.Fprintln(opts.Out, "ok")

	fmt.Fprintf(opts.Out, "\nPair %d ready. Apply Layer 3 resources:\n\n", index)
	fmt.Fprintf(opts.Out, "  kubectl apply -n %s -f envoyproxies.yaml\n", n.DataplaneNS)
	fmt.Fprintf(opts.Out, "  kubectl apply -n %s -f gateways.yaml     # gatewayClassName: %s\n", n.DataplaneNS, n.GatewayClass)
	fmt.Fprintf(opts.Out, "  kubectl apply -n %s -f httproutes.yaml\n\n", n.DataplaneNS)
	fmt.Fprintf(opts.Out, "  gwp pair info %d   for the exact values needed in your Gateway manifests.\n", index)
	return nil
}

// Delete uninstalls an eg-pair Helm release using the correct teardown sequence:
//
//  1. Delete all Gateways in the dataplane NS with --wait so EG deprovisions
//     the proxy Deployment and clears its ownerRef before the controller exits.
//  2. Wait for all EG-managed Deployments and Services to be gone.
//  3. helm uninstall (removes controller, ClusterRoles, both namespaces).
//  4. Delete both namespaces explicitly -- helm uninstall does not always remove
//     the release namespace in all Helm versions.
//
// Skipping step 1 leaves proxy pod finalizers uncleared. The namespace then
// hangs in Terminating indefinitely because the controller that could clear
// them is gone.
//
// Note on termination speed: EG sets terminationGracePeriodSeconds =
// drainTimeout + 300s (default 360s). In production clusters with live
// connections this is intentional. In CI or when you control the EnvoyProxy,
// set spec.shutdown.drainTimeout: "1s" to reduce the grace period to 301s.
// For immediate exit with no live connections, POST /quitquitquit to the
// Envoy admin API (127.0.0.1:19000) via kubectl port-forward before deleting
// the Gateway -- see e2e/testutil.Harness.QuitProxyPods for the implementation.
func Delete(ctx context.Context, helmClient Helmer, kubeClient Kubectl, index int, prefix string, out io.Writer) error {
	if prefix == "" {
		prefix = "tr"
	}
	if out == nil {
		out = os.Stdout
	}
	n := names.For(prefix, index)

	// Step 1: Delete all Gateways so EG deprovisions proxies before the
	// controller is removed. --wait blocks until EG removes the Deployment
	// and clears the finalizer on the Gateway object.
	fmt.Fprintf(out, "Deleting Gateways in %s (waiting for proxy deprovision)...\n", n.DataplaneNS)
	kubeClient.Output(ctx, "delete", "gateways", "--all", "-n", n.DataplaneNS, //nolint:errcheck
		"--ignore-not-found", "--wait=true", "--timeout=2m")
	kubeClient.Output(ctx, "delete", "envoyproxies", "--all", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck

	// Step 2: Wait for EG-managed Deployments and Services to be gone.
	// The Deployment deletion is the signal that EG has fully deprovisioned
	// the proxy. Services are GC'd via ownerRef shortly after.
	fmt.Fprintf(out, "Waiting for proxy resources to be removed from %s...\n", n.DataplaneNS)
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		deploys, _ := kubeClient.Output(ctx,
			"get", "deployments", "-n", n.DataplaneNS,
			"-l", "app.kubernetes.io/managed-by=envoy-gateway",
			"-o", "jsonpath={.items}", "--ignore-not-found")
		svcs, _ := kubeClient.Output(ctx,
			"get", "services", "-n", n.DataplaneNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-namespace="+n.DataplaneNS,
			"-o", "jsonpath={.items}", "--ignore-not-found")
		d := strings.TrimSpace(deploys)
		s := strings.TrimSpace(svcs)
		if (d == "[]" || d == "") && (s == "[]" || s == "") {
			break
		}
		time.Sleep(3 * time.Second)
	}

	// Step 3: helm uninstall.
	fmt.Fprintf(out, "Uninstalling %s from %s...\n", n.ReleaseName, n.SystemNS)
	if err := helmClient.Run(ctx, out, os.Stderr, "uninstall", n.ReleaseName, "--namespace", n.SystemNS); err != nil {
		return fmt.Errorf("helm uninstall %s: %w", n.ReleaseName, err)
	}

	// Step 4: Delete both namespaces explicitly.
	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		kubeClient.Output(ctx, "delete", "namespace", ns, "--ignore-not-found", "--wait=false") //nolint:errcheck
	}

	fmt.Fprintln(out, "Done.")
	return nil
}

// Get returns the status of a single installed pair.
func Get(ctx context.Context, helmClient Helmer, kubeClient Kubectl, index int, prefix string) (*Status, error) {
	if prefix == "" {
		prefix = "tr"
	}
	n := names.For(prefix, index)

	releases, err := helmClient.List(ctx, "^"+n.ReleaseName+"$")
	if err != nil {
		return nil, fmt.Errorf("helm list: %w", err)
	}

	s := &Status{Index: index, Names: n}
	if len(releases) == 0 {
		s.HelmStatus = "not-installed"
		return s, nil
	}
	s.HelmStatus = releases[0].Status

	// Controller availability
	ready, err := kubeClient.Output(ctx,
		"get", "deployment", "envoy-gateway", "-n", n.SystemNS,
		"-o", "jsonpath={.status.availableReplicas}/{.status.replicas}",
		"--ignore-not-found")
	if err == nil && ready != "" {
		s.Controller.Ready = ready
		s.Controller.Available = strings.HasPrefix(ready, "1/")
	}

	// GatewayClass
	gcConditions, err := kubeClient.Output(ctx,
		"get", "gatewayclass", n.GatewayClass,
		"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}",
		"--ignore-not-found")
	if err == nil {
		s.GatewayClass.Accepted = strings.Contains(gcConditions, "Accepted=True")
		if !s.GatewayClass.Accepted {
			s.GatewayClass.Reason, _ = kubeClient.Output(ctx,
				"get", "gatewayclass", n.GatewayClass,
				"-o", `jsonpath={range .status.conditions[?(@.type=="Accepted")]}{.reason}{end}`,
				"--ignore-not-found")
		}
	}

	// Layer 3 Gateways in dataplane NS
	gwNames, err := kubeClient.Output(ctx,
		"get", "gateways", "-n", n.DataplaneNS,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\" \"}{end}",
		"--ignore-not-found")
	if err == nil {
		for _, gwName := range strings.Fields(gwNames) {
			gs := gatewayStatus(ctx, kubeClient, gwName, n.DataplaneNS)
			s.L3Gateways = append(s.L3Gateways, gs)
		}
	}

	return s, nil
}

// List returns the status of all installed pairs, discovered via helm list.
func List(ctx context.Context, helmClient Helmer, kubeClient Kubectl, prefix string) ([]*Status, error) {
	if prefix == "" {
		prefix = "tr"
	}
	releases, err := helmClient.List(ctx, `^eg-pair-\d+$`)
	if err != nil {
		return nil, fmt.Errorf("helm list: %w", err)
	}

	var statuses []*Status
	for _, rel := range releases {
		index := 0
		fmt.Sscanf(rel.Name, "eg-pair-%d", &index)
		if index == 0 {
			continue
		}
		s, err := Get(ctx, helmClient, kubeClient, index, prefix)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, s)
	}
	return statuses, nil
}

// Info returns the coupling fields an operator needs when writing Layer 3 manifests.
func Info(prefix string, index int) names.Pair {
	if prefix == "" {
		prefix = "tr"
	}
	return names.For(prefix, index)
}

// ── internal helpers ─────────────────────────────────────────────────────────

func waitController(ctx context.Context, k Kubectl, systemNS string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := k.Output(ctx,
			"get", "deployment", "envoy-gateway", "-n", systemNS,
			"-o", "jsonpath={.status.availableReplicas}",
			"--ignore-not-found")
		if err == nil && strings.TrimSpace(out) == "1" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("controller not Available in %s after %s", systemNS, timeout)
}

func waitGatewayClass(ctx context.Context, k Kubectl, gcName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := k.Output(ctx,
			"get", "gatewayclass", gcName,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}",
			"--ignore-not-found")
		if err == nil && strings.Contains(out, "Accepted=True") {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("GatewayClass %s not Accepted after %s", gcName, timeout)
}

func gatewayStatus(ctx context.Context, k Kubectl, gwName, ns string) GatewayStatus {
	gs := GatewayStatus{Name: gwName}

	conditions, err := k.Output(ctx,
		"get", "gateway", gwName, "-n", ns,
		"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}",
		"--ignore-not-found")
	if err == nil {
		gs.Programmed = strings.Contains(conditions, "Programmed=True")
	}

	gs.EnvoyProxyName, _ = k.Output(ctx,
		"get", "gateway", gwName, "-n", ns,
		"-o", "jsonpath={.spec.infrastructure.parametersRef.name}",
		"--ignore-not-found")

	// Find the proxy Deployment owned by this gateway.
	proxyReady, err := k.Output(ctx,
		"get", "deployments", "-n", ns,
		"-l", "gateway.envoyproxy.io/owning-gateway-name="+gwName,
		"-o", "jsonpath={.items[0].status.availableReplicas}/{.items[0].status.replicas}",
		"--ignore-not-found")
	if err == nil && strings.TrimSpace(proxyReady) != "/" && proxyReady != "" {
		gs.ProxyReady = proxyReady
	} else {
		gs.ProxyReady = "-"
	}

	return gs
}

func helmTimeout(d time.Duration) string {
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func extractChart() (string, func(), error) {
	dir, err := os.MkdirTemp("", "gwp-chart-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	root := "eg-pair"
	if err := fs.WalkDir(charts.FS(), root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		data, err := fs.ReadFile(charts.FS(), path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0644)
	}); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract eg-pair chart: %w", err)
	}
	return dir, cleanup, nil
}
