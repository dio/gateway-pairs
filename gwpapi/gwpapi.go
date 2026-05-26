// Package gwpapi is the public embedding API for gateway-pairs.
//
// It provides a single Client type that exposes all gwp operations as Go
// function calls -- suitable for tools or operators that want to embed
// gateway-pairs lifecycle management without shelling out to the gwp binary.
//
// Example:
//
//	c := gwpapi.New(gwpapi.Options{KubeContext: "k3d-mycluster", Prefix: "tr"})
//	if err := c.CRDInstall(ctx, gwpapi.CRDInstallOptions{}); err != nil {
//	    log.Fatal(err)
//	}
//	if err := c.PairInstall(ctx, 1, gwpapi.PairInstallOptions{}); err != nil {
//	    log.Fatal(err)
//	}
//	if ok, _ := c.Preflight(ctx, gwpapi.PreflightOptions{PairIndex: 1}); !ok { ... }
//	if r, _ := c.PairVerify(ctx, 1, gwpapi.PairVerifyOptions{}); !r.Healthy { ... }
//	list, _ := c.ChartsList()
package gwpapi

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/dio/gateway-pairs/crd"
	"github.com/dio/gateway-pairs/gwpcharts"
	"github.com/dio/gateway-pairs/internal/helm"
	"github.com/dio/gateway-pairs/internal/kube"
	"github.com/dio/gateway-pairs/names"
	"github.com/dio/gateway-pairs/pair"
	"github.com/dio/gateway-pairs/preflight"
)

// Options configures the gwpapi Client.
type Options struct {
	// KubeContext is the kubectl context name. Default: current context.
	KubeContext string
	// Kubeconfig is the path to the kubeconfig file. Default: ~/.kube/config.
	Kubeconfig string
	// Prefix is the name prefix applied to all derived resource names. Default: "tr".
	Prefix string
	// Suffix is an optional string override for the numeric index in all resource names
	// (e.g. "prod" → tr-system-prod, GatewayClass tr-prod).
	Suffix string
	// UseSuffix must be true when Suffix should be used (even when Suffix is "").
	// Distinguishes "no --suffix flag" (use numeric index) from "--no-suffix" (no suffix at all).
	UseSuffix bool
}

// Client is the main entry point for embedding gwp operations.
type Client struct {
	opts Options
	kube *kube.Client
	helm *helm.Client
}

// New creates a Client with the given options.
func New(opts Options) *Client {
	if opts.Prefix == "" {
		opts.Prefix = "tr"
	}
	return &Client{
		opts: opts,
		kube: &kube.Client{Context: opts.KubeContext, Kubeconfig: opts.Kubeconfig},
		helm: &helm.Client{KubeContext: opts.KubeContext, Kubeconfig: opts.Kubeconfig},
	}
}

// ── CRD operations ────────────────────────────────────────────────────────────

// CRDDetectResult is re-exported for callers that import only the api package.
type CRDDetectResult = crd.DetectResult

// CRDDetect inspects the cluster and returns the current CRD installation state.
func (c *Client) CRDDetect(ctx context.Context) (CRDDetectResult, error) {
	return crd.Detect(ctx, c.kube)
}

// CRDInstallOptions controls CRD installation.
type CRDInstallOptions struct {
	// SkipGatewayAPI skips Gateway API CRDs regardless of detection result.
	SkipGatewayAPI bool
	// ForceGatewayAPI installs Gateway API CRDs even when already present.
	ForceGatewayAPI bool
	// Channel is "standard" or "experimental". Default: "standard".
	Channel string
	// Out receives progress output. Default: os.Stdout.
	Out io.Writer
}

// CRDInstall detects and installs the Gateway API + EG CRDs.
func (c *Client) CRDInstall(ctx context.Context, opts CRDInstallOptions) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	detected, err := crd.Detect(ctx, c.kube)
	if err != nil {
		return err
	}
	return crd.Install(ctx, c.kube, detected, crd.InstallOptions{
		SkipGatewayAPI:  opts.SkipGatewayAPI,
		ForceGatewayAPI: opts.ForceGatewayAPI,
		Channel:         opts.Channel,
		Out:             opts.Out,
	})
}

// ── Pair operations ───────────────────────────────────────────────────────────

// PairStatus is re-exported for callers that import only the api package.
type PairStatus = pair.Status

// PairInstallOptions controls pair installation.
type PairInstallOptions struct {
	// ExtraSet are additional --set flags passed to helm.
	ExtraSet []string
	// RatelimitDisabled disables the rate-limit deployment.
	// When true, sets replicas=0 via --set.
	RatelimitDisabled bool
	// RatelimitImage overrides the rate-limit container image.
	// Format: "repository:tag" or "repository"
	// Example: "gcr.io/myproject/ratelimit:v1.5"
	RatelimitImage string
	// HelmTimeout is the helm upgrade --install timeout. Default: 5m.
	HelmTimeout time.Duration
	// WaitTimeout is the readiness polling timeout. Default: 3m.
	WaitTimeout time.Duration
	// Out receives progress output. Default: os.Stdout.
	Out io.Writer
}

