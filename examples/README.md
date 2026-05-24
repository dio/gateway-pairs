# Examples

Layer 3 resource examples for use after `gwp pair install`.

## single-tier

One Envoy proxy serving all traffic for a pair. The simplest possible topology.

```bash
gwp pair install 1
kubectl apply -n tr-dataplane-1 -f examples/single-tier/
```

Resources: `EnvoyProxy/proxy`, `Gateway/proxy`, `HTTPRoute/default`.

## multi-tier

Two independent Envoy proxies under one EG controller (L1 edge + L2 backend).
Each tier has its own `EnvoyProxy`, `Gateway`, and `HTTPRoute`.
EG places each proxy Deployment in `tr-dataplane-1` (GatewayNamespace mode).

```bash
gwp pair install 1
kubectl apply -n tr-dataplane-1 -f examples/multi-tier/
```

Resources: `EnvoyProxy/{l1,l2}`, `Gateway/{l1,l2}`, `HTTPRoute/{l1-to-l2,l2-to-backend}`.

## Customizing for your prefix/index

All examples use `tr-1` (prefix `tr`, index `1`). For a different pair:

```bash
# Get the exact values for your pair
gwp --prefix myapp pair info 2

# Substitute in the manifests
sed 's/tr-dataplane-1/myapp-dataplane-2/g; s/gatewayClassName: tr-1/gatewayClassName: myapp-2/g' \
  examples/single-tier/*.yaml | kubectl apply -n myapp-dataplane-2 -f -
```

## Key rules

- `gateway.spec.gatewayClassName` must match the pair's GatewayClass (`gwp pair info <i>` → `gatewayClassName`).
- `gateway.spec.infrastructure.parametersRef` must reference an `EnvoyProxy` in the **same namespace** as the Gateway. Cross-namespace is not supported at the Gateway level.
- `envoyService.name` sets a stable, predictable Service name. Without it EG generates a hash-based name that changes on recreate.
- `allowedRoutes.from: Selector` with label `tr/gateway-routes: "true"` is set on `tr-dataplane-{i}` by the `eg-pair` chart automatically.
