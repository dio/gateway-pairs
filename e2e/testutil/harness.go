// Package helper provides shared utilities for gateway-pairs e2e tests.
package testutil

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dio/sh"
)

// Harness holds a kubectl/helm context and testing.T, providing ergonomic
// wrappers over sh.Run/sh.Output for e2e test suites.
// Embed it in your testify suite struct.
type Harness struct {
	T        *testing.T
	Ctx      context.Context
	Ktx      string // kube context name, e.g. "k3d-gw-simple-e2e"
	RepoRoot string // absolute path to repo root
}

func (h *Harness) Must(cmd string, args ...string) {
	h.T.Helper()
	if err := sh.Run(h.Ctx, cmd, args...); err != nil {
		h.T.Fatalf("%s %v: %v", cmd, args, err)
	}
}

func (h *Harness) MustKubectl(args ...string) string {
	h.T.Helper()
	a := append([]string{"--context", h.Ktx}, args...)
	out, err := sh.Output(h.Ctx, "kubectl", a...)
	if err != nil {
		h.T.Fatalf("kubectl %v: %v\n%s", args, err, out)
	}
	return out
}

func (h *Harness) Kubectl(args ...string) (string, error) {
	a := append([]string{"--context", h.Ktx}, args...)
	return sh.Output(h.Ctx, "kubectl", a...)
}

func (h *Harness) MustHelm(args ...string) {
	h.T.Helper()
	a := append([]string{"--kube-context", h.Ktx}, args...)
	if err := sh.Run(h.Ctx, "helm", a...); err != nil {
		h.T.Fatalf("helm %v: %v", args, err)
	}
}

// Apply runs kubectl apply -n ns -f - with the given manifest.
func (h *Harness) Apply(ns, manifest string) {
	h.T.Helper()
	a := []string{"--context", h.Ktx, "apply", "-n", ns, "-f", "-"}
	_, err := sh.ExecWithStdin(h.Ctx, nil, strings.NewReader(manifest),
		nil, nil, "kubectl", a...)
	if err != nil {
		h.T.Fatalf("kubectl apply in %s: %v", ns, err)
	}
}

// Eventually polls fn until it returns true or timeout is exceeded.
func (h *Harness) Eventually(fn func() bool, timeout, tick time.Duration, msg string, args ...interface{}) {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(tick)
	}
	h.T.Fatalf("condition not met within %s: %s", timeout, fmt.Sprintf(msg, args...))
}

// PortForward starts kubectl port-forward and returns a cancel func.
func (h *Harness) PortForward(ns, resource, ports string) func() {
	cmd := exec.CommandContext(h.Ctx, "kubectl", "--context", h.Ktx,
		"port-forward", "-n", ns, resource, ports)
	cmd.Start() //nolint:errcheck
	return func() {
		if cmd.Process != nil {
			cmd.Process.Kill() //nolint:errcheck
		}
	}
}

// FindGWSvc returns the name of the EG-generated gateway Service in ns.
func (h *Harness) FindGWSvc(ns string) (string, error) {
	out, err := h.Kubectl("get", "services", "-n", ns,
		"-l", "gateway.envoyproxy.io/owning-gateway-namespace="+ns,
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("no gateway service in %s: %v", ns, err)
	}
	return strings.TrimSpace(out), nil
}

// WaitNS polls until all given namespaces are gone (max 2 minutes).
func (h *Harness) WaitNS(namespaces ...string) {
	deadline := time.Now().Add(2 * time.Minute)
	for _, ns := range namespaces {
		for time.Now().Before(deadline) {
			out, _ := h.Kubectl("get", "namespace", ns)
			if !strings.Contains(out, ns) {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}

func (h *Harness) ChartPath(chart string) string {
	return filepath.Join(h.RepoRoot, "charts", chart)
}
