//go:build e2e

package e2e_test

// RUN_PAIRS_E2E=1 go test -v -count=1 -tags=e2e -run TestGatewayPairs ./...

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type gatewayPairsSuite struct {
	pairsBaseSuite
}

func TestGatewayPairs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}
	suite.Run(t, new(gatewayPairsSuite))
}

func (s *gatewayPairsSuite) Test01_InstallCRDs() {
	s.T().Log("installing eg-crds")
	s.mustHelm(
		"upgrade", "--install", "eg-crds",
		s.chartPath("eg-crds"),
		"--namespace", "kube-system",
		"--set", "crds.gatewayAPI.enabled=true",
		"--set", "crds.gatewayAPI.channel=standard",
		"--set", "crds.envoyGateway.enabled=true",
		"--wait", "--timeout", "120s",
	)

	// The eg-crds chart itself just tracks metadata; actual CRDs are applied
	// by the Makefile crds-install target. Verify that target ran.
	s.verifyGatewayAPICRDs()
	s.verifyEGCRDs()
}

func (s *gatewayPairsSuite) Test02_InstallPair1() {
	s.installPair(1)
}

func (s *gatewayPairsSuite) Test03_InstallPair2() {
	s.installPair(2)
}

func (s *gatewayPairsSuite) Test04_VerifyIsolation() {
	// Each controller should only see resources in its own pair namespaces.
	// Rough proxy: each controller Deployment is distinct and healthy.
	for _, i := range []int{1, 2} {
		sysNS := fmt.Sprintf("tr-system-%d", i)
		s.T().Logf("checking controller in %s", sysNS)
		out := s.mustKubectl("get", "deployment", "envoy-gateway",
			"-n", sysNS,
			"-o", "jsonpath={.status.availableReplicas}")
		s.Equal("1", strings.TrimSpace(out),
			"expected 1 available replica in %s", sysNS)
	}
}

func (s *gatewayPairsSuite) Test05_VerifyGatewayClasses() {
	out := s.mustKubectl("get", "gatewayclass", "-o",
		"jsonpath={range .items[*]}{.metadata.name}={.status.conditions[?(@.type=="Accepted")].status}\n{end}")
	for _, i := range []int{1, 2} {
		gcName := fmt.Sprintf("tr-%d", i)
		s.Contains(out, gcName+"=True",
			"GatewayClass %s not Accepted", gcName)
	}
}

func (s *gatewayPairsSuite) Test06_VerifyGateways() {
	for _, i := range []int{1, 2} {
		sysNS := fmt.Sprintf("tr-system-%d", i)
		// Wait up to 2 min for Gateway to be Programmed.
		s.T().Logf("waiting for Gateway in %s to be Programmed", sysNS)
		s.eventually(func() bool {
			out, err := s.kubectl("get", "gateway", "eg", "-n", sysNS,
				"-o", "jsonpath={.status.conditions[?(@.type=\"Programmed\")].status}")
			return err == nil && strings.TrimSpace(out) == "True"
		}, 2*time.Minute, 5*time.Second,
			"Gateway eg in %s never reached Programmed=True", sysNS)
	}
}

func (s *gatewayPairsSuite) Test07_VerifyDataplaneProxies() {
	// EG should have created Envoy proxy Deployments in the dataplane namespaces.
	for _, i := range []int{1, 2} {
		dpNS := fmt.Sprintf("tr-dataplane-%d", i)
		s.T().Logf("waiting for Envoy proxy Deployment in %s", dpNS)
		s.eventually(func() bool {
			out, err := s.kubectl("get", "deployments", "-n", dpNS,
				"-l", fmt.Sprintf("eg-pair=%d,eg-role=dataplane", i),
				"-o", "jsonpath={.items[0].status.availableReplicas}")
			return err == nil && strings.TrimSpace(out) == "1"
		}, 3*time.Minute, 5*time.Second,
			"Envoy proxy not ready in %s", dpNS)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *gatewayPairsSuite) installPair(index int) {
	release := fmt.Sprintf("eg-pair-%d", index)
	sysNS := fmt.Sprintf("tr-system-%d", index)
	s.T().Logf("installing %s into %s", release, sysNS)

	s.mustHelm(
		"upgrade", "--install", release,
		s.chartPath("eg-pair"),
		"--namespace", sysNS,
		"--create-namespace",
		"--set", fmt.Sprintf("pair.index=%d", index),
		"--skip-crds",
		"--wait", "--timeout", "120s",
	)

	// Wait for controller Deployment specifically, not just pod labels.
	s.mustKubectl("rollout", "status", "deployment/envoy-gateway",
		"-n", sysNS, "--timeout=120s")
	s.mustKubectl("wait", "deployment/envoy-gateway",
		"-n", sysNS, "--for=condition=Available", "--timeout=120s")
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
