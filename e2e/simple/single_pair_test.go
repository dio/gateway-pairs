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
	out, _ := s.kubectl("get", "crd", "gateways.gateway.networking.k8s.io")
	if strings.Contains(out, "gateways.gateway.networking.k8s.io") {
		s.T().Log("CRDs already installed -- skipping")
		return
	}
	s.must("bash", s.repoRoot+"/hack/install-crds.sh")
	s.mustKubectl("get", "crd",
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"envoyproxies.gateway.envoyproxy.io",
	)
}

func (s *simpleSuite) Test02_InstallPair() {
	n := namesFor(1)
	s.T().Logf("installing eg-pair-1 → %s / %s", n.SystemNS, n.DataplaneNS)
	s.mustHelm(
		"upgrade", "--install", "eg-pair-1",
		s.chartPath("eg-pair"),
		"--namespace", n.SystemNS, "--create-namespace",
		"--set", "pair.index=1",
		"--set", "gateway-helm.config.envoyGateway.gateway.controllerName=gateway.envoyproxy.io/"+n.GWClass,
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.type=Namespaces",
		"--set", "gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces={"+n.SystemNS+","+n.DataplaneNS+"}",
		"--set", "gateway-helm.config.envoyGateway.extensionApis.enableEnvoyPatchPolicy=true",
		"--skip-crds",
		"--timeout", "5m",
	)
	s.mustKubectl("wait", "deployment/envoy-gateway",
		"-n", n.SystemNS, "--for=condition=Available", "--timeout=5m")
}

func (s *simpleSuite) Test03_GatewayClassAccepted() {
	n := namesFor(1)
	s.eventually(func() bool {
		out, err := s.kubectl("get", "gatewayclass", n.GWClass,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
		return err == nil && strings.Contains(out, "Accepted=True")
	}, 3*time.Minute, 5*time.Second, "GatewayClass %s not Accepted", n.GWClass)
	s.T().Logf("GatewayClass %s Accepted", n.GWClass)
}

func (s *simpleSuite) Test04_ApplyTier() {
	n := namesFor(1)
	s.T().Logf("applying Layer 3 into %s", n.DataplaneNS)
	s.apply(n.DataplaneNS, testutil.TestEnvoyProxyManifest(n.DataplaneNS, n.GWClass))
	s.apply(n.DataplaneNS, testutil.TestGatewayManifest(n.DataplaneNS, n.GWClass))
	s.eventually(func() bool {
		out, err := s.kubectl("get", "gateway", "eg-test", "-n", n.DataplaneNS,
			"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}")
		return err == nil && strings.Contains(out, "Programmed=True")
	}, 3*time.Minute, 5*time.Second, "Gateway eg-test not Programmed in %s", n.DataplaneNS)
	s.T().Logf("Gateway eg-test Programmed in %s", n.DataplaneNS)
}

func (s *simpleSuite) Test05_Traffic() {
	n := namesFor(1)
	s.apply(n.DataplaneNS, testutil.EchoDeploymentManifest(n.DataplaneNS))
	s.apply(n.DataplaneNS, testutil.EchoServiceManifest(n.DataplaneNS))
	s.mustKubectl("rollout", "status", "deployment/echo",
		"-n", n.DataplaneNS, "--timeout=90s")
	s.apply(n.DataplaneNS, testutil.HTTPRouteManifest("eg-test", n.DataplaneNS))

	gwSvc := s.findGWSvc(n.DataplaneNS)
	stopFwd := s.portForward(n.DataplaneNS, "svc/"+gwSvc, "18080:80")
	defer stopFwd()
	time.Sleep(2 * time.Second)

	s.eventually(func() bool {
		resp, err := http.Get("http://localhost:18080/") //nolint:noctx
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 2*time.Second, "expected 200 from echo")
	s.T().Log("traffic OK")
}

func (s *simpleSuite) Test06_Delete() {
	n := namesFor(1)
	s.T().Logf("deleting pair-1")

	// Delete Gateway first so EG clears proxy finalizers before controller goes away.
	s.kubectl("delete", "gateway", "eg-test", "-n", n.DataplaneNS, //nolint:errcheck
		"--ignore-not-found", "--wait=true", "--timeout=60s")
	s.eventually(func() bool {
		out, _ := s.kubectl("get", "deployments", "-n", n.DataplaneNS,
			"-l", "gateway.envoyproxy.io/owning-gateway-name=eg-test",
			"--ignore-not-found")
		return !strings.Contains(out, "eg-test")
	}, 90*time.Second, 3*time.Second, "proxy not removed after Gateway delete")

	s.mustHelm("uninstall", "eg-pair-1", "--namespace", n.SystemNS)
	for _, ns := range []string{n.SystemNS, n.DataplaneNS} {
		exec.Command("kubectl", "--context", ktx,
			"delete", "namespace", ns, "--ignore-not-found", "--wait=false").Run() //nolint:errcheck
	}
	s.eventually(func() bool {
		_, err := s.kubectl("get", "gatewayclass", n.GWClass)
		return err != nil
	}, 30*time.Second, 2*time.Second, "GatewayClass %s not removed", n.GWClass)
	s.T().Log("pair-1 deleted cleanly")
}
