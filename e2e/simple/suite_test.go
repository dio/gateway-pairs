package simple_test

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

	"github.com/dio/sh"
	"github.com/stretchr/testify/suite"

	"github.com/dio/gateway-pairs/e2e/helper"
)

const (
	clusterName = "gw-simple-e2e"
	ktx         = "k3d-" + clusterName
	k3sImage    = "rancher/k3s:v1.32.2-k3s1"
)

func TestSimplePair(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to run")
	}
	suite.Run(t, new(simpleSuite))
}

type simpleSuite struct {
	suite.Suite
	ctx      context.Context
	cancel   context.CancelFunc
	repoRoot string
}

func (s *simpleSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 10*time.Minute)
	_, file, _, _ := runtime.Caller(0)
	s.repoRoot = filepath.Join(filepath.Dir(file), "../..")

	if os.Getenv("REUSE_CLUSTER") == "1" {
		s.T().Log("reusing cluster", clusterName)
		n := namesFor(1)
		sh.Run(s.ctx, "kubectl", "--context", ktx, //nolint:errcheck
			"delete", "gateway", "--all", "-n", n.DataplaneNS, "--ignore-not-found")
		sh.Run(s.ctx, "helm", "--kube-context", ktx, //nolint:errcheck
			"uninstall", "eg-pair-1", "-n", n.SystemNS, "--ignore-not-found")
		for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
			sh.Run(s.ctx, "kubectl", "--context", ktx, //nolint:errcheck
				"delete", "namespace", ns, "--ignore-not-found", "--wait=false")
		}
		s.waitNS(n.SystemNS, n.DataplaneNS)
		return
	}

	exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck
	s.must("k3d", "cluster", "create", clusterName,
		"--agents", "0",
		"--image", k3sImage,
		"--k3s-arg", "--disable=traefik@server:*",
	)
	s.mustKubectl("wait",
		fmt.Sprintf("nodes/k3d-%s-server-0", clusterName),
		"--for=condition=Ready", "--timeout=120s")
}

func (s *simpleSuite) TearDownSuite() {
	defer s.cancel()
	if os.Getenv("KEEP_CLUSTER") == "1" {
		return
	}
	exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *simpleSuite) must(cmd string, args ...string) {
	s.T().Helper()
	s.Require().NoError(sh.Run(s.ctx, cmd, args...))
}

func (s *simpleSuite) mustKubectl(args ...string) string {
	s.T().Helper()
	a := append([]string{"--context", ktx}, args...)
	out, err := sh.Output(s.ctx, "kubectl", a...)
	s.Require().NoError(err, "kubectl %v", args)
	return out
}

func (s *simpleSuite) kubectl(args ...string) (string, error) {
	a := append([]string{"--context", ktx}, args...)
	return sh.Output(s.ctx, "kubectl", a...)
}

func (s *simpleSuite) mustHelm(args ...string) {
	s.T().Helper()
	a := append([]string{"--kube-context", ktx}, args...)
	s.Require().NoError(sh.Run(s.ctx, "helm", a...))
}

func (s *simpleSuite) apply(ns, manifest string) {
	s.T().Helper()
	a := []string{"--context", ktx, "apply", "-n", ns, "-f", "-"}
	_, err := sh.ExecWithStdin(s.ctx, nil, strings.NewReader(manifest),
		nil, nil, "kubectl", a...)
	s.Require().NoError(err, "kubectl apply in %s", ns)
}

func (s *simpleSuite) eventually(fn func() bool, timeout, tick time.Duration, msg string, args ...interface{}) {
	s.T().Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(tick)
	}
	s.Require().Fail("condition not met within timeout", fmt.Sprintf(msg, args...))
}

func (s *simpleSuite) portForward(ns, resource, ports string) func() {
	cmd := exec.CommandContext(s.ctx, "kubectl", "--context", ktx,
		"port-forward", "-n", ns, resource, ports)
	cmd.Start() //nolint:errcheck
	return func() {
		if cmd.Process != nil {
			cmd.Process.Kill() //nolint:errcheck
		}
	}
}

func (s *simpleSuite) findGWSvc(ns string) string {
	s.T().Helper()
	out, err := s.kubectl("get", "services", "-n", ns,
		"-l", "gateway.envoyproxy.io/owning-gateway-namespace="+ns,
		"-o", "jsonpath={.items[0].metadata.name}")
	s.Require().NoError(err, "gateway service not found in %s", ns)
	svc := strings.TrimSpace(out)
	s.Require().NotEmpty(svc, "gateway service name empty in %s", ns)
	return svc
}

func (s *simpleSuite) waitNS(namespaces ...string) {
	deadline := time.Now().Add(2 * time.Minute)
	for _, ns := range namespaces {
		for time.Now().Before(deadline) {
			out, _ := s.kubectl("get", "namespace", ns)
			if !strings.Contains(out, ns) {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}

func (s *simpleSuite) chartPath(chart string) string {
	return filepath.Join(s.repoRoot, "charts", chart)
}

// Re-export helper functions so tests can call them without package prefix.
var (
	testEnvoyProxy = helper.TestEnvoyProxyManifest
	testGateway    = helper.TestGatewayManifest
	echoDeployment = helper.EchoDeploymentManifest
	echoService    = helper.EchoServiceManifest
	httpRoute      = helper.HTTPRouteManifest
)
