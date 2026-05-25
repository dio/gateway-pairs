package multipairs_test

// RUN_E2E=1 go test -v -count=1 -run TestGatewayPairs ./multipairs/...
// Override namespace prefix: PAIR_PREFIX=tr go test ...
// Override pair count:       PAIR_COUNT=5 go test ...

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/dio/gateway-pairs/e2e/testutil"
)

// deleteIdx is the pair used for the delete/reinstall cycle.
// Always valid because PAIR_COUNT >= 2.
const deleteIdx = 2

type gatewayPairsSuite struct {
	pairsBaseSuite
}

func TestGatewayPairs(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to run")
	}
	suite.Run(t, new(gatewayPairsSuite))
}

// ── install ───────────────────────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test01_InstallCRDs() {
	s.T().Log("installing CRDs via hack/install-crds.sh")
	script := filepath.Join(s.repoRoot, "hack", "install-crds.sh")
	cmd := exec.CommandContext(s.ctx, "bash", script)
	cmd.Env = append(cmd.Environ(),
		"KTX="+ktx,
		"EG_VERSION="+EGVersion,
		"CHANNEL=standard",
	)
	out, err := cmd.CombinedOutput()
	s.T().Logf("install-crds.sh:\n%s", string(out))
	s.Require().NoError(err, "hack/install-crds.sh failed")
	s.verifyGatewayAPICRDs()
	s.verifyEGCRDs()
}

func (s *gatewayPairsSuite) Test02_InstallAllPairs() {
	for _, i := range pairIndices() {
		s.installPair(i)
	}
}

// ── isolation + correctness ───────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test05_VerifyIsolation() {
	for _, i := range pairIndices() {
		n := namesFor(i)
		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployment", "envoy-gateway",
				"-n", n.SystemNS, "-o", "jsonpath={.status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 30*time.Second, 3*time.Second,
			"controller not Available in %s", n.SystemNS)
	}
}

// Test05b_VerifyAllPairs runs gwp pair verify for every pair.
// Executed after the pairs are installed; confirms the CLI health checks
// agree with kubectl-level observations before proceeding to traffic tests.
func (s *gatewayPairsSuite) Test05b_VerifyAllPairs() {
	for _, i := range pairIndices() {
		n := namesFor(i)
		s.T().Logf("verifying pair %d via gwp pair verify", i)

		out, err := s.gwp("--prefix", n.prefix, "pair", "verify", fmt.Sprintf("%d", i))
		s.T().Logf("gwp pair verify %d:\n%s", i, out)
		s.Require().NoError(err, "gwp pair verify %d should exit 0", i)
		s.Contains(out, "healthy", "pair verify %d should report healthy", i)
	}
}

