# Flux CD integration

Deploy `gateway-pairs` pairs using Flux CD's `helm-controller` and `source-controller`.
No Git repository required for the chart source -- the chart is pulled from the
OCI registry at `ghcr.io/dio/gateway-pairs/charts`.

## Prerequisites

- Flux v2 controllers installed (`flux install` or the static manifest)
- `gwp crds install` run once on the cluster (or use the CRD HelmRelease in `crds/`)
- `eg-pair` chart published to `ghcr.io/dio/gateway-pairs/charts` (done on every release)

## Bootstrap sequence

```bash
# 1. Install Flux controllers (one time per cluster)
kubectl apply -f https://github.com/fluxcd/flux2/releases/download/v2.5.1/install.yaml
kubectl wait -n flux-system deploy/source-controller --for=condition=Available --timeout=5m
kubectl wait -n flux-system deploy/helm-controller   --for=condition=Available --timeout=5m

# 2. Install CRDs (one time per cluster)
#    Option A: gwp CLI (handles provider-managed clusters correctly)
gwp crds install
#    Option B: Flux HelmRelease (full GitOps -- see crds/)
kubectl apply -f integrations/flux/crds/

# 3. Apply the HelmRepository (points at the OCI chart registry)
kubectl apply -f integrations/flux/pair/helmrepository.yaml

# 4. Apply one HelmRelease per pair
kubectl apply -f integrations/flux/examples/single-pair/helmrelease-pair-1.yaml
```

## Teardown ordering (important)

Delete Layer 3 resources before removing the HelmRelease. See the [repo README](../../integrations/README.md).

```bash
# Wrong: deletes controller while Gateway still exists -> namespace stuck Terminating
kubectl delete -f integrations/flux/examples/single-pair/helmrelease-pair-1.yaml

# Correct:
kubectl delete gateway --all -n tr-dataplane-1 --wait=true   # EG deprovisions proxy
kubectl delete -f integrations/flux/examples/single-pair/helmrelease-pair-1.yaml
```

## OCI chart registry

The `eg-pair` chart is published on every release:

```
oci://ghcr.io/dio/gateway-pairs/charts/eg-pair:<version>
```

The HelmRepository definition is in `pair/helmrepository.yaml`.

## Per-pair values

Three values cannot be auto-derived by the chart (Helm subchart limitation).
They are written statically in each HelmRelease because the pair identity is
fixed by declaration:

```yaml
values:
  gateway-helm:
    config:
      envoyGateway:
        gateway:
          controllerName: gateway.envoyproxy.io/tr-1   # unique per pair
        provider:
          kubernetes:
            watch:
              type: Namespaces
              namespaces:
                - tr-system-1     # {prefix}-system-{index}
                - tr-dataplane-1  # {prefix}-dataplane-{index}
```

Use `gwp pair info <index>` to get the exact values for an existing pair.
