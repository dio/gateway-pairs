# E2E Harness Shape for gateway-pairs

Reference implementation: `github.com/dio/gateway-pairs/e2e/`

## Module layout

```
e2e/
  go.mod          -- separate module, keeps heavy k8s deps out of root CLI module
  suite_test.go   -- pairsBaseSuite: cluster lifecycle, kubectl/helm helpers
  names_test.go   -- pairNames struct + namesFor(index) + PAIR_PREFIX env var
  pairs_test.go   -- TestGatewayPairs: 12 ordered tests
```

Unified under `go.work` at repo root: `go work init . ./e2e`.

## pairNames helper (names_test.go)

All namespace/resource names derive from `PAIR_PREFIX` (env var, default `tr`):

```go
type pairNames struct {
    prefix    string
    index     int
    ReleaseNS string  // {prefix}-release-{index}
    SystemNS  string  // {prefix}-system-{index}  -- everything lives here
    GWClass   string  // {prefix}-{index}
}

func namesFor(index int) pairNames { ... }
func (p pairNames) helmSetPrefix() []string {
    return []string{"--set", fmt.Sprintf("pair.namePrefix=%s", p.prefix)}
}
```

Passes `--set pair.namePrefix=...` to helm automatically. Makefile `e2e` target
passes `PAIR_PREFIX=$(PAIR_PREFIX)` into the go test invocation.

## installPair -- wait for Gateway=Programmed

`installPair` must not return until `Gateway Programmed=True`. Returning as soon
as the controller Deployment is Available is insufficient -- the GatewayClass may
not be Accepted yet. The correct pattern:

```go
func (s *gatewayPairsSuite) installPair(index int) {
    n := namesFor(index)
    args := []string{
        "upgrade", "--install", release, chartPath,
        "--namespace", n.ReleaseNS, "--create-namespace",
        "--set", fmt.Sprintf("pair.index=%d", index),
        "--skip-crds", "--timeout", "5m",  // no --wait
    }
    args = append(args, n.helmSetPrefix()...)
    s.mustHelm(args...)

    s.mustKubectl("wait", "deployment/envoy-gateway",
        "-n", n.SystemNS, "--for=condition=Available", "--timeout=5m")

    s.eventually(func() bool {
        out, err := s.kubectl("get", "gateway", "eg", "-n", n.SystemNS,
            "-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
        return err == nil && strings.Contains(out, "Programmed=True")
    }, 5*time.Minute, 5*time.Second, "Gateway eg in %s not Programmed", n.SystemNS)
}
```

Once `installPair` guarantees `Programmed=True`, `Test06_VerifyGatewayClasses`
and `Test07_VerifyGateways` become instant `s.Contains` checks, not polled loops.

## Gateway status jsonpath

Use range expression to avoid filter expression quoting issues across kubectl versions:

```go
// CORRECT: range iterates all conditions, Contains finds Programmed=True
out, err := s.kubectl("get", "gateway", "eg", "-n", n.SystemNS,
    "-o", "jsonpath={range .status.conditions[*]}{.type}={.status} {end}")
return err == nil && strings.Contains(out, "Programmed=True")

// WRONG: filter expression ?(@.type=='Programmed') has quoting issues
out, err := s.kubectl("get", "gateway", "eg", "-n", n.SystemNS,
    "-o", "jsonpath={.status.conditions[?(@.type==\"Programmed\")].status}")
```

Same pattern for GatewayClass `Accepted=True`.

## Delete sequence (Test10)

Delete Gateway BEFORE helm uninstall to prevent namespace stuck in Terminating:

```go
// 1. Delete Gateway -- EG deprovisions proxy, removes finalizers
s.kubectl("delete", "gateway", "eg", "-n", n.SystemNS,
    "--ignore-not-found", "--wait=true", "--timeout=60s")

// 2. Wait for proxy Deployment to be gone
s.eventually(func() bool {
    out, err := s.kubectl("get", "deployments", "-n", n.SystemNS,
        "-l", "gateway.envoyproxy.io/owning-gateway-name=eg", "--ignore-not-found")
    return err == nil && !strings.Contains(out, "eg")
}, 90*time.Second, 3*time.Second, "proxy not removed")

// 3. Helm uninstall
s.mustHelm("uninstall", "eg-pair-2", "--namespace", n.ReleaseNS)

// 4. Explicit namespace delete (release NS not tracked by Helm)
for _, ns := range []string{n.ReleaseNS, n.SystemNS} {
    s.kubectl("delete", "namespace", ns, "--ignore-not-found", "--wait=false")
}

// 5. Poll for termination
s.eventually(func() bool {
    _, err := s.kubectl("get", "namespace", n.SystemNS)
    return err != nil
}, 90*time.Second, 3*time.Second, "Namespace %s not removed", n.SystemNS)
```

## REUSE_CLUSTER cleanup

When `REUSE_CLUSTER=1`, uninstall and delete all namespaces explicitly:

```go
for i := 1; i <= 3; i++ {
    n := namesFor(i)
    exec.Command("helm", "--kube-context", ktx,
        "uninstall", fmt.Sprintf("eg-pair-%d", i), "--namespace", n.ReleaseNS,
        "--ignore-not-found").Run()
}
for i := 1; i <= 3; i++ {
    n := namesFor(i)
    for _, ns := range []string{n.ReleaseNS, n.SystemNS} {
        exec.Command("kubectl", "--context", ktx,
            "delete", "namespace", ns, "--ignore-not-found", "--wait=false").Run()
    }
}
// Poll until all namespaces are gone (2-minute deadline)
deadline := time.Now().Add(2 * time.Minute)
for _, i := range []int{1, 2, 3} {
    n := namesFor(i)
    for _, ns := range []string{n.ReleaseNS, n.SystemNS} {
        for time.Now().Before(deadline) {
            out, err := kubectl("get", "namespace", ns)
            if err != nil || !strings.Contains(out, ns) { break }
            time.Sleep(2 * time.Second)
        }
    }
}
```

Key: `tr-release-{i}` is created by `--create-namespace` without Helm ownership
annotations. `helm uninstall` does NOT delete it. Must delete explicitly.

## k3d cluster sizing

Use `--agents 1` for 3-pair e2e. Single-node (`--agents 0`) causes resource
contention -- 3 controllers + 3 proxies + 3 certgen Jobs on one node is too
much under typical laptop constraints. Pair 2 (scheduled last) always times out.

```bash
k3d cluster create gw-pairs-e2e \
  --agents 1 \
  --image rancher/k3s:v1.32.2-k3s1 \
  --k3s-arg --disable=traefik@server:*
```

Wait for BOTH nodes after create:
```bash
kubectl wait nodes/k3d-gw-pairs-e2e-server-0 --for=condition=Ready --timeout=120s
kubectl wait nodes/k3d-gw-pairs-e2e-agent-0  --for=condition=Ready --timeout=120s
```