// PairInstall installs or upgrades pair index.
func (c *Client) PairInstall(ctx context.Context, index int, opts PairInstallOptions) error {
	return pair.Install(ctx, c.helm, c.kube, index, pair.InstallOptions{
		Prefix:            c.opts.Prefix,
		Suffix:            c.opts.Suffix,
		UseSuffix:         c.opts.UseSuffix,
		ExtraSet:          opts.ExtraSet,
		RatelimitDisabled: opts.RatelimitDisabled,
		RatelimitImage:    opts.RatelimitImage,
		HelmTimeout:       opts.HelmTimeout,
		WaitTimeout:       opts.WaitTimeout,
		Out:               opts.Out,
	})
}

// PairDelete uninstalls pair index using the correct teardown sequence.
func (c *Client) PairDelete(ctx context.Context, index int, out io.Writer) error {
	return pair.Delete(ctx, c.helm, c.kube, index, c.opts.Prefix, c.opts.Suffix, c.opts.UseSuffix, out)
}

// PairGet returns the status of a single installed pair.
func (c *Client) PairGet(ctx context.Context, index int) (*PairStatus, error) {
	return pair.Get(ctx, c.helm, c.kube, index, c.opts.Prefix, c.opts.Suffix, c.opts.UseSuffix)
}

// PairList returns the status of all installed pairs.
func (c *Client) PairList(ctx context.Context) ([]*PairStatus, error) {
	return pair.List(ctx, c.helm, c.kube, c.opts.Prefix)
}

// PairInfo returns the coupling fields needed to write Layer 3 manifests for pair index.
func (c *Client) PairInfo(index int) names.Pair {
	return pair.Info(c.opts.Prefix, c.opts.Suffix, c.opts.UseSuffix, index)
}

// PairVerifyResult is re-exported for callers that import only the api package.
type PairVerifyResult = pair.VerifyResult

// PairVerifyOptions controls PairVerify behaviour.
type PairVerifyOptions struct {
	// Diagnose, when true, appends diagnostic output on failure.
	Diagnose bool
	// Out receives progress and diagnostic output. Default: io.Discard.
	Out io.Writer
}

// PairVerify re-runs post-install health checks for pair index without reinstalling.
// Returns a VerifyResult; result.Healthy is true only when all checks pass.
func (c *Client) PairVerify(ctx context.Context, index int, opts PairVerifyOptions) (*PairVerifyResult, error) {
	return pair.Verify(ctx, c.kube, index, pair.VerifyOptions{
		Prefix:    c.opts.Prefix,
		Suffix:    c.opts.Suffix,
		UseSuffix: c.opts.UseSuffix,
		Diagnose:  opts.Diagnose,
		Out:       opts.Out,
	})
}

// ── Preflight ─────────────────────────────────────────────────────────────────

// PreflightResult is re-exported for callers that import only the api package.
type PreflightResult = preflight.Result

// PreflightCheck is re-exported for callers that import only the api package.
type PreflightCheck = preflight.Check

// PreflightOptions controls which preflight checks to run.
type PreflightOptions struct {
	// PairIndex, when > 0, enables pair-specific conflict checks (6-8).
	PairIndex int
	// UnsafeContext suppresses the hard block on non-k3d contexts (still warns).
	UnsafeContext bool
	// Out receives progress output. Default: io.Discard.
	Out io.Writer
}

// Preflight runs pre-install cluster readiness checks.
// Returns (result, error). The result summarises all check outcomes; error is
// non-nil only on internal failures (not on check failures -- check result.Failures).
func (c *Client) Preflight(ctx context.Context, opts PreflightOptions) (*PreflightResult, error) {
	return preflight.Run(ctx, c.kube, preflight.Options{
		PairIndex:     opts.PairIndex,
		Prefix:        c.opts.Prefix,
		Suffix:        c.opts.Suffix,
		UseSuffix:     c.opts.UseSuffix,
		UnsafeContext: opts.UnsafeContext,
		Out:           opts.Out,
	})
}

// ── Charts ────────────────────────────────────────────────────────────────────

// ChartsListResult is re-exported for callers that import only the api package.
type ChartsListResult = gwpcharts.ListResult

// ChartsList returns metadata about all embedded charts and CRD bundles.
// No cluster access required.
func (c *Client) ChartsList() (*ChartsListResult, error) {
	return gwpcharts.List()
}

// ChartsExport extracts the embedded eg-pair chart tree to dir.
// No cluster access required.
func (c *Client) ChartsExport(dir string) error {
	return gwpcharts.Export(dir)
}

// ChartsShowValues returns the default values.yaml for the named chart.
// Currently only "eg-pair" is supported.
// No cluster access required.
func (c *Client) ChartsShowValues(name string) (string, error) {
	return gwpcharts.ShowValues(name)
}
