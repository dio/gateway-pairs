package simplegwp_test

// RUN_E2E=1 go test -v -count=1 -run TestGWPSingle -timeout 20m ./gwp/...
// Override namespace prefix: PAIR_PREFIX=tr go test ...

import (
	"net/http"
	"strings"
	"time"

	"github.com/dio/gateway-pairs/e2e/testutil"
)

// Test00_Preflight runs preflight before anything is installed.
// On a fresh cluster with no CRDs the checks should warn (not fail) --
// missing CRDs are a warning that gwp crds install will fix, not a hard block.
func (s *gwpSuite) Test00_Preflight() {
	s.T().Log("running preflight on fresh cluster (no CRDs installed)")
	n := pairNames(1)

	// Preflight without --pair: only global checks. Should exit 0 (warnings OK).
	out, err := s.GWP("preflight", "--unsafe-context")
	s.T().Logf("preflight output:\n%s", out)
	s.NoError(err, "preflight should exit 0 on a fresh cluster (warnings allowed)")

	// With --pair 1: also runs conflict checks. No conflicts on fresh cluster.
	out, err = s.GWP("preflight", "--unsafe-context",
		"--pair", "1",
		"--prefix", n.Prefix)
	s.T().Logf("preflight --pair 1 output:\n%s", out)
	s.NoError(err, "preflight --pair 1 should exit 0 on fresh cluster")
}

// Test00b_ChartsList lists embedded charts -- no cluster access needed.
func (s *gwpSuite) Test00b_ChartsList() {
	out := s.MustGWP("charts", "list")
	s.T().Logf("charts list:\n%s", out)
	s.Contains(out, "eg-pair", "charts list should show eg-pair")
}

// Test01_InstallCRDs installs Gateway API + EG CRDs via gwp crds install.
// Equivalent to: bash hack/install-crds.sh
func (s *gwpSuite) Test01_InstallCRDs() {
	s.T().Log("installing CRDs via gwp crds install")

	out, _ := s.Kubectl("get", "crd", "gateways.gateway.networking.k8s.io", "--ignore-not-found")
	if strings.Contains(out, "gateways.gateway.networking.k8s.io") {
		s.T().Log("CRDs already installed -- skipping")
		return
	}

	s.MustGWP("crds", "install")

	// Verify key CRDs are present.
	s.MustKubectl("get", "crd",
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"envoyproxies.gateway.envoyproxy.io",
	)
	s.T().Log("CRDs installed")
}

// Test02_CRDDetect verifies gwp crds detect output after install.
func (s *gwpSuite) Test02_CRDDetect() {
	out := s.MustGWP("crds", "detect")
	s.T().Logf("gwp crds detect:\n%s", out)
	s.Contains(out, "installed", "expected CRDs to show as installed")
}

// Test03_InstallPair installs pair 1 via gwp pair install.
// Equivalent to: make pair-install PAIR=1
// gwp injects controllerName + watch.namespaces automatically.
func (s *gwpSuite) Test03_InstallPair() {
	n := pairNames(1)
	s.T().Logf("installing pair 1 via gwp → %s / %s", n.SystemNS, n.DataplaneNS)

	s.MustGWP("--prefix", n.Prefix, "pair", "install", "1",
		"--set", "gateway-helm.config.envoyGateway.extensionApis.enableEnvoyPatchPolicy=true",
	)
	// gwp pair install already waits for controller Available + GatewayClass Accepted.
	// Double-check controller is Available.
	s.MustKubectl("wait", "deployment/envoy-gateway",
		"-n", n.SystemNS, "--for=condition=Available", "--timeout=2m")
}

// Test04_GatewayClassAccepted verifies GatewayClass state via gwp pair status.
func (s *gwpSuite) Test04_GatewayClassAccepted() {
	n := pairNames(1)

	out := s.MustGWP("--prefix", n.Prefix, "pair", "status", "1")
	s.T().Logf("gwp pair status 1:\n%s", out)
	s.Contains(out, "Accepted=True", "expected GatewayClass Accepted=True in gwp pair status")
}

