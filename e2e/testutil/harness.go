// Package testutil provides shared utilities for gateway-pairs e2e tests.
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
		h.T.Errorf("helm %v: %v", args, err)
		h.T.FailNow()
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
	if !h.eventuallyBool(fn, timeout, tick) {
		h.T.Fatalf("condition not met within %s: %s", timeout, fmt.Sprintf(msg, args...))
	}
}

// eventuallyBool polls fn until true or timeout, returning success. Non-fatal.
func (h *Harness) eventuallyBool(fn func() bool, timeout, tick time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(tick)
	}
	return false
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

// QuitProxyPods sends POST /quitquitquit to every running Envoy proxy pod
// in ns via kubectl port-forward + HTTP POST.
//
// WHY THIS EXISTS
//
// EG sets terminationGracePeriodSeconds = drainTimeout + 300s (default 360s).
// Even with zero live connections the pod sits Terminating for the full
// grace period, blocking namespace deletion. The Envoy admin /quitquitquit
// endpoint triggers an immediate graceful shutdown: the process exits as
// soon as the connection drain completes -- which is instant in a test cluster.
// This collapses the 360s wait to <1s.
//
// WHY PORT-FORWARD (NOT EXEC)
//
// EG uses distroless images (no shell, no wget). kubectl exec is therefore
// useless. The admin API listens on 127.0.0.1:19000 (localhost only by
// design -- see EG threat model). Port-forwarding from outside the cluster
// is the only access path.
//
// WHEN TO CALL
//
// Before deleting the Gateway. Port-forward to a Terminating pod is
// unreliable because the kubelet may refuse new connections once SIGTERM is
// delivered. Call this while pods are still in Running phase.
//
// baseLocalPort is the first host port to use (e.g. 19100). Each pod gets
// baseLocalPort+i to avoid conflicts.
//
// Best-effort: port-forward or curl failures are logged and the pod is
// force-deleted as a fallback so the test can continue.
func (h *Harness) QuitProxyPods(ns string, baseLocalPort int) {
	h.T.Helper()
	pods, err := h.Kubectl("get", "pods", "-n", ns,
		"-l", "app.kubernetes.io/managed-by=envoy-gateway",
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
		"--ignore-not-found")
	if err != nil || strings.TrimSpace(pods) == "" {
		return
	}
	for i, pod := range strings.Fields(pods) {
		localPort := baseLocalPort + i
		h.T.Logf("sending /quitquitquit to %s/%s via :%d", ns, pod, localPort)

		fwd := exec.CommandContext(h.Ctx, "kubectl", "--context", h.Ktx,
			"port-forward", "-n", ns, "pod/"+pod,
			fmt.Sprintf("%d:19000", localPort))
		if startErr := fwd.Start(); startErr != nil {
			h.T.Logf("port-forward start failed for %s: %v (force-deleting)", pod, startErr)
			h.Kubectl("delete", "pod", pod, "-n", ns, //nolint:errcheck
				"--grace-period=0", "--force", "--ignore-not-found")
			continue
		}

		// Use Eventually to poll until the port-forward tunnel is ready,
		// then POST /quitquitquit. Best-effort -- failure falls back to force-delete.
		url := fmt.Sprintf("http://127.0.0.1:%d/quitquitquit", localPort)
		var lastOut string
		ok := false
		h.eventuallyBool(func() bool {
			out, err := sh.Output(h.Ctx, "curl",
				"-s", "-X", "POST",
				"--connect-timeout", "1",
				"--max-time", "2",
				url)
			lastOut = out
			ok = err == nil
			return ok
		}, 5*time.Second, 200*time.Millisecond)
		fwd.Process.Kill() //nolint:errcheck
		if !ok {
			h.T.Logf("quitquitquit failed for %s (%s) -- force-deleting", pod, strings.TrimSpace(lastOut))
			h.Kubectl("delete", "pod", pod, "-n", ns, //nolint:errcheck
				"--grace-period=0", "--force", "--ignore-not-found")
		} else {
			h.T.Logf("quitquitquit sent to %s: %s", pod, strings.TrimSpace(lastOut))
		}
	}

	// Wait for all proxy pods to exit. After quitquitquit the Envoy process
	// begins its drain; the shutdown-manager sidecar also needs to exit.
	// We must wait here before namespace delete -- a pod still Terminating
	// blocks namespace termination indefinitely.
	h.eventuallyBool(func() bool {
		out, err := h.Kubectl("get", "pods", "-n", ns,
			"-l", "app.kubernetes.io/managed-by=envoy-gateway",
			"-o", "jsonpath={.items}",
			"--ignore-not-found")
		return err == nil && strings.TrimSpace(out) == "[]"
	}, 30*time.Second, 1*time.Second)
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
