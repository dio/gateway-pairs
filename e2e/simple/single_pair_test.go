package simple_test

import (
	"net/http"

	"github.com/dio/gateway-pairs/e2e/testutil"
	"os/exec"
	"strings"
	"time"
)

func (s *simpleSuite) Test01_InstallCRDs() {
	s.T().Log("installing CRDs")
	// Skip if already installed
	out, _ := s.Kubectl("get", "crd", "gateways.gateway.networking.k8s.io")
	if strings.Contains(out, "gateways.gateway.networking.k8s.io") {
		s.T().Log("CRDs already installed -- skipping")
		return
	}
	s.Must("bash", s.h.RepoRoot+"/hack/install-crds.sh")
	s.MustKubectl("get", "crd",
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"envoyproxies.gateway.envoyproxy.io",
	)
}

func (s *simpleSuite) Test02_InstallPair() {
	n := namesFor(1)
	s.T().Logf("installing eg-pair-1 → %s / %s", n.SystemNS, n.DataplaneNS)
	s.MustHelm(
		"upgrade", "--install", "eg-pair-1",
		s.ChartPath("eg-pair"),
		"--namespace", n.SystemNS, "--create-namespace",
		"--set", "pair.index=1",
		"--set", "gateway-helm.config.envoyGateway.gateway.controllerName=gateway.envoyproxy.io/"+n.GWClass,
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.type=Namespaces",
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces={"+n.SystemNS+","+n.DataplaneNS+"}",
		"--set", "gateway-helm.config.envoyGateway.extensionApis.enableEnvoyPatchPolicy=true",
		"--skip-crds",
		"--timeout", "8m",
	)
	s.MustKubectl("wait", "deployment/envoy-gateway",
		"-n", n.SystemNS, "--for=condition=Available", "--timeout=5m")
}

func (s *simpleSuite) Test03_GatewayClassAccepted() {
	n := namesFor(1)
	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "gatewayclass", n.GWClass,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
		return err == nil && strings.Contains(out, "Accepted=True")
	}, 3*time.Minute, 5*time.Second, "GatewayClass %s not Accepted", n.GWClass)
	s.T().Logf("GatewayClass %s Accepted", n.GWClass)
}

func (s *simpleSuite) Test04_ApplyTier() {
	n := namesFor(1)
	s.T().Logf("applying Layer 3 into %s", n.DataplaneNS)
	s.Apply(n.DataplaneNS, testutil.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
	s.Apply(n.DataplaneNS, testutil.TestGatewayManifest(n.DataplaneNS, n.GWClass))
	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
			"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
		return err == nil && strings.Contains(out, "Programmed=True")
	}, 3*time.Minute, 5*time.Second, "Gateway eg-test not Programmed in %s", n.DataplaneNS)
	s.T().Logf("Gateway eg-test Programmed in %s", n.DataplaneNS)
}

func (s *simpleSuite) Test05_Traffic() {
	n := namesFor(1)
	s.Apply(n.DataplaneNS, testutil.EchoDeploymentManifest(n.DataplaneNS))
	s.Apply(n.DataplaneNS, testutil.EchoServiceManifest(n.DataplaneNS))
	s.MustKubectl("rollout", "status", "deployment/echo",
		"-n", n.DataplaneNS, "--timeout=90s")
	s.Apply(n.DataplaneNS, testutil.HTTPRouteManifest("eg-test", n.DataplaneNS))

	gwSvc, err := s.FindGWSvc(n.DataplaneNS)
	s.Require().NoError(err, "gateway service not found in %s", n.DataplaneNS)
	stopFwd := s.PortForward(n.DataplaneNS, "svc/"+gwSvc, "18080:80")
	defer stopFwd()

	// Wait until the forwarded port is actually accepting connections.
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

func (s *simpleSuite) Test06_Delete() {
	n := namesFor(1)
	s.T().Logf("deleting pair-1")

	// Delete Gateway first so EG clears proxy finalizers before controller goes away.
	s.Kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, //nolint:errcheck
		"--ignore-not-found", "--wait=true", "--timeout=60s")
	s.Eventually(func() bool {
		out, _ := s.Kubectl("get", "deployments", "-n", n.DataplaneNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-name=eg-test",
			"--ignore-not-found")
		return !strings.Contains(out, "eg-test")
	}, 90*time.Second, 3*time.Second, "proxy not removed after Gateway delete")

	s.MustHelm("uninstall", "eg-pair-1", "--namespace", n.SystemNS)
	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		exec.Command("kubectl", "--context", ktx,
			"delete", "namespace", ns, "--ignore-not-found", "--wait=false").Run() //nolint:errcheck
	}
	s.Eventually(func() bool {
		_, err := s.Kubectl("get", "gatewayclass", n.GWClass)
		return err != nil
	}, 30*time.Second, 2*time.Second, "GatewayClass %s not removed", n.GWClass)
	s.T().Log("pair-1 deleted cleanly")
}
