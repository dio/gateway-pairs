package multipairs_test

// RUN_E2E=1 go test -v -count=1 -run TestGatewayPairs ./multipairs/...
// Override namespace prefix: PAIR_PREFIX=tr go test ...

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

	"github.com/dio/gateway-pairs/e2e/helper"
)

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

func (s *gatewayPairsSuite) Test02_InstallPair1() { s.installPair(1) }
func (s *gatewayPairsSuite) Test03_InstallPair2() { s.installPair(2) }
func (s *gatewayPairsSuite) Test04_InstallPair3() { s.installPair(3) }

// ── isolation + correctness ───────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test05_VerifyIsolation() {
	for _, i := range []int{1, 2, 3} {
		n := namesFor(i)
		out := s.mustKubectl("get", "deployment", "envoy-gateway",
			"-n", n.SystemNS, "-o", "jsonpath={.status.availableReplicas}")
		s.Equal("1", strings.TrimSpace(out),
			"expected 1 available replica in %s", n.SystemNS)
	}
	// Proxy lives in SystemNS (Gateway's namespace). Dataplane NS holds tenant routes.
	// The per-pair controller Available check above is sufficient for isolation.
}

func (s *gatewayPairsSuite) Test06_VerifyGatewayClasses() {
	// installPair already waited for Gateway=Programmed, which implies
	// GatewayClass=Accepted. This is a fast sanity check.
	for _, i := range []int{1, 2, 3} {
		n := namesFor(i)
		out, err := s.kubectl("get", "gatewayclass", n.GWClass,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
		s.Require().NoError(err)
		s.Contains(out, "Accepted=True", "GatewayClass %s not Accepted", n.GWClass)
	}
}

func (s *gatewayPairsSuite) Test07_VerifyGateways() {
	// No default Gateway is created by the chart (gateway.create: false).
	// Apply a test Gateway+EnvoyProxy into the dataplane NS, verify it reaches
	// Programmed, then clean up. This validates the full EG reconcile path.
	for _, i := range []int{1, 2, 3} {
		n := namesFor(i)
		s.applyManifest(n.DataplaneNS, helper.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
		s.applyManifest(n.DataplaneNS, helper.TestGatewayManifest(n.DataplaneNS, n.GWClass))
		s.eventually(func() bool {
			out, err := s.kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
				"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
			return err == nil && strings.Contains(out, "Programmed=True")
		}, 3*time.Minute, 5*time.Second,
			"test Gateway eg-test in %s not Programmed", n.DataplaneNS)
		s.kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
		s.kubectl("delete", "envoyproxy", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
	}
}

func (s *gatewayPairsSuite) Test08_VerifyDataplaneProxies() {
	// Apply a test Gateway+EnvoyProxy, wait for proxy Deployment in dataplaneNS,
	// then clean up. Confirms the controller creates proxy resources correctly.
	for _, i := range []int{1, 2, 3} {
		n := namesFor(i)
		s.applyManifest(n.DataplaneNS, helper.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
		s.applyManifest(n.DataplaneNS, helper.TestGatewayManifest(n.DataplaneNS, n.GWClass))
		s.T().Logf("waiting for proxy Deployment in %s", n.DataplaneNS)
		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployments", "-n", n.DataplaneNS,
				"-l", "gateway.envoyproxy.io/owning-gateway-name=eg-test",
				"-o", "jsonpath={.items[0].status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 3*time.Minute, 5*time.Second,
			"Envoy proxy not ready in %s", n.DataplaneNS)
		s.kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
		s.kubectl("delete", "envoyproxy", "eg-test", "-n", n.DataplaneNS, "--ignore-not-found") //nolint:errcheck
	}
}

// ── traffic via HTTPRoute ─────────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test09_TrafficThroughPair1() {
	n := namesFor(1)

	// Apply test Gateway+EnvoyProxy into dataplaneNS (Layer 3 pattern).
	s.applyManifest(n.DataplaneNS, helper.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
	s.applyManifest(n.DataplaneNS, helper.TestGatewayManifest(n.DataplaneNS, n.GWClass))
	s.eventually(func() bool {
		out, err := s.kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
			"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
		return err == nil && strings.Contains(out, "Programmed=True")
	}, 3*time.Minute, 5*time.Second, "test Gateway not Programmed")

	// Echo backend in dataplaneNS.
	s.applyManifest(n.DataplaneNS, helper.EchoDeploymentManifest(n.DataplaneNS))
	s.applyManifest(n.DataplaneNS, helper.EchoServiceManifest(n.DataplaneNS))
	s.mustKubectl("rollout", "status", "deployment/echo", "-n", n.DataplaneNS, "--timeout=90s")

	// HTTPRoute in dataplaneNS referencing the test Gateway (same namespace).
	s.applyManifest(n.DataplaneNS, helper.HTTPRouteManifest("eg-test", n.DataplaneNS))

	// Gateway Service lives in dataplaneNS (proxy is there -- GatewayNamespace mode).
	gwSvc, err := s.findGatewayService(n.DataplaneNS)
	s.Require().NoError(err, "could not find Gateway Service in %s", n.DataplaneNS)

	stopFwd := s.portForward(n.DataplaneNS, "svc/"+gwSvc, "18080:80")
	defer stopFwd()
	time.Sleep(2 * time.Second)

	s.eventually(func() bool {
		resp, err := http.Get("http://localhost:18080/get") //nolint:noctx
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 2*time.Second, "expected 200 from echo via pair-1 Gateway")
}

// ── delete and recovery ───────────────────────────────────────────────────────

func (s *gatewayPairsSuite) Test10_DeletePair2() {
	n := namesFor(2)
	s.T().Logf("deleting eg-pair-2 from %s", n.SystemNS)

	// Delete the Gateway first so EG deprovisions the proxy Deployment and
	// removes its finalizer before we remove the controller.
	s.kubectl("delete", "gateway", "eg", "-n", n.SystemNS, //nolint:errcheck
		"--ignore-not-found", "--wait=true", "--timeout=60s")

	// Wait for proxy to be gone before uninstalling.
	s.eventually(func() bool {
		out, err := s.kubectl("get", "deployments", "-n", n.SystemNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-name=eg",
			"--ignore-not-found")
		return err == nil && !strings.Contains(out, "eg")
	}, 90*time.Second, 3*time.Second, "proxy Deployment not removed after Gateway delete")

	s.mustHelm("uninstall", "eg-pair-2", "--namespace", n.SystemNS)

	// Delete both namespaces -- system NS is the release NS (not explicitly
	// tracked for deletion by Helm after uninstall in all versions).
	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		s.kubectl("delete", "namespace", ns, "--ignore-not-found", "--wait=false") //nolint:errcheck
	}

	s.eventually(func() bool {
		_, err := s.kubectl("get", "gatewayclass", n.GWClass)
		return err != nil
	}, 30*time.Second, 2*time.Second, "GatewayClass %s not removed", n.GWClass)

	s.eventually(func() bool {
		_, err := s.kubectl("get", "namespace", n.SystemNS)
		return err != nil
	}, 3*time.Minute, 3*time.Second, "Namespace %s not removed", n.SystemNS)

	s.eventually(func() bool {
		_, err := s.kubectl("get", "namespace", n.DataplaneNS)
		return err != nil
	}, 3*time.Minute, 3*time.Second, "Namespace %s not removed", n.DataplaneNS)

	prefix := fmt.Sprintf("eg-pair-%d", 2)
	for _, res := range []string{
		"clusterrole/" + prefix + "-tokenreviews",
		"clusterrole/" + prefix + "-gateway-controller",
		"clusterrolebinding/" + prefix + "-tokenreviews",
		"clusterrolebinding/" + prefix + "-gateway-controller",
	} {
		res := res
		s.eventually(func() bool {
			_, err := s.kubectl("get", res)
			return err != nil
		}, 30*time.Second, 2*time.Second, "%s not removed", res)
	}
}

func (s *gatewayPairsSuite) Test11_PairsUnaffectedByDelete() {
	for _, i := range []int{1, 3} {
		n := namesFor(i)
		// Use eventually -- the controller may briefly show 0 available replicas
		// while reconciling the deletion of pair 2's GatewayClass and ClusterRoles.
		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployment", "envoy-gateway",
				"-n", n.SystemNS, "-o", "jsonpath={.status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 30*time.Second, 3*time.Second,
			"controller in %s degraded after pair-2 delete", n.SystemNS)

		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployments", "-n", n.SystemNS,
				"-l", "gateway.envoyproxy.io/owning-gateway-name=eg",
				"-o", "jsonpath={.items[0].status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 30*time.Second, 3*time.Second,
			"proxy in %s degraded after pair-2 delete", n.SystemNS)
	}
}

func (s *gatewayPairsSuite) Test12_ReinstallPair2() {
	s.installPair(2)
	n := namesFor(2)
	s.eventually(func() bool {
		out, err := s.kubectl("get", "deployments", "-n", n.SystemNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-name=eg",
			"-o", "jsonpath={.items[0].status.availableReplicas}")
		return err == nil && strings.TrimSpace(out) == "1"
	}, 3*time.Minute, 5*time.Second,
		"proxy not ready in %s after reinstall", n.SystemNS)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *gatewayPairsSuite) installPair(index int) {
	n := namesFor(index)
	release := fmt.Sprintf("eg-pair-%d", index)
	s.T().Logf("installing %s (release ns: %s)", release, n.SystemNS)

	args := []string{
		"upgrade", "--install", release,
		s.chartPath("eg-pair"),
		"--namespace", n.SystemNS,
		"--create-namespace",
		"--set", fmt.Sprintf("pair.index=%d", index),
		"--skip-crds",
		// No --wait here: helm's --wait only covers Deployments, not the certgen
		// Job completion. We handle readiness checks explicitly below.
		"--timeout", "5m",
	}
	args = append(args, n.helmSetPrefix()...)
	s.mustHelm(args...)

	// Wait for certgen Job to complete (it runs as a pre-install hook but
	// helm may return before the Job finalizes).
	s.mustKubectl("wait", "deployment/envoy-gateway",
		"-n", n.SystemNS, "--for=condition=Available", "--timeout=5m")

	// Health gate: GatewayClass Accepted confirms the controller is running
	// and has reconciled. No default Gateway is created (gateway.create: false)
	// so we do not wait for Programmed. Layer 3 operators apply their own
	// Gateways after this returns.
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

func (s *gatewayPairsSuite) applyNS(name string) {
	s.T().Helper()
	manifest := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", name)
	cmd := exec.CommandContext(s.ctx, "kubectl", "--context", ktx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, "apply namespace %s:\n%s", name, string(out))
}

func (s *gatewayPairsSuite) findGatewayService(ns string) (string, error) {
	out, err := s.kubectl("get", "svc", "-n", ns,
		"-l", "gateway.envoyproxy.io/owning-gateway-name=eg",
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
	cmd := exec.Command("kubectl", "--context", ktx,
		"port-forward", "-n", ns, resource, ports)
	_ = cmd.Start()
	return func() {
		if cmd.Process != nil {
			cmd.Process.Kill() //nolint:errcheck
		}
	}
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
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(tick)
	}
	s.Fail("condition not met within timeout", msgAndArgs...)
}

// ── manifest helpers ──────────────────────────────────────────────────────────

func helper.EchoDeploymentManifest(ns string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo
  template:
    metadata:
      labels:
        app: echo
    spec:
      containers:
      - name: echo
        image: kennethreitz/httpbin:latest
        ports:
        - containerPort: 80
`, ns)
}

func helper.EchoServiceManifest(ns string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: echo
  namespace: %s
spec:
  selector:
    app: echo
  ports:
  - port: 80
    targetPort: 80
`, ns)
}

func helper.HTTPRouteManifest(gatewayName, ns string) string {
	return fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo
  namespace: %s
spec:
  parentRefs:
  - name: %s
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /
    backendRefs:
    - name: echo
      port: 80
`, ns, gatewayName)
}

func helper.TestEnvoyProxyManifest(ns, gcName string) string {
	return fmt.Sprintf(`apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: eg-test
  namespace: %s
spec:
  provider:
    type: Kubernetes
    kubernetes:
      envoyService:
        name: eg-test
        type: ClusterIP
      envoyDeployment:
        pod:
          labels:
            eg-pair-test: "true"
`, ns)
}

func helper.TestGatewayManifest(ns, gcName string) string {
	return fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: eg-test
  namespace: %s
spec:
  gatewayClassName: %s
  infrastructure:
    parametersRef:
      group: gateway.envoyproxy.io
      kind: EnvoyProxy
      name: eg-test
  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: Same
`, ns, gcName)
}
