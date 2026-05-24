package simple_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/dio/gateway-pairs/e2e/testutil"
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
	h      testutil.Harness
	cancel context.CancelFunc
}

// Delegate convenience methods so tests call s.Must/s.Apply etc.
func (s *simpleSuite) Must(cmd string, args ...string) { s.h.Must(cmd, args...) }
func (s *simpleSuite) MustKubectl(args ...string) string { return s.h.MustKubectl(args...) }
func (s *simpleSuite) Kubectl(args ...string) (string, error) { return s.h.Kubectl(args...) }
func (s *simpleSuite) MustHelm(args ...string)  { s.h.MustHelm(args...) }
func (s *simpleSuite) Apply(ns, m string)        { s.h.Apply(ns, m) }
func (s *simpleSuite) Eventually(fn func() bool, timeout, tick time.Duration, msg string, a ...interface{}) {
	s.h.Eventually(fn, timeout, tick, msg, a...)
}
func (s *simpleSuite) PortForward(ns, res, ports string) func() { return s.h.PortForward(ns, res, ports) }
func (s *simpleSuite) FindGWSvc(ns string) (string, error)      { return s.h.FindGWSvc(ns) }
func (s *simpleSuite) WaitNS(ns ...string)                       { s.h.WaitNS(ns...) }
func (s *simpleSuite) ChartPath(chart string) string             { return s.h.ChartPath(chart) }

func (s *simpleSuite) SetupSuite() {
	var ctx context.Context
	ctx, s.cancel = context.WithTimeout(context.Background(), 10*time.Minute)

	_, file, _, _ := runtime.Caller(0)
	s.h = testutil.Harness{
		T:        s.T(),
		Ctx:      ctx,
		Ktx:      ktx,
		RepoRoot: filepath.Join(filepath.Dir(file), "../.."),
	}

	if os.Getenv("REUSE_CLUSTER") == "1" {
		s.T().Log("reusing cluster", clusterName)
		n := namesFor(1)
		s.Kubectl("delete", "gateway", "--all", "-n", n.DataplaneNS, "--ignore-not-found")   //nolint:errcheck
		s.MustHelm("uninstall", "eg-pair-1", "-n", n.SystemNS, "--ignore-not-found")
		for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
			s.Kubectl("delete", "namespace", ns, "--ignore-not-found", "--wait=false") //nolint:errcheck
		}
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

func (s *simpleSuite) TearDownSuite() {
	defer s.cancel()
	if os.Getenv("KEEP_CLUSTER") == "1" {
		return
	}
	exec.Command("k3d", "cluster", "delete", clusterName).Run() //nolint:errcheck
}
