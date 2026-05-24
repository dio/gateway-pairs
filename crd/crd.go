// Package crd handles Gateway API and Envoy Gateway CRD detection and installation.
package crd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/dio/gateway-pairs/charts"
)

// Kubectl is the subset of kube.Client used by this package.
// Injecting the interface lets tests pass a fake without exec.
type Kubectl interface {
	Output(ctx context.Context, args ...string) (string, error)
	RunWithStdin(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error
}

// State describes the installation state of a CRD bundle.
type State int

const (
	// NotInstalled means the CRD was not found on the cluster.
	NotInstalled State = iota
	// SelfManaged means the CRD exists and was installed by a non-provider tool.
	SelfManaged
	// ProviderManaged means the CRD is owned by a cloud provider controller.
	// gwp will not overwrite provider-managed CRDs by default.
	ProviderManaged
)

func (s State) String() string {
	switch s {
	case NotInstalled:
		return "not-installed"
	case SelfManaged:
		return "self-managed"
	case ProviderManaged:
		return "provider-managed"
	default:
		return "unknown"
	}
}

// GatewayAPIInfo describes the Gateway API CRDs found on a cluster.
type GatewayAPIInfo struct {
	State           State
	BundleVersion   string
	Channel         string
	ProviderManager string // non-empty when ProviderManaged
}

// EGInfo describes the Envoy Gateway CRDs found on a cluster.
type EGInfo struct {
	State   State
	Version string
}

// DetectResult is the combined output of a CRD detection pass.
type DetectResult struct {
	GatewayAPI GatewayAPIInfo
	EG         EGInfo
}

var knownProviderManagers = []string{
	"gke-networking-controller",
	"gke-gateway-api",
	"aks-gateway-api-controller",
	"addon-manager",
}

// Detect inspects the cluster and returns the current CRD installation state.
func Detect(ctx context.Context, kube Kubectl) (DetectResult, error) {
	var r DetectResult

	// Gateway API: probe gateways.gateway.networking.k8s.io
	gapi, err := detectGatewayAPI(ctx, kube)
	if err != nil {
		return r, fmt.Errorf("detect gateway-api CRDs: %w", err)
	}
	r.GatewayAPI = gapi

	// EG: probe envoyproxies.gateway.envoyproxy.io
	eg, err := detectEG(ctx, kube)
	if err != nil {
		return r, fmt.Errorf("detect envoy-gateway CRDs: %w", err)
	}
	r.EG = eg

	return r, nil
}

func detectGatewayAPI(ctx context.Context, k Kubectl) (GatewayAPIInfo, error) {
	// jsonpath reads can fail cleanly when CRD is absent
	version, err := k.Output(ctx,
		"get", "crd", "gateways.gateway.networking.k8s.io",
		"-o", "jsonpath={.metadata.annotations.gateway\\.networking\\.k8s\\.io/bundle-version}",
		"--ignore-not-found")
	if err != nil {
		return GatewayAPIInfo{}, err
	}
	if version == "" {
		return GatewayAPIInfo{State: NotInstalled}, nil
	}

	channel, _ := k.Output(ctx,
		"get", "crd", "gateways.gateway.networking.k8s.io",
		"-o", "jsonpath={.metadata.annotations.gateway\\.networking\\.k8s\\.io/channel}",
		"--ignore-not-found")

	// Check managedFields for provider fingerprints.
	// bundle-version alone is not a provider-ownership signal.
	managers, _ := k.Output(ctx,
		"get", "crd", "gateways.gateway.networking.k8s.io",
		"-o", "jsonpath={range .metadata.managedFields[*]}{.manager}{\" \"}{end}")
	for _, pm := range knownProviderManagers {
		if containsWord(managers, pm) {
			return GatewayAPIInfo{
				State:           ProviderManaged,
				BundleVersion:   version,
				Channel:         channel,
				ProviderManager: pm,
			}, nil
		}
	}

	return GatewayAPIInfo{
		State:         SelfManaged,
		BundleVersion: version,
		Channel:       channel,
	}, nil
}

func detectEG(ctx context.Context, k Kubectl) (EGInfo, error) {
	version, err := k.Output(ctx,
		"get", "crd", "envoyproxies.gateway.envoyproxy.io",
		"-o", "jsonpath={.metadata.labels.app\\.kubernetes\\.io/version}",
		"--ignore-not-found")
	if err != nil {
		return EGInfo{}, err
	}
	if version == "" {
		return EGInfo{State: NotInstalled}, nil
	}
	return EGInfo{State: SelfManaged, Version: version}, nil
}

// InstallOptions controls the behavior of Install.
type InstallOptions struct {
	// SkipGatewayAPI skips Gateway API CRDs regardless of detection result.
	SkipGatewayAPI bool
	// ForceGatewayAPI installs Gateway API CRDs even when already present.
	ForceGatewayAPI bool
	// Channel is "standard" or "experimental". Default: "standard".
	Channel string
	// Out is where installation progress is written. Default: os.Stdout.
	Out io.Writer
}

// Install applies CRDs to the cluster based on detection results.
// Uses pre-rendered embedded CRD bytes when available; errors clearly if missing.
func Install(ctx context.Context, kube Kubectl, detected DetectResult, opts InstallOptions) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Channel == "" {
		opts.Channel = "standard"
	}

	if err := installGatewayAPI(ctx, kube, detected.GatewayAPI, opts); err != nil {
		return err
	}
	return installEG(ctx, kube, detected.EG, opts)
}

