package simplegwp_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/dio/gateway-pairs/e2e/testutil"
)

const (
	clusterName = "gw-gwp-e2e"
	ktx         = "k3d-" + clusterName
	k3sImage    = "rancher/k3s:v1.32.2-k3s1"
)

func TestGWPSingle(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to run")
	}
	suite.Run(t, new(gwpSuite))
}

type gwpSuite struct {
	suite.Suite
	h      testutil.Harness
	gwp    string // path to gwp binary
	cancel context.CancelFunc
}

// Delegate convenience methods so tests call s.Must/s.Apply etc.
func (s *gwpSuite) Must(cmd string, args ...string)  { s.h.Must(cmd, args...) }
func (s *gwpSuite) MustKubectl(args ...string) string { return s.h.MustKubectl(args...) }
func (s *gwpSuite) Kubectl(args ...string) (string, error) { return s.h.Kubectl(args...) }
func (s *gwpSuite) Apply(ns, m string)               { s.h.Apply(ns, m) }
func (s *gwpSuite) Eventually(fn func() bool, timeout, tick time.Duration, msg string, a ...interface{}) {
	s.h.Eventually(fn, timeout, tick, msg, a...)
}
func (s *gwpSuite) PortForward(ns, res, ports string) func() { return s.h.PortForward(ns, res, ports) }
func (s *gwpSuite) FindGWSvc(ns string) (string, error)      { return s.h.FindGWSvc(ns) }
func (s *gwpSuite) WaitNS(ns ...string)                      { s.h.WaitNS(ns...) }

// MustGWP runs gwp with the given args, forwarding output and failing on error.
func (s *gwpSuite) MustGWP(args ...string) string {
	s.T().Helper()
	all := append([]string{"--context", ktx}, args...)
	cmd := exec.CommandContext(s.h.Ctx, s.gwp, all...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.T().Errorf("gwp %v: %v\n%s", args, err, string(out))
		s.T().FailNow()
	}
	return string(out)
}

// GWP runs gwp and returns (output, error).
func (s *gwpSuite) GWP(args ...string) (string, error) {
	all := append([]string{"--context", ktx}, args...)
	cmd := exec.CommandContext(s.h.Ctx, s.gwp, all...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *gwpSuite) SetupSuite() {
	var ctx context.Context
	ctx, s.cancel = context.WithTimeout(context.Background(), 20*time.Minute)

	// Resolve repo root from GOWORK (set by go workspace) or via runtime.Caller.
	repoRoot := repoRootFromGoWork()

	s.h = testutil.Harness{
		T:        s.T(),
		Ctx:      ctx,
		Ktx:      ktx,
		RepoRoot: repoRoot,
	}

	// Locate the gwp binary. GWP_BIN is set by the Makefile (prereq: build).
	// Fall back to bin/gwp relative to repo root for direct go test runs.
	s.gwp = os.Getenv("GWP_BIN")
	if s.gwp == "" {
		s.gwp = filepath.Join(repoRoot, "bin", "gwp")
	}
	if _, err := os.Stat(s.gwp); err != nil {
		s.T().Fatalf("gwp binary not found at %s -- run 'make build' first", s.gwp)
	}
	s.T().Logf("using gwp binary: %s", s.gwp)

	if os.Getenv("REUSE_CLUSTER") == "1" {
		s.T().Log("reusing cluster", clusterName)
		n := pairNames(1)
		s.Kubectl("delete", "gateway", "--all", "-n", n.DataplaneNS, "--ignore-not-found")   //nolint:errcheck
		// Use gwp to tear down if the binary is available.
		s.GWP("pair", "delete", "1") //nolint:errcheck
		s.WaitNS(n.SystemNS, n.DataplaneNS)
		return
	}

	exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck
	s.Must("k3d", "cluster", "create", clusterName,
		"--agents", "0",
		"--image", k3sImage,
		"--k3s-arg", "--disable=traefik@server:*",
	)
	s.MustKubectl("wait",
		fmt.Sprintf("nodes/k3d-%s-server-0", clusterName),
		"--for=condition=Ready", "--timeout=120s")
}

func (s *gwpSuite) TearDownSuite() {
	defer s.cancel()
	if os.Getenv("KEEP_CLUSTER") == "1" {
		return
	}
	exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck
}
