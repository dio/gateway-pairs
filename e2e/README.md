# e2e

Integration tests for gateway-pairs. Each test run creates a fresh k3d cluster,
installs CRDs, installs N pairs, verifies isolation, exercises delete and reinstall,
then tears the cluster down.

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

# Keep cluster after run for manual inspection
KEEP_CLUSTER=1 make e2e

# Reuse an already-running cluster (skip create)
REUSE_CLUSTER=1 make e2e

# Direct go test form
cd e2e
RUN_PAIRS_E2E=1 go test -v -count=1 -tags=e2e -run TestGatewayPairs ./...
```

Always use `-count=1`. Cached results skip the real cluster run.

## Test sequence

| Test | What it proves |
|---|---|
| 01_InstallCRDs | Gateway API v1.5.1 + EG CRDs present; uses `hack/install-crds.sh` |
| 02_InstallPair1 | `eg-pair-1` controller Available, Deployment healthy |
| 03_InstallPair2 | `eg-pair-2` controller Available |
| 04_InstallPair3 | `eg-pair-3` controller Available |
| 05_VerifyIsolation | Each controller Available in its own namespace; no leak into dataplane NS |
| 06_VerifyGatewayClasses | GatewayClasses `tr-1`, `tr-2`, `tr-3` all Accepted |
| 07_VerifyGateways | Gateways `eg` in each system namespace reach `Programmed=True` |
| 08_VerifyDataplaneProxies | Envoy proxy Deployment available in each dataplane namespace |
| 09_DeletePair2 | Helm uninstall removes all cluster-scoped resources for pair 2 |
| 10_PairsUnaffectedByDelete | Pairs 1 and 3 still fully healthy after pair 2 deleted |
| 11_ReinstallPair2 | Pair 2 slot is reusable; reinstall reaches Programmed within timeout |

## Environment variables

| Variable | Default | Effect |
|---|---|---|
| `KEEP_CLUSTER` | `0` | If `1`, skip cluster delete in TearDownSuite |
| `KEEP_CLUSTER_ON_FAILURE` | `0` | If `1`, keep cluster only when the suite fails |
| `REUSE_CLUSTER` | `0` | If `1`, skip k3d cluster create (use existing) |

## Debugging a failed run

```bash
# Keep the cluster
KEEP_CLUSTER=1 RUN_PAIRS_E2E=1 go test -v -count=1 -tags=e2e -run TestGatewayPairs ./...

# Check overall state
kubectl --context k3d-gw-pairs-e2e get ns
kubectl --context k3d-gw-pairs-e2e get gatewayclass
kubectl --context k3d-gw-pairs-e2e get gateway -A

# Check a specific pair (replace 1 with the failing pair index)
kubectl --context k3d-gw-pairs-e2e get deployment envoy-gateway -n tr-system-1
kubectl --context k3d-gw-pairs-e2e logs -n tr-system-1 deploy/envoy-gateway | tail -40

# Check Gateway conditions verbosely
kubectl --context k3d-gw-pairs-e2e get gateway eg -n tr-system-1 -o yaml | grep -A20 conditions

# Check generated proxy resources
kubectl --context k3d-gw-pairs-e2e get all -n tr-dataplane-1
```

## Module

The e2e suite lives in its own Go module (`e2e/go.mod`) to keep its heavy
k8s dependencies out of the root CLI module graph. Run `go mod tidy` from
inside the `e2e/` directory.