// Test05_PairInfo verifies gwp pair info output contains the correct coupling fields.
func (s *gwpSuite) Test05_PairInfo() {
	n := pairNames(1)

	out := s.MustGWP("--prefix", n.Prefix, "pair", "info", "1")
	s.T().Logf("gwp pair info 1:\n%s", out)
	s.Contains(out, n.GatewayClass, "expected GatewayClass name in gwp pair info")
	s.Contains(out, n.DataplaneNS, "expected dataplane namespace in gwp pair info")
}

// Test06_ApplyTier applies Layer 3 resources and waits for Gateway Programmed.
// Equivalent to: kubectl apply -n tr-dataplane-1 -f ...
func (s *gwpSuite) Test06_ApplyTier() {
	n := pairNames(1)
	s.T().Logf("applying Layer 3 into %s", n.DataplaneNS)

	s.Apply(n.DataplaneNS, testutil.TestEnvoyProxyManifest(n.DataplaneNS, n.GatewayClass))
	s.Apply(n.DataplaneNS, testutil.TestGatewayManifest(n.DataplaneNS, n.GatewayClass))

	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
			"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
		return err == nil && strings.Contains(out, "Programmed=True")
	}, 3*time.Minute, 5*time.Second, "Gateway eg-test not Programmed in %s", n.DataplaneNS)

	s.T().Logf("Gateway eg-test Programmed in %s", n.DataplaneNS)
}

// Test07_PairStatusShowsL3 verifies gwp pair status reflects the operator-applied Gateway.
func (s *gwpSuite) Test07_PairStatusShowsL3() {
	n := pairNames(1)

	out := s.MustGWP("--prefix", n.Prefix, "pair", "status", "1")
	s.T().Logf("gwp pair status 1 (after Layer 3):\n%s", out)
	s.Contains(out, "eg-test", "expected test Gateway in gwp pair status Layer 3 output")
}

// Test07b_PairVerify verifies gwp pair verify exits 0 after a healthy install+L3 apply.
func (s *gwpSuite) Test07b_PairVerify() {
	n := pairNames(1)

	// pair verify checks L3 Gateway Programmed status. Poll until healthy
	// since the Gateway may still be reconciling after Test06's wait.
	s.Eventually(func() bool {
		_, err := s.GWP("--prefix", n.Prefix, "pair", "verify", "1")
		return err == nil
	}, 2*time.Minute, 5*time.Second, "gwp pair verify should exit 0 on a healthy pair")

	// Confirm text output contains "healthy".
	out, err := s.GWP("--prefix", n.Prefix, "pair", "verify", "1")
	s.T().Logf("gwp pair verify 1:\n%s", out)
	s.NoError(err)
	s.Contains(out, "healthy")

	// JSON mode: healthy must be true.
	jsonOut, err := s.GWP("-o", "json", "--prefix", n.Prefix, "pair", "verify", "1")
	s.NoError(err)
	s.Contains(jsonOut, `"healthy": true`)
}

