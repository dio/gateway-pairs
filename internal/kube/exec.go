// Package kube provides a thin exec wrapper around kubectl, using dio/sh.
// It is internal -- callers outside this module use the pair and crd packages.
package kube

import (
	"context"
	"io"
	"strings"

	"github.com/dio/sh"
)

// Outputter is the interface crd and pair packages depend on.
// *Client satisfies it; tests can inject fakes.
type Outputter interface {
	Output(ctx context.Context, args ...string) (string, error)
	Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error
	RunWithStdin(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error
}

// Client holds the connection parameters for kubectl invocations.
type Client struct {
	Context    string // --context value; empty means current-context
	Kubeconfig string // --kubeconfig path; empty means default
}

func (c *Client) baseArgs(extra ...string) []string {
	var a []string
	if c.Context != "" {
		a = append(a, "--context", c.Context)
	}
	if c.Kubeconfig != "" {
		a = append(a, "--kubeconfig", c.Kubeconfig)
	}
	return append(a, extra...)
}

// Output runs kubectl and returns trimmed stdout.
// stderr is forwarded to os.Stderr by sh.
func (c *Client) Output(ctx context.Context, args ...string) (string, error) {
	out, err := sh.Output(ctx, "kubectl", c.baseArgs(args...)...)
	return strings.TrimSpace(out), err
}

// Run runs kubectl, forwarding stdout and stderr to the provided writers.
func (c *Client) Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	_, err := sh.Exec(ctx, nil, stdout, stderr, "kubectl", c.baseArgs(args...)...)
	return err
}

// RunWithStdin runs kubectl with the given stdin, forwarding stdout/stderr.
func (c *Client) RunWithStdin(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	_, err := sh.ExecWithStdin(ctx, nil, stdin, stdout, stderr, "kubectl", c.baseArgs(args...)...)
	return err
}