func installGatewayAPI(ctx context.Context, k Kubectl, info GatewayAPIInfo, opts InstallOptions) error {
	switch {
	case opts.SkipGatewayAPI:
		fmt.Fprintf(opts.Out, "  gateway-api: skipped (--skip-gateway-api-crds)\n")
		return nil
	case info.State == ProviderManaged:
		fmt.Fprintf(opts.Out, "  gateway-api: %s %s -- provider-managed (%s), skipping\n",
			info.BundleVersion, info.Channel, info.ProviderManager)
		return nil
	case info.State == SelfManaged && !opts.ForceGatewayAPI:
		fmt.Fprintf(opts.Out, "  gateway-api: %s %s -- already installed, skipping (--force-gateway-api-crds to upgrade)\n",
			info.BundleVersion, info.Channel)
		return nil
	}

	filename := "gateway-api-standard.yaml"
	if opts.Channel == "experimental" {
		filename = "gateway-api-experimental.yaml"
	}

	fmt.Fprintf(opts.Out, "  gateway-api: installing (%s)... ", opts.Channel)
	if err := applyEmbeddedCRD(ctx, k, filename); err != nil {
		return fmt.Errorf("gateway-api CRDs: %w", err)
	}
	fmt.Fprintln(opts.Out, "done")
	return nil
}

func installEG(ctx context.Context, k Kubectl, info EGInfo, opts InstallOptions) error {
	if info.State == SelfManaged {
		fmt.Fprintf(opts.Out, "  envoy-gateway: %s -- already installed, skipping\n", info.Version)
		return nil
	}
	fmt.Fprintf(opts.Out, "  envoy-gateway: installing... ")
	if err := applyEmbeddedCRD(ctx, k, "envoy-gateway.yaml"); err != nil {
		return fmt.Errorf("envoy-gateway CRDs: %w", err)
	}
	fmt.Fprintln(opts.Out, "done")
	return nil
}

func applyEmbeddedCRD(ctx context.Context, k Kubectl, filename string) error {
	crdFS := charts.CRDs()
	data, err := fs.ReadFile(crdFS, filename)
	if err != nil {
		return fmt.Errorf("embedded CRD %q not found -- run 'make generate-crds' first: %w", filename, err)
	}
	if len(data) == 0 {
		return fmt.Errorf("embedded CRD %q is empty -- run 'make generate-crds' first", filename)
	}
	return k.RunWithStdin(ctx, bytes.NewReader(data), os.Stdout, os.Stderr,
		"apply", "--server-side", "-f", "-")
}

// containsWord checks if s contains the word w as a space-separated token.
func containsWord(s, w string) bool {
	for _, token := range splitWords(s) {
		if token == w {
			return true
		}
	}
	return false
}

func splitWords(s string) []string {
	var words []string
	for _, f := range bytes.Fields([]byte(s)) {
		words = append(words, string(f))
	}
	return words
}