// Test08_Traffic sends HTTP through the pair and expects 200.
func (s *gwpSuite) Test08_Traffic() {
	n := pairNames(1)

	s.Apply(n.DataplaneNS, testutil.EchoDeploymentManifest(n.DataplaneNS))
	s.Apply(n.DataplaneNS, testutil.EchoServiceManifest(n.DataplaneNS))
	s.MustKubectl("rollout", "status", "deployment/echo",
		"-n", n.DataplaneNS, "--timeout=90s")
	s.Apply(n.DataplaneNS, testutil.HTTPRouteManifest("eg-test", n.DataplaneNS))

	gwSvc, err := s.FindGWSvc(n.DataplaneNS)
	s.Require().NoError(err, "gateway service not found in %s", n.DataplaneNS)
	stopFwd := s.PortForward(n.DataplaneNS, "svc/"+gwSvc, "18080:80")
	defer stopFwd()

	s.Eventually(func() bool {
		resp, err := http.Get("http://localhost:18080/") //nolint:noctx
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 1*time.Second, "expected 200 from echo")
	s.T().Log("traffic OK")
}

// Test09_PairList verifies gwp pair list finds the installed pair.
func (s *gwpSuite) Test09_PairList() {
	n := pairNames(1)

	out := s.MustGWP("pair", "list")
	s.T().Logf("gwp pair list:\n%s", out)
	s.Contains(out, n.SystemNS, "expected system namespace in gwp pair list")
	s.Contains(out, n.GatewayClass, "expected GatewayClass name in gwp pair list")
	s.Contains(out, "deployed", "expected deployed status in gwp pair list")
}

// Test09b_EnvoyPatchPolicy applies an EnvoyPatchPolicy that remaps 404 → 406
// and verifies the patch is picked up by the proxy. This confirms that
// enableEnvoyPatchPolicy=true (passed during gwp pair install) is effective.
func (s *gwpSuite) Test09b_EnvoyPatchPolicy() {
	n := pairNames(1)

	// Apply the EPP. It targets eg-test in the dataplane NS.
	s.Apply(n.DataplaneNS, testutil.EnvoyPatchPolicyManifest(n.DataplaneNS))

	// Wait for EPP to be Accepted and Programmed.
	// EPP uses Policy Attachment status: .status.ancestors[0].conditions.
	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "envoypatchpolicy", "epp-test",
			"-n", n.DataplaneNS,
			"-o", "jsonpath={range .status.ancestors[0].conditions[*]}{.type}={.status} {end}",
			"--ignore-not-found")
		return err == nil &&
			strings.Contains(out, "Accepted=True") &&
			strings.Contains(out, "Programmed=True")
	}, 90*time.Second, 3*time.Second, "EnvoyPatchPolicy epp-test not Programmed")

	s.T().Log("EnvoyPatchPolicy Accepted=True, Programmed=True: patch applied to proxy")

	// Verify the 404→406 remap. The HTTPRoute covers /; remove it temporarily
	// so /nonexistent returns an Envoy-generated 404 (which EPP remaps to 406).
	s.MustKubectl("delete", "httproute", "echo",
		"-n", n.DataplaneNS, "--ignore-not-found")

	gwSvc, err := s.FindGWSvc(n.DataplaneNS)
	s.Require().NoError(err)
	stopFwd := s.PortForward(n.DataplaneNS, "svc/"+gwSvc, "18081:80")
	defer stopFwd()
	time.Sleep(500 * time.Millisecond)

	// Without any route: Envoy returns 404. With EPP: remapped to 406.
	s.Eventually(func() bool {
		resp, err := http.Get("http://localhost:18081/") //nolint:noctx
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 406
	}, 30*time.Second, 2*time.Second,
		"expected 406 from EnvoyPatchPolicy (404→406 remap)")
	s.T().Log("EnvoyPatchPolicy verified: Envoy 404 remapped to 406")

	// Restore the HTTPRoute for the remaining tests.
	s.Apply(n.DataplaneNS, testutil.HTTPRouteManifest("eg-test", n.DataplaneNS))

	// Cleanup EPP so Test10 teardown is clean.
	s.MustKubectl("delete", "envoypatchpolicy", "epp-test",
		"-n", n.DataplaneNS, "--ignore-not-found")
}

// Test10_Delete uninstalls pair 1 via gwp pair delete.
// Equivalent to: make pair-delete PAIR=1 (but with correct Gateway teardown sequence).
func (s *gwpSuite) Test10_Delete() {
	n := pairNames(1)
	s.T().Log("deleting pair 1 via gwp pair delete")

	// Delete Gateway first so EG clears proxy finalizers before controller goes away.
	s.Kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, //nolint:errcheck
		"--ignore-not-found", "--wait=true", "--timeout=60s")
	s.Eventually(func() bool {
		out, _ := s.Kubectl("get", "deployments", "-n", n.DataplaneNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-name=eg-test",
			"--ignore-not-found")
		return !strings.Contains(out, "eg-test")
	}, 90*time.Second, 3*time.Second, "proxy not removed after Gateway delete")

	s.MustGWP("--prefix", n.Prefix, "pair", "delete", "1")

	s.Eventually(func() bool {
		_, err := s.Kubectl("get", "gatewayclass", n.GatewayClass)
		return err != nil
	}, 30*time.Second, 2*time.Second, "GatewayClass %s not removed after gwp pair delete", n.GatewayClass)

	s.T().Log("pair 1 deleted cleanly")
}
