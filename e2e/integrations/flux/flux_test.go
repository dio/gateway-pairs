package flux_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/dio/sh"
	"github.com/dio/gateway-pairs/e2e/testutil"
)

const (
	systemNS    = pairPrefix + "-system-1"
	dataplaneNS = pairPrefix + "-dataplane-1"
	gwClass     = pairPrefix + "-1"
	releaseName = "eg-pair-1"
)

// Test01_HelmReleaseReady verifies the Flux helm-controller has successfully
// reconciled the eg-pair HelmRelease.
func (s *fluxSuite) Test01_HelmReleaseReady() {
	s.waitHelmRelease(releaseName, "flux-system", 5*time.Minute)

	// Sanity: helmChart field is populated (source-controller found and pulled the chart).
	out := s.MustKubectl("get", "helmrelease", releaseName, "-n", "flux-system",
		"-o", "jsonpath={.status.helmChart}")
	s.NotEmpty(strings.TrimSpace(out), "HelmRelease.status.helmChart should be set after reconcile")
}

// Test02_GatewayClassAccepted verifies EG registered and accepted the GatewayClass.
func (s *fluxSuite) Test02_GatewayClassAccepted() {
	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "gatewayclass", gwClass,
			"-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}",
			"--ignore-not-found")
		return err == nil && strings.Contains(out, "Accepted=True")
	}, 3*time.Minute, 5*time.Second, "GatewayClass %s not Accepted", gwClass)
}

// Test03_ControllerAvailable verifies the EG controller Deployment is healthy.
func (s *fluxSuite) Test03_ControllerAvailable() {
	s.MustKubectl("wait",
		"-n", systemNS, "deploy/envoy-gateway",
		"--for=condition=Available", "--timeout=3m")
}

// Test04_GatewayProgrammed applies a test Gateway + echo backend and verifies
// the Envoy proxy is wired up and serving HTTP.
func (s *fluxSuite) Test04_GatewayProgrammed() {
	// Apply Layer 3 resources into the dataplane namespace.
	s.Apply(dataplaneNS, testutil.TestEnvoyProxyManifest(dataplaneNS, ""))
	s.Apply(dataplaneNS, testutil.TestGatewayManifest(dataplaneNS, gwClass))
	s.Apply(dataplaneNS, testutil.EchoDeploymentManifest(dataplaneNS))
	s.Apply(dataplaneNS, testutil.EchoServiceManifest(dataplaneNS))
	// HTTPRouteManifest(gatewayName, ns) -- gateway name is "eg-test" (from TestGatewayManifest)
	s.Apply(dataplaneNS, testutil.HTTPRouteManifest("eg-test", dataplaneNS))

	// Wait for listener-level Programmed=True.
	// ClusterIP Gateways never show top-level Programmed=True (AddressNotAssigned).
	// Use listener conditions instead.
	s.Eventually(func() bool {
		out, err := s.Kubectl("get", "gateway", "eg-test", "-n", dataplaneNS,
			"-o", "jsonpath={range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}",
			"--ignore-not-found")
		return err == nil && strings.Contains(out, "Programmed=True")
	}, 5*time.Minute, 5*time.Second, "Gateway eg-test listener not Programmed in %s", dataplaneNS)

	// Find the EG-generated Service and port-forward to it.
	svc, err := s.h.FindGWSvc(dataplaneNS)
	s.Require().NoError(err, "EG-generated gateway service not found in %s", dataplaneNS)

	stop := s.PortForward(dataplaneNS, "svc/"+svc, "18080:80")
	defer stop()

	// Give port-forward tunnel a moment to establish.
	time.Sleep(500 * time.Millisecond)

	// Poll until port-forward tunnel is ready, then verify HTTP 200.
	var lastOut string
	s.Eventually(func() bool {
		out, curlErr := sh.Output(s.h.Ctx, "curl",
			"-s", "-o", "/dev/null", "-w", "%{http_code}",
			"--connect-timeout", "1", "--max-time", "2",
			"http://127.0.0.1:18080/")
		lastOut = out
		return curlErr == nil && strings.TrimSpace(out) == "200"
	}, 2*time.Minute, 2*time.Second, "HTTP GET via proxy returned %s", lastOut)
}

// Test05_DeleteOrdering verifies that deleting the Gateway before the HelmRelease
// allows Flux's helm uninstall to complete cleanly without a stuck namespace.
//
// Correct sequence:
//  1. QuitProxyPods -- instant teardown, collapses 360s grace period
//  2. Delete Layer 3 (Gateway, EnvoyProxy, HTTPRoute, echo) -- EG deprovisions proxy Deployment
//  3. Wait for EG-managed Deployments gone -- controller must still be alive here
//  4. Delete HelmRelease -- Flux runs helm uninstall; namespace terminates cleanly
func (s *fluxSuite) Test05_DeleteOrdering() {
	// 1. Send /quitquitquit to proxy pods before anything else.
	s.h.QuitProxyPods(dataplaneNS, 19100)

	// 2. Delete Layer 3 resources. Gateway deletion signals EG to deprovision the proxy.
	s.MustKubectl("delete", "gateway", "eg-test", "-n", dataplaneNS,
		"--ignore-not-found", "--wait=true")
	s.MustKubectl("delete", "envoyproxy", "eg-test", "-n", dataplaneNS, "--ignore-not-found")
	s.MustKubectl("delete", "httproute", "echo", "-n", dataplaneNS, "--ignore-not-found")
	s.MustKubectl("delete", "deployment", "echo", "-n", dataplaneNS, "--ignore-not-found")
	s.MustKubectl("delete", "service", "echo", "-n", dataplaneNS, "--ignore-not-found")

	// 3. Wait for EG-managed Deployments to be gone from the dataplane namespace.
	s.Eventually(func() bool {
		out, _ := s.Kubectl("get", "deployments", "-n", dataplaneNS,
			"-l", "app.kubernetes.io/managed-by=envoy-gateway",
			"-o", "jsonpath={.items}",
			"--ignore-not-found")
		t := strings.TrimSpace(out)
		return t == "" || t == "[]"
	}, 2*time.Minute, 2*time.Second, "EG-managed Deployments still present in %s", dataplaneNS)

	// 4. Delete the HelmRelease. Flux runs helm uninstall.
	//    Because the proxy is already gone, both namespaces terminate cleanly.
	//    Don't --wait on the delete itself -- Flux's finalizer can take longer
	//    than kubectl's wait window. Instead poll the namespaces directly below.
	s.MustKubectl("delete", "helmrelease", releaseName, "-n", "flux-system",
		"--ignore-not-found")

	// 5. Assert both namespaces are gone within 3 minutes.
	s.WaitNS(systemNS, dataplaneNS)

	s.T().Logf("Delete ordering validated: namespace %s and %s terminated cleanly",
		systemNS, fmt.Sprintf("(%s)", dataplaneNS))
}
