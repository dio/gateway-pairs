// Package helm provides a thin exec wrapper around the helm binary, using dio/sh.
// It is internal -- callers outside this module use the pair and crd packages.
package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dio/sh"
)

// Runner is the interface pair depends on. *Client satisfies it; tests inject fakes.
type Runner interface {
	Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error
	Output(ctx context.Context, args ...string) (string, error)
	List(ctx context.Context, filterRegex string) ([]Release, error)
}

// Client holds connection parameters for helm invocations.
type Client struct {
	KubeContext string
	Kubeconfig  string
}

func (c *Client) baseArgs(extra ...string) []string {
	var a []string
	if c.KubeContext != "" {
		a = append(a, "--kube-context", c.KubeContext)
	}
	if c.Kubeconfig != "" {
		a = append(a, "--kubeconfig", c.Kubeconfig)
	}
	return append(a, extra...)
}

// Output runs helm and returns trimmed stdout.
func (c *Client) Output(ctx context.Context, args ...string) (string, error) {
	out, err := sh.Output(ctx, "helm", c.baseArgs(args...)...)
	return strings.TrimSpace(out), err
}

// Run runs helm forwarding stdout and stderr to the provided writers.
func (c *Client) Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	_, err := sh.Exec(ctx, nil, stdout, stderr, "helm", c.baseArgs(args...)...)
	return err
}

// Template runs helm template and writes rendered manifests to w.
func (c *Client) Template(ctx context.Context, w io.Writer, helmArgs ...string) error {
	args := append([]string{"template"}, helmArgs...)
	var stderr bytes.Buffer
	if _, err := sh.Exec(ctx, nil, w, &stderr, "helm", c.baseArgs(args...)...); err != nil {
		return fmt.Errorf("helm template: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Release is a summary of a Helm release from helm list.
type Release struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Status     string `json:"status"`
	AppVersion string `json:"app_version"`
}

// List returns Helm releases matching filterRegex across all namespaces.
func (c *Client) List(ctx context.Context, filterRegex string) ([]Release, error) {
	args := []string{"list", "--all-namespaces", "--output", "json"}
	if filterRegex != "" {
		args = append(args, "--filter", filterRegex)
	}
	out, err := c.Output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("helm list: %w", err)
	}
	if out == "" || out == "null" {
		return nil, nil
	}
	var releases []Release
	if err := json.Unmarshal([]byte(out), &releases); err != nil {
		return nil, fmt.Errorf("helm list parse: %w", err)
	}
	return releases, nil
}