func (s *gatewayPairsSuite) Test06_VerifyGatewayClasses() {
	// installPair already waited for GatewayClass=Accepted. Fast sanity check.
	for _, i := range pairIndices() {
		n := namesFor(i)
		out, err := s.kubectl("get", "gatewayclass", n.GWClass,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
		s.Require().NoError(err)
		s.Contains(out, "Accepted=True", "GatewayClass %s not Accepted", n.GWClass)
	}
}

func (s *gatewayPairsSuite) Test07_VerifyGateways() {
	// No default Gateway is created by the chart (gateway.create: false).
	// Apply a test Gateway+EnvoyProxy into each dataplane NS, verify it reaches
	// Programmed, then clean up. This validates the full EG reconcile path.
	for _, i := range pairIndices() {
		n := namesFor(i)
		s.applyManifest(n.DataplaneNS, testutil.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
		s.applyManifest(n.DataplaneNS, testutil.TestGatewayManifest(n.DataplaneNS, n.GWClass))
		s.eventually(func() bool {
			out, err := s.kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
				"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
			return err == nil && strings.Contains(out, "Programmed=True")
		}, 3*time.Minute, 5*time.Second,
			"test Gateway eg-test in %s not Programmed", n.DataplaneNS)
		s.kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found")     //nolint:errcheck
		s.kubectl("delete", "envoyproxy", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
	}
}

func (s *gatewayPairsSuite) Test08_VerifyDataplaneProxies() {
	// Apply a test Gateway+EnvoyProxy, wait for proxy Deployment in dataplaneNS,
	// then clean up. Confirms the controller creates proxy resources correctly.
	for _, i := range pairIndices() {
		n := namesFor(i)
		s.applyManifest(n.DataplaneNS, testutil.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
		s.applyManifest(n.DataplaneNS, testutil.TestGatewayManifest(n.DataplaneNS, n.GWClass))
		s.T().Logf("waiting for proxy Deployment in %s", n.DataplaneNS)
		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployments", "-n", n.DataplaneNS,
				"-l", "gateway.envoyproxy.io/owning-gateway-name=eg-test",
				"-o", "jsonpath={.items[0].status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 3*time.Minute, 5*time.Second,
			"Envoy proxy not ready in %s", n.DataplaneNS)
		s.kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found")     //nolint:errcheck
		s.kubectl("delete", "envoyproxy", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
	}
}

// ── traffic via HTTPRoute ─────────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test09_TrafficThroughAllPairs() {
	// localPort assigns a stable, non-overlapping host port to each pair so all
	// port-forwards can coexist: pair 1 → 18080, pair 2 → 18081, ...
	localPort := func(i int) int { return 18079 + i }

	for _, i := range pairIndices() {
		i := i // capture for closure
		n := namesFor(i)
		port := localPort(i)

		s.applyManifest(n.DataplaneNS, testutil.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
		s.applyManifest(n.DataplaneNS, testutil.TestGatewayManifest(n.DataplaneNS, n.GWClass))
		s.eventually(func() bool {
			out, err := s.kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
				"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
			return err == nil && strings.Contains(out, "Programmed=True")
		}, 3*time.Minute, 5*time.Second, "test Gateway not Programmed for pair %d", i)

		s.applyManifest(n.DataplaneNS, testutil.EchoDeploymentManifest(n.DataplaneNS))
		s.applyManifest(n.DataplaneNS, testutil.EchoServiceManifest(n.DataplaneNS))
		s.mustKubectl("rollout", "status", "deployment/echo", "-n", n.DataplaneNS, "--timeout=90s")

		s.applyManifest(n.DataplaneNS, testutil.HTTPRouteManifest("eg-test", n.DataplaneNS))

		gwSvc, err := s.findGatewayService(n.DataplaneNS)
		s.Require().NoError(err, "could not find Gateway Service in %s", n.DataplaneNS)

		stopFwd := s.portForward(n.DataplaneNS, "svc/"+gwSvc, fmt.Sprintf("%d:80", port))
		defer stopFwd()

		// Wait for the port-forward tunnel to be ready before probing.
		url := fmt.Sprintf("http://localhost:%d/get", port)
		s.eventually(func() bool {
			resp, err := http.Get(url) //nolint:noctx
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 30*time.Second, 2*time.Second, "expected 200 from echo via pair-%d Gateway", i)

		// Clean up test resources so pair dataplaneNS is pristine for Test10+.
		s.kubectl("delete", "httproute", "echo", "-n", n.DataplaneNS, "--ignore-not-found")     //nolint:errcheck
		s.kubectl("delete", "deployment", "echo", "-n", n.DataplaneNS, "--ignore-not-found")    //nolint:errcheck
		s.kubectl("delete", "service", "echo", "-n", n.DataplaneNS, "--ignore-not-found")       //nolint:errcheck
		s.kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found")    //nolint:errcheck
		s.kubectl("delete", "envoyproxy", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
	}
}

// ── delete and recovery ───────────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test10_DeletePair() {
	n := namesFor(deleteIdx)
	release := fmt.Sprintf("eg-pair-%d", deleteIdx)
	s.T().Logf("deleting %s from %s", release, n.SystemNS)

	// Hit /quitquitquit on proxy pods FIRST, while still Running.
	// See testutil.Harness.QuitProxyPods for full rationale.
	// This call also waits for the pods to exit before returning.
	s.quitProxyPods(n.DataplaneNS)

	// Delete all Gateways so EG clears its finalizer and deprovisions the
	// proxy Deployment before the controller is removed.
	s.kubectl("delete", "gateways", "--all", "-n", n.DataplaneNS, //nolint:errcheck
		"--ignore-not-found", "--wait=true", "--timeout=90s")
	s.kubectl("delete", "envoyproxies", "--all", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck

	// Wait until the proxy Deployment is deleted -- EG has deprovisioned.
	s.eventually(func() bool {
		out, err := s.kubectl("get", "deployments", "-n", n.DataplaneNS,
			"-l", "app.kubernetes.io/managed-by=envoy-gateway",
			"-o", "jsonpath={.items}")
		return err == nil && strings.TrimSpace(out) == "[]"
	}, 90*time.Second, 3*time.Second, "EG proxy Deployment not removed from %s", n.DataplaneNS)

	// Also wait for EG-owned Services to be removed (GC'd after the Deployment).
	s.eventually(func() bool {
		out, err := s.kubectl("get", "services", "-n", n.DataplaneNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-namespace="+n.DataplaneNS,
			"-o", "jsonpath={.items}")
		return err == nil && strings.TrimSpace(out) == "[]"
	}, 30*time.Second, 2*time.Second, "EG proxy Services not removed from %s", n.DataplaneNS)

	s.mustHelm("uninstall", release, "--namespace", n.SystemNS)

	// Delete both namespaces -- the system NS is the Helm release namespace and
	// is not removed by helm uninstall in all Helm versions.
	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		s.kubectl("delete", "namespace", ns, "--ignore-not-found", "--wait=false") //nolint:errcheck
	}

	s.eventually(func() bool {
		_, err := s.kubectl("get", "gatewayclass", n.GWClass)
		return err != nil
	}, 30*time.Second, 2*time.Second, "GatewayClass %s not removed", n.GWClass)

	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		ns := ns
		if !s.eventuallyBool(func() bool {
			_, err := s.kubectl("get", "namespace", ns)
			return err != nil
		}, 5*time.Minute, 5*time.Second) {
			// Namespace still Terminating -- dump remaining resources so we
			// know exactly what is blocking termination.
			s.dumpNamespaceBlockers(ns)
			s.Fail("condition not met within timeout", "Namespace %s not removed", ns)
		}
	}

	// Verify all cluster-scoped RBAC for this pair is gone.
	for _, res := range clusterScopedRBACFor(release) {
		res := res
		s.eventually(func() bool {
			_, err := s.kubectl("get", res)
			return err != nil
		}, 30*time.Second, 2*time.Second, "%s not removed", res)
	}
}

func (s *gatewayPairsSuite) Test11_PairsUnaffectedByDelete() {
	for _, i := range pairIndicesExcept(deleteIdx) {
		i := i // capture for closure
		n := namesFor(i)
		// Controller may briefly show 0 available replicas while reconciling the
		// deletion of deleteIdx's GatewayClass and ClusterRoles.
		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployment", "envoy-gateway",
				"-n", n.SystemNS, "-o", "jsonpath={.status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 30*time.Second, 3*time.Second,
			"controller in %s degraded after pair-%d delete", n.SystemNS, deleteIdx)

		// GatewayClass must remain Accepted -- confirms the controller is still
		// reconciling and was not affected by the deleted pair's GatewayClass removal.
		s.eventually(func() bool {
			out, err := s.kubectl("get", "gatewayclass", n.GWClass,
				"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
			return err == nil && strings.Contains(out, "Accepted=True")
		}, 30*time.Second, 3*time.Second,
			"GatewayClass %s degraded after pair-%d delete", n.GWClass, deleteIdx)
	}
}

func (s *gatewayPairsSuite) Test12_ReinstallPair() {
	s.installPair(deleteIdx)
	// installPair's own health gate (controller Available + GatewayClass Accepted)
	// is the complete readiness check -- no additional assertions needed here.
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *gatewayPairsSuite) installPair(index int) {
	n := namesFor(index)
	release := fmt.Sprintf("eg-pair-%d", index)
	s.T().Logf("installing %s (release ns: %s)", release, n.SystemNS)

	controllerName := fmt.Sprintf("gateway.envoyproxy.io/%s", n.GWClass)
	watchNS := fmt.Sprintf("{%s,%s}", n.SystemNS, n.DataplaneNS)

	args := []string{
		"upgrade", "--install", release,
		s.chartPath("eg-pair"),
		"--namespace", n.SystemNS,
		"--create-namespace",
		"--set", fmt.Sprintf("pair.index=%d", index),
		"--skip-crds",
		// Inject per-pair values the CLI would normally compute:
		"--set", "gateway-helm.config.envoyGateway.gateway.controllerName=" + controllerName,
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.type=Namespaces",
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces=" + watchNS,
		// No --wait: helm's --wait covers Deployments only, not certgen Job
		// completion. Readiness is checked explicitly below.
		"--timeout", "5m",
	}
	args = append(args, n.helmSetPrefix()...)
	s.mustHelm(args...)

	s.mustKubectl("wait", "deployment/envoy-gateway",
		"-n", n.SystemNS, "--for=condition=Available", "--timeout=5m")

	s.T().Logf("waiting for GatewayClass %s to be Accepted", n.GWClass)
	s.eventually(func() bool {
		out, err := s.kubectl("get", "gatewayclass", n.GWClass,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
		return err == nil && strings.Contains(out, "Accepted=True")
	}, 3*time.Minute, 5*time.Second,
		"GatewayClass %s not Accepted after install", n.GWClass)
}

func (s *gatewayPairsSuite) applyManifest(ns, manifest string) {
	s.T().Helper()
	cmd := exec.CommandContext(s.ctx, "kubectl", "--context", ktx,
		"apply", "-n", ns, "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, "kubectl apply failed:\n%s", string(out))
}

func (s *gatewayPairsSuite) findGatewayService(ns string) (string, error) {
	out, err := s.kubectl("get", "svc", "-n", ns,
		"-l", "gateway.envoyproxy.io/owning-gateway-name=eg-test",
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return "", fmt.Errorf("find gateway svc in %s: %w -- %s", ns, err, out)
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", fmt.Errorf("no gateway service found in %s", ns)
	}
	return name, nil
}

func (s *gatewayPairsSuite) portForward(ns, resource, ports string) func() {
	h := testutil.Harness{T: s.T(), Ctx: s.ctx, Ktx: ktx}
	return h.PortForward(ns, resource, ports)
}

func (s *gatewayPairsSuite) quitProxyPods(ns string) {
	// Delegate to testutil.Harness -- see QuitProxyPods for full rationale.
	// baseLocalPort 19100+ avoids conflicts with gateway port-forward (18080+).
	h := testutil.Harness{T: s.T(), Ctx: s.ctx, Ktx: ktx}
	h.QuitProxyPods(ns, 19100)
}

// gwp runs the gwp binary with the given args and returns stdout+stderr.
// Uses the binary path from the GWP_BIN env (set by make e2e).
func (s *gatewayPairsSuite) gwp(args ...string) (string, error) {
	h := testutil.Harness{T: s.T(), Ctx: s.ctx, Ktx: ktx, RepoRoot: s.repoRoot}
	return h.GWP(args...)
}

func (s *gatewayPairsSuite) verifyGatewayAPICRDs() {
	s.T().Helper()
	for _, crd := range []string{
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"referencegrants.gateway.networking.k8s.io",
	} {
		out := s.mustKubectl("get", "crd", crd, "-o", "name")
		s.Contains(out, crd, "missing CRD %s", crd)
	}
}

func (s *gatewayPairsSuite) verifyEGCRDs() {
	s.T().Helper()
	for _, crd := range []string{
		"envoyproxies.gateway.envoyproxy.io",
		"envoypatchpolicies.gateway.envoyproxy.io",
		"backendtrafficpolicies.gateway.envoyproxy.io",
	} {
		out := s.mustKubectl("get", "crd", crd, "-o", "name")
		s.Contains(out, crd, "missing CRD %s", crd)
	}
}

func (s *gatewayPairsSuite) eventually(
	condition func() bool,
	waitFor, tick time.Duration,
	msgAndArgs ...interface{},
) {
	s.T().Helper()
	if !s.eventuallyBool(condition, waitFor, tick) {
		s.Fail("condition not met within timeout", msgAndArgs...)
	}
}

func (s *gatewayPairsSuite) eventuallyBool(
	condition func() bool,
	waitFor, tick time.Duration,
) bool {
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(tick)
	}
	return false
}

// dumpNamespaceBlockers logs all resources with finalizers in ns.
// Called when namespace termination times out so CI logs show the blocker.
func (s *gatewayPairsSuite) dumpNamespaceBlockers(ns string) {
	s.T().Helper()
	s.T().Logf("=== namespace %s still Terminating -- dumping blockers ===", ns)

	// All pods with their finalizers.
	pods, _ := s.kubectl("get", "pods", "-n", ns,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\" finalizers=\"}{.metadata.finalizers}{\"\\n\"}{end}",
		"--ignore-not-found")
	if strings.TrimSpace(pods) != "" {
		s.T().Logf("pods:\n%s", pods)
	}

	// All resources with non-empty finalizers via kubectl api-resources.
	for _, res := range []string{"deployments", "services", "serviceaccounts", "roles", "rolebindings", "configmaps"} {
		out, _ := s.kubectl("get", res, "-n", ns,
			"-o", "jsonpath={range .items[?(@.metadata.finalizers)]}{.metadata.name}{\" finalizers=\"}{.metadata.finalizers}{\"\\n\"}{end}",
			"--ignore-not-found")
		if strings.TrimSpace(out) != "" {
			s.T().Logf("%s with finalizers:\n%s", res, out)
		}
	}

	// Namespace finalizers and status.
	nsDesc, _ := s.kubectl("get", "namespace", ns,
		"-o", "jsonpath=finalizers={.metadata.finalizers} phase={.status.phase} conditions={.status.conditions}",
		"--ignore-not-found")
	s.T().Logf("namespace status: %s", nsDesc)
}

// clusterScopedRBACFor returns the cluster-scoped RBAC resource identifiers
// created by the eg-pair chart for a given Helm release name.
func clusterScopedRBACFor(release string) []string {
	return []string{
		"clusterrole/" + release + "-tokenreviews",
		"clusterrole/" + release + "-gateway-controller",
		"clusterrolebinding/" + release + "-tokenreviews",
		"clusterrolebinding/" + release + "-gateway-controller",
	}
}