package flux_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/dio/gateway-pairs/e2e/integrations/flux"
	"github.com/dio/gateway-pairs/e2e/testutil"
)

const (
	clusterName  = "gw-flux-e2e"
	registryName = "gw-flux-registry"
	ktx          = "k3d-" + clusterName
	k3sImage     = "rancher/k3s:v1.32.2-k3s1"
	fluxManifest = "https://github.com/fluxcd/flux2/releases/download/v2.5.1/install.yaml"

	// chartVersion is the synthetic semver used when packaging the local chart for e2e.
	chartVersion = "0.0.0-e2e"

	// pairIndex and pairPrefix define the single pair installed by this suite.
	pairIndex  = 1
	pairPrefix = "tr"
)

func TestFluxIntegration(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to run")
	}
	suite.Run(t, new(fluxSuite))
}

type fluxSuite struct {
	suite.Suite
	h            testutil.Harness
	cancel       context.CancelFunc
	registryPort string // host-side port of the k3d local registry
}

// Delegate convenience methods so tests read like prose.
func (s *fluxSuite) Kubectl(args ...string) (string, error)  { return s.h.Kubectl(args...) }
func (s *fluxSuite) MustKubectl(args ...string) string       { return s.h.MustKubectl(args...) }
func (s *fluxSuite) Apply(ns, m string)                      { s.h.Apply(ns, m) }
func (s *fluxSuite) WaitNS(ns ...string)                     { s.h.WaitNS(ns...) }
func (s *fluxSuite) PortForward(ns, res, ports string) func() { return s.h.PortForward(ns, res, ports) }
func (s *fluxSuite) Eventually(fn func() bool, timeout, tick time.Duration, msg string, a ...interface{}) {
	s.h.Eventually(fn, timeout, tick, msg, a...)
}

func (s *fluxSuite) SetupSuite() {
	// Guard: Docker must be reachable (OrbStack must be running).
	s.checkDockerRunning()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	s.cancel = cancel

	s.h = testutil.Harness{
		T:        s.T(),
		Ctx:      ctx,
		Ktx:      ktx,
		RepoRoot: repoRoot(),
	}

	if os.Getenv("REUSE_CLUSTER") == "1" {
		s.T().Log("reusing cluster", clusterName)
		s.cleanupPair()
		return
	}

	// Delete stale cluster if any.
	exec.Command("k3d", "cluster", "delete", clusterName).Run()   //nolint:errcheck
	exec.Command("k3d", "registry", "delete", registryName).Run() //nolint:errcheck

	// Create cluster + registry in one shot.
	// --registry-create wires the registry into the cluster's containerd config
	// so source-controller can pull from gw-flux-registry:5000 inside the cluster.
	s.h.Must("k3d", "cluster", "create", clusterName,
		"--agents", "1",
		"--image", k3sImage,
		"--k3s-arg", "--disable=traefik@server:*",
		"--registry-create", registryName+":0.0.0.0:0", // port 0 = random host port
	)

	s.MustKubectl("wait",
		fmt.Sprintf("nodes/k3d-%s-server-0", clusterName),
		"--for=condition=Ready", "--timeout=120s")
	s.MustKubectl("wait",
		fmt.Sprintf("nodes/k3d-%s-agent-0", clusterName),
		"--for=condition=Ready", "--timeout=120s")

	// Resolve the random host port assigned to the local registry.
	s.registryPort = s.resolveRegistryPort()
	s.T().Logf("local OCI registry: localhost:%s -> k3d-%s:5000", s.registryPort, registryName)

	// Install Gateway API + EG CRDs (same path as existing e2e suites).
	// install-crds.sh reads KTX from the environment.
	crdScript := filepath.Join(s.h.RepoRoot, "hack", "install-crds.sh")
	cmd := exec.CommandContext(ctx, "bash", crdScript)
	cmd.Env = append(os.Environ(), "KTX="+ktx)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	s.Require().NoError(cmd.Run(), "install-crds.sh")

	// Install Flux controllers from the official static manifest.
	// This installs source-controller, helm-controller (and others -- harmless).
	// Allow 5m for the image pull on first run (ghcr.io can be slow).
	s.MustKubectl("apply", "-f", fluxManifest)
	s.MustKubectl("wait",
		"-n", "flux-system", "deploy/source-controller",
		"--for=condition=Available", "--timeout=5m")
	s.MustKubectl("wait",
		"-n", "flux-system", "deploy/helm-controller",
		"--for=condition=Available", "--timeout=5m")

	// Package + push the local eg-pair chart to the local registry.
	s.pushChart()

	// Apply Flux HelmRepository and HelmRelease for the pair.
	s.applyFluxResources()
}

func (s *fluxSuite) TearDownSuite() {
	defer s.cancel()
	if os.Getenv("KEEP_CLUSTER") == "1" {
		return
	}
	exec.Command("k3d", "cluster", "delete", clusterName).Run()   //nolint:errcheck
	exec.Command("k3d", "registry", "delete", registryName).Run() //nolint:errcheck
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (s *fluxSuite) checkDockerRunning() {
	s.T().Helper()
	out, err := exec.Command("docker", "info").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Server Version") {
		s.T().Skip("Docker daemon not reachable -- start OrbStack first")
	}
}

