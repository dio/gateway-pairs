# integrations/

GitOps and CD tool integrations for `gateway-pairs`.

Each subdirectory shows how to drive the **Layer 2** pair lifecycle (the `eg-pair`
Helm releases) from a specific tool. Layer 3 resources (Gateways, EnvoyProxies,
HTTPRoutes) are always operator-managed and live in a separate Git path.

## Three-layer model

```
Layer 1  CRDs (once per cluster)
         gwp crds install
         -- or -- a Flux HelmRelease for gateway-crds-helm

Layer 2  Per-pair scaffold (one Helm release per pair)
         This is what integrations/ drives.
         eg-pair chart -> tr-system-{i} + tr-dataplane-{i} + GatewayClass tr-{i}

Layer 3  Operator resources (per-tenant, per-pair)
         EnvoyProxy, Gateway, HTTPRoute, EnvoyPatchPolicy
         Applied by the operator into tr-dataplane-{i}.
         See examples/ in the repo root for reference manifests.
```

## Teardown ordering

Layer 3 must be removed before Layer 2. If you delete the eg-pair HelmRelease
while Gateways still exist, the proxy pod sits in Terminating for its full grace
period (360s) and the namespace hangs indefinitely.

Safe teardown in any GitOps tool:

1. Remove Layer 3 resources from Git (or kubectl delete the Gateways). Wait for
   Flux/ArgoCD to reconcile (proxy Deployment is removed by EG).
2. Remove the eg-pair HelmRelease from Git. The tool runs helm uninstall; the
   namespaces terminate cleanly because the proxy is already gone.

See each integration's README for tool-specific guidance.

## Integrations

| Dir | Tool | Status |
|---|---|---|
| `flux/` | Flux CD (helm-controller + source-controller) | ready |
| `helmfile/` | Helmfile | planned |
| `argocd/` | Argo CD (ApplicationSet) | planned |
