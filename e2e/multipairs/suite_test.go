package multipairs_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	clusterName = "gw-pairs-e2e"
	ktx         = "k3d-" + clusterName

	EGVersion = "v1.8.0"
	k3sImage  = "rancher/k3s:v1.32.2-k3s1"

	// Gateway API CRD bundle version shipped by gateway-crds-helm v1.8.0.
	// Do NOT set this independently -- the version is determined by EG's chart,
	// not by a separate Gateway API release pin.
	gatewayAPIBundleVersion = "v1.5.1"
)

// pairsBaseSuite holds cluster lifecycle. Embed in per-scenario suites.
type pairsBaseSuite struct {
	suite.Suite
	ctx     context.Context
	cancel  context.CancelFunc
	repoRoot string
}

func (s *pairsBaseSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 20*time.Minute)

	_, file, _, _ := runtime.Caller(0)
	s.repoRoot = filepath.Join(filepath.Dir(file), "..")

	keep := os.Getenv("KEEP_CLUSTER") == "1"

	if os.Getenv("REUSE_CLUSTER") != "1" {
		s.T().Log("creating k3d cluster", clusterName)
		exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck -- ignore if absent
		s.mustRun(
			"k3d", "cluster", "create", clusterName,
			"--agents", "1",
			"--image", k3sImage,
			"--k3s-arg", "--disable=traefik@server:*",
			"--k3s-arg", "--kubelet-arg=allowed-unsafe-sysctls=net.ipv4.ip_unprivileged_port_start@server:*",
		)
		s.T().Log("k3d cluster ready")
	} else {
		s.T().Log("reusing existing cluster", clusterName)
		// Uninstall previous pair releases.
		for i := 1; i <= 3; i++ {
			n := namesFor(i)
			release := fmt.Sprintf("eg-pair-%d", i)
			exec.Command("helm", "--kube-context", ktx, //nolint:errcheck
				"uninstall", release, "--namespace", n.SystemNS,
				"--ignore-not-found",
			).Run()
		}
		// Explicitly delete all pair namespaces. The release namespace is created
		// by --create-namespace and is NOT tracked by the Helm release, so
		// helm uninstall never removes it. Polling for its termination would block
		// indefinitely. Delete all three namespaces for each pair unconditionally.
		for i := 1; i <= 3; i++ {
			n := namesFor(i)
			for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
				exec.Command("kubectl", "--context", ktx, //nolint:errcheck
					"delete", "namespace", ns, "--ignore-not-found", "--wait=false",
				).Run()
			}
		}
		// Wait for all namespaces to terminate.
		deadline := time.Now().Add(2 * time.Minute)
		for _, i := range []int{1, 2, 3} {
			n := namesFor(i)
			for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
				ns := ns
				for time.Now().Before(deadline) {
					out, err := s.kubectl("get", "namespace", ns)
					if err != nil || !strings.Contains(out, ns) {
						break
					}
					time.Sleep(2 * time.Second)
				}
			}
		}
	}

	if !keep {
		s.T().Cleanup(func() {
			if !s.T().Failed() || os.Getenv("KEEP_CLUSTER_ON_FAILURE") != "1" {
				exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck
			}
		})
	}

	s.waitNodeReady()
}

func (s *pairsBaseSuite) TearDownSuite() {
	s.cancel()
}

// ── cluster helpers ───────────────────────────────────────────────────────────

func (s *pairsBaseSuite) mustRun(name string, args ...string) {
	s.T().Helper()
	out, err := s.run(name, args...)
	require.NoError(s.T(), err, "command failed: %s %s\n%s", name, strings.Join(args, " "), out)
}

func (s *pairsBaseSuite) run(name string, args ...string) (string, error) {
	cmd := exec.CommandContext(s.ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *pairsBaseSuite) kubectl(args ...string) (string, error) {
	return s.run("kubectl", append([]string{"--context", ktx}, args...)...)
}

func (s *pairsBaseSuite) mustKubectl(args ...string) string {
	s.T().Helper()
	out, err := s.kubectl(args...)
	require.NoError(s.T(), err, "kubectl %s failed:\n%s", strings.Join(args, " "), out)
	return out
}

func (s *pairsBaseSuite) helm(args ...string) (string, error) {
	return s.run("helm", append([]string{"--kube-context", ktx}, args...)...)
}

func (s *pairsBaseSuite) mustHelm(args ...string) string {
	s.T().Helper()
	out, err := s.helm(args...)
	require.NoError(s.T(), err, "helm %s failed:\n%s", strings.Join(args, " "), out)
	return out
}

func (s *pairsBaseSuite) waitNodeReady() {
	s.T().Helper()
	s.mustKubectl("wait", fmt.Sprintf("nodes/k3d-%s-server-0", clusterName),
		"--for=condition=Ready", "--timeout=120s")
	// Agent node -- present when --agents 1 is used.
	s.mustKubectl("wait", fmt.Sprintf("nodes/k3d-%s-agent-0", clusterName),
		"--for=condition=Ready", "--timeout=120s")
}

func (s *pairsBaseSuite) chartPath(chart string) string {
	return filepath.Join(s.repoRoot, "charts", chart)
}

// TestSuiteBootstrap is a dummy entry point for go test to discover the file.
func TestSuiteBootstrap(t *testing.T) {
	t.Log("gateway-pairs e2e suite bootstrap ok")
}