// resolveRegistryPort finds the host-side port mapped to container port 5000
// on the k3d-managed registry container.
//
// k3d v5.8.3 creates the registry container as "gw-flux-registry" (no k3d- prefix).
// The k3d registry list JSON always shows HostPort "0" (a known k3d bug); we read
// the actual assigned port from Docker directly.
func (s *fluxSuite) resolveRegistryPort() string {
	s.T().Helper()
	// Use docker inspect to get the actual host port (k3d registry list returns "0").
	out, err := exec.Command("docker", "port", registryName, "5000").Output()
	if err == nil {
		// "docker port" returns "0.0.0.0:32768\n" or ":::32768\n"
		parts := strings.Split(strings.TrimSpace(string(out)), ":")
		if port := parts[len(parts)-1]; port != "" && port != "0" {
			return port
		}
	}
	// Fallback: parse docker inspect
	inspect, err := exec.Command("docker", "inspect", registryName,
		"--format", "{{(index (index .NetworkSettings.Ports \"5000/tcp\") 0).HostPort}}").Output()
	s.Require().NoError(err, "docker inspect %s for port 5000", registryName)
	port := strings.TrimSpace(string(inspect))
	s.Require().NotEmpty(port, "could not resolve host port for registry %s", registryName)
	return port
}

// pushChart packages the local eg-pair chart and pushes it to the local registry.
func (s *fluxSuite) pushChart() {
	s.T().Helper()
	tmpDir := filepath.Join(os.TempDir(), "gw-flux-e2e")
	s.Require().NoError(os.MkdirAll(tmpDir, 0o755))

	chartSrc := filepath.Join(s.h.RepoRoot, "charts", "eg-pair")
	s.h.Must("helm", "package", chartSrc,
		"--version", chartVersion,
		"--destination", tmpDir,
	)

	tgz := filepath.Join(tmpDir, "eg-pair-"+chartVersion+".tgz")
	dst := fmt.Sprintf("oci://localhost:%s/dio/gateway-pairs/charts", s.registryPort)
	s.h.Must("helm", "push", tgz, dst, "--plain-http")
	s.T().Logf("pushed %s to %s", tgz, dst)
}

// applyFluxResources applies the HelmRepository and HelmRelease for the pair.
// The registry hostname inside the k3d cluster is just registryName (no k3d- prefix).
// CoreDNS NodeHosts has the entry written by k3d cluster create --registry-create.
func (s *fluxSuite) applyFluxResources() {
	s.T().Helper()
	// k3d creates the registry container as "gw-flux-registry" (no k3d- prefix).
	// CoreDNS NodeHosts resolves it inside the cluster.
	inClusterRegistry := fmt.Sprintf("oci://%s:5000/dio/gateway-pairs/charts", registryName)

	repo := flux.HelmRepositoryManifest(flux.HelmRepositoryParams{
		Name:              "eg-pair",
		Namespace:         "flux-system",
		RegistryInCluster: inClusterRegistry,
	})
	release := flux.HelmReleaseManifest(flux.HelmReleaseParams{
		Name:            fmt.Sprintf("eg-pair-%d", pairIndex),
		Namespace:       "flux-system",
		TargetNamespace: fmt.Sprintf("%s-system-%d", pairPrefix, pairIndex),
		ChartVersion:    chartVersion,
		SourceName:      "eg-pair",
		PairIndex:       pairIndex,
		NamePrefix:      pairPrefix,
	})
	s.Apply("flux-system", flux.CombinedManifest(repo, release))
}

// waitHelmRelease polls until the HelmRelease is Ready or the timeout expires.
// It fails fast if the HelmRelease transitions to a terminal failure.
func (s *fluxSuite) waitHelmRelease(name, ns string, timeout time.Duration) {
	s.T().Helper()
	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "helmrelease", name, "-n", ns,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status}/{.reason} {end}",
			"--ignore-not-found")
		if err != nil {
			return false
		}
		if strings.Contains(out, "Ready=True") {
			return true
		}
		// Fail fast on terminal install failure (no point waiting 5m).
		if strings.Contains(out, "Ready=False/InstallFailed") ||
			strings.Contains(out, "Ready=False/UpgradeFailed") {
			s.T().Logf("HelmRelease %s/%s terminal failure: %s", ns, name, out)
			s.Require().Fail("HelmRelease hit terminal failure", out)
		}
		return false
	}, timeout, 5*time.Second, "HelmRelease %s/%s not Ready", ns, name)
}

// cleanupPair is used when REUSE_CLUSTER=1 to wipe the previous pair state.
func (s *fluxSuite) cleanupPair() {
	s.T().Helper()
	releaseName := fmt.Sprintf("eg-pair-%d", pairIndex)
	systemNS := fmt.Sprintf("%s-system-%d", pairPrefix, pairIndex)
	dataNS := fmt.Sprintf("%s-dataplane-%d", pairPrefix, pairIndex)

	// Remove HelmRelease first so Flux runs helm uninstall.
	s.Kubectl("delete", "helmrelease", releaseName, "-n", "flux-system", "--ignore-not-found") //nolint:errcheck
	s.Kubectl("delete", "helmrepository", "eg-pair", "-n", "flux-system", "--ignore-not-found") //nolint:errcheck

	// Remove any leftover pair namespaces.
	for _, ns := range []string{systemNS, dataNS} {
		s.Kubectl("delete", "namespace", ns, "--ignore-not-found", "--wait=false") //nolint:errcheck
	}
	s.WaitNS(systemNS, dataNS)

	// Re-push chart and re-apply Flux resources.
	s.registryPort = s.resolveRegistryPort()
	s.pushChart()
	s.applyFluxResources()
}

// repoRoot resolves the gateway-pairs repository root via the GOWORK env var
// (set by go test in workspace mode) or by walking up to find go.work.
func repoRoot() string {
	if gw := os.Getenv("GOWORK"); gw != "" {
		return filepath.Dir(gw)
	}
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	panic("go.work not found -- run from inside the gateway-pairs workspace")
}
