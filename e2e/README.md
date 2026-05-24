# e2e

Integration tests for gateway-pairs. Each run creates a fresh k3d cluster,
installs CRDs, installs N pairs, verifies isolation and traffic, exercises
delete and reinstall, then tears down.

## Requirements

- Go >= 1.24
- k3d >= 5.7
- helm >= 3.14
- kubectl
- Docker (for k3d)

## Running

```bash
# Full suite (creates cluster, installs everything, tears down on success)
make e2e

# Custom namespace prefix (default: tr)
PAIR_PREFIX=tars make e2e

# Keep cluster after run
KEEP_CLUSTER=1 make e2e

# Reuse an already-running cluster (skips cluster create + CRD install)
REUSE_CLUSTER=1 make e2e

# Direct go test
cd e2e
PAIR_PREFIX=tr RUN_PAIRS_E2E=1 \
  go test -v -count=1 -tags=e2e -run TestGatewayPairs ./...
```

Always use `-count=1`. Cached results skip the real cluster run.

## Namespace prefix

`PAIR_PREFIX` (default: `tr`) controls all derived names:

| PAIR_PREFIX | index | SystemNS (= release NS) | GatewayClass |
|-------------|-------|-------------------------|--------------|
| `tr`        | 1     | tr-system-1             | tr-1         |
| `tars`      | 1     | tars-release-1  | tars-system-1  | tars-dataplane-1  | tars-1       |
| `""`        | 1     | release-1       | system-1       | dataplane-1       | 1            |

The same prefix must be passed to both the test suite and the chart:
- `make e2e PAIR_PREFIX=tars` -- passes `PAIR_PREFIX=tars` to the suite and
  `--set pair.namePrefix=tars` to helm automatically.
- `make pair-install PAIR=1 PAIR_PREFIX=tars` -- Makefile derives the release
  namespace and passes `--set pair.namePrefix=tars` to helm.

## Test sequence

| Test | What it proves |
|---|---|
| 01_InstallCRDs | Gateway API v1.5.1 + EG CRDs present |
| 02_InstallPair1 | eg-pair-1 controller Available |
| 03_InstallPair2 | eg-pair-2 controller Available |
| 04_InstallPair3 | eg-pair-3 controller Available |
| 05_VerifyIsolation | Each controller Available in own NS; no leak into dataplane NS |
| 06_VerifyGatewayClasses | GatewayClasses {prefix}-1/2/3 all Accepted |
| 07_VerifyGateways | Gateways in each system NS reach Programmed=True |
| 08_VerifyDataplaneProxies | Envoy proxy Deployment available (in system NS) |
| 09_TrafficThroughPair1 | Echo server + HTTPRoute + port-forward → HTTP 200 |
| 10_DeletePair2 | helm uninstall removes all cluster-scoped resources for pair 2 |
| 11_PairsUnaffectedByDelete | Pairs 1 and 3 still healthy after pair 2 deleted |
| 12_ReinstallPair2 | Pair 2 slot is reusable; reinstall recovers within timeout |

## Proxy namespace note

In GatewayNamespace mode EG places the proxy Deployment in the **Gateway
object's namespace** (SystemNS). DataplaneNS holds tenant HTTPRoutes only.
The `allowedRoutes` selector on the Gateway listener permits cross-namespace
attachment from DataplaneNS.

## Environment variables

| Variable | Default | Effect |
|---|---|---|
| `PAIR_PREFIX` | `tr` | Namespace prefix for all derived names |
| `KEEP_CLUSTER` | `0` | If `1`, skip cluster delete on teardown |
| `KEEP_CLUSTER_ON_FAILURE` | `0` | If `1`, keep cluster only on failure |
| `REUSE_CLUSTER` | `0` | If `1`, skip cluster create and CRD install |

## Debugging

```bash
KEEP_CLUSTER=1 REUSE_CLUSTER=1 RUN_PAIRS_E2E=1 \
  go test -v -count=1 -tags=e2e -run TestGatewayPairs ./...

# Overall state
kubectl --context k3d-gw-pairs-e2e get ns | grep -E 'release|system|dataplane'
kubectl --context k3d-gw-pairs-e2e get gatewayclass
kubectl --context k3d-gw-pairs-e2e get gateway -A

# One pair (replace 1 with failing index)
kubectl --context k3d-gw-pairs-e2e logs -n tr-system-1 deploy/envoy-gateway | tail -30
kubectl --context k3d-gw-pairs-e2e get gateway eg -n tr-system-1 -o yaml | grep -A20 conditions
kubectl --context k3d-gw-pairs-e2e get all -n tr-system-1
```

## Module

The e2e suite has its own Go module (`e2e/go.mod`) separate from the root
CLI module. Run `go mod tidy` from inside `e2e/`. Both modules are joined
by `go.work` at the repo root.
