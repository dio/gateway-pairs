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
//	statuses, err := c.PairList(ctx)
package gwpapi

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/dio/gateway-pairs/crd"
	"github.com/dio/gateway-pairs/internal/helm"
	"github.com/dio/gateway-pairs/internal/kube"
	"github.com/dio/gateway-pairs/names"
	"github.com/dio/gateway-pairs/pair"
)

// Options configures a Client.
type Options struct {
	// KubeContext selects the kubectl/helm context. Default: current context.
	KubeContext string
	// Kubeconfig is the path to the kubeconfig file. Default: ~/.kube/config.
	Kubeconfig string
	// Prefix is the name prefix applied to all derived resource names. Default: "tr".
	Prefix string
}

// Client is the main entry point for embedding gwp operations.
type Client struct {
	opts   Options
	kube   *kube.Client
	helm   *helm.Client
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
		Prefix:      c.opts.Prefix,
		ExtraSet:    opts.ExtraSet,
		HelmTimeout: opts.HelmTimeout,
		WaitTimeout: opts.WaitTimeout,
		Out:         opts.Out,
	})
}

// PairDelete uninstalls pair index.
func (c *Client) PairDelete(ctx context.Context, index int, out io.Writer) error {
	return pair.Delete(ctx, c.helm, c.kube, index, c.opts.Prefix, out)
}

// PairGet returns the status of a single installed pair.
func (c *Client) PairGet(ctx context.Context, index int) (*PairStatus, error) {
	return pair.Get(ctx, c.helm, c.kube, index, c.opts.Prefix)
}

// PairList returns the status of all installed pairs.
func (c *Client) PairList(ctx context.Context) ([]*PairStatus, error) {
	return pair.List(ctx, c.helm, c.kube, c.opts.Prefix)
}

// PairInfo returns the coupling fields needed to write Layer 3 manifests for pair index.
func (c *Client) PairInfo(index int) names.Pair {
	return pair.Info(c.opts.Prefix, index)
}
