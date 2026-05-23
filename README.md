# gateway-pairs

Deploy Envoy Gateway as isolated controller+dataplane pairs inside a single
cluster. Each pair consists of:

- `tr-system-{i}` -- holds the EG controller Deployment, its config, and
  Gateway/HTTPRoute resources written by tenants.
- `tr-dataplane-{i}` -- receives the generated Envoy proxy Deployment and
  Service created by the controller.

Multiple pairs share one cluster. CRDs are installed once. Each pair is a
separate Helm release.

## Charts

| Chart | Install once? | Purpose |
|---|---|---|
| `eg-crds` | yes | Gateway API + Envoy Gateway CRDs |
| `eg-pair` | per pair | EG controller, RBAC, GatewayClass, Gateway |

## Quickstart (k3d)

```bash
# one cluster
k3d cluster create gw-pairs --agents 0 \
  --k3s-arg --disable=traefik@server:*

# CRDs -- install once
make crds-install

# pair 1
make pair-install PAIR=1

# e2e proof
make e2e
```

## Requirements

- k3d >= 5.7
- kubectl
- helm >= 3.14
- go >= 1.24 (for e2e harness)

## Design

See [docs/design.md](docs/design.md) for the full architecture, RBAC shape,
CRD conflict strategy, and allowedRoutes wiring.
