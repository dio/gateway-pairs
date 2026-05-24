# gateway-pairs

Deploy N isolated Envoy Gateway controller+dataplane pairs inside a single
Kubernetes cluster. Each pair is one Helm release of `eg-pair`.

```
tr-system-{i}      EG controller Deployment, SA, RBAC, ConfigMap
tr-dataplane-{i}   Gateways, EnvoyProxies, proxy Deployments/Services, HTTPRoutes
GatewayClass tr-{i}   cluster-scoped, unique per pair
```

CRDs are installed once cluster-wide. Each pair's controller watches only its own
two namespaces and manages only its own GatewayClass -- complete isolation.

## Install

Download `gwp` from the [releases page](https://github.com/dio/gateway-pairs/releases)
or via Homebrew:

```bash
brew install dio/tap/gwp
```

`gwp` embeds the `eg-pair` chart and pre-rendered CRD YAML. No OCI registry
access or separate chart downloads needed at install time.

## Quickstart

```bash
# Create a local cluster
k3d cluster create gw-pairs --agents 1 \
  --k3s-arg --disable=traefik@server:*

# Install CRDs (Gateway API v1.5.1 + EG v1.8.0) -- once per cluster
gwp crds install

# Install pair 1
gwp pair install 1

# Apply Layer 3 resources (Gateways, EnvoyProxies, HTTPRoutes)
kubectl apply -n tr-dataplane-1 -f gateways.yaml

# Check status
gwp pair status
```

## Namespace model

Each pair uses two namespaces:

| Namespace | Contents |
|---|---|
| `tr-system-{i}` | Helm release Secret, EG controller, SA, RBAC, ConfigMap |
| `tr-dataplane-{i}` | Gateways, EnvoyProxies, proxy Deployments + Services, HTTPRoutes |

In GatewayNamespace mode EG places proxy Deployments in the Gateway's namespace.
Since Gateways live in `tr-dataplane-{i}`, proxies land there too.

## gwp CLI

```
gwp crds detect          show installed CRD versions and who manages them
gwp crds install         install Gateway API + EG CRDs (detects provider-managed, skips if present)

gwp pair install <i>     install or upgrade pair i (injects controllerName + watch.namespaces)
gwp pair status [i]      health of one pair or all pairs (Layer 2 + Layer 3 Gateways)
gwp pair info <i>        print gatewayClassName, dataplaneNamespace, allowedRoutes label
gwp pair list            list all installed pairs
gwp pair delete <i>      uninstall pair i (warns if proxy finalizers need clearing first)

gwp version              print gwp version and bundled EG version
```

See [MANUAL.md](MANUAL.md) for full operator workflows, the raw Helm path,
and troubleshooting.

## Layer model

| Layer | Installed by | Resources |
|---|---|---|
| 1 -- cluster | `gwp crds install` | Gateway API CRDs, EG CRDs |
| 2 -- per pair | `gwp pair install` | GatewayClass, controller, RBAC, namespaces |
| 3 -- per tenant | operator (`kubectl apply`) | EnvoyProxies, Gateways, HTTPRoutes |

The only coupling from Layer 3 to Layer 2 is `gatewayClassName: tr-{i}` in each
Gateway manifest. Use `gwp pair info <i>` for the exact values.

## e2e

```bash
make e2e                 # multipairs suite (2 pairs, creates k3d cluster)
PAIR_COUNT=5 make e2e    # larger machine
make e2e-simple          # single-pair smoke test via raw Helm
make e2e-simple-gwp      # single-pair smoke test via gwp CLI
```

See [e2e/README.md](e2e/README.md) for the full test sequence.

## Requirements

| Tool | Version | When |
|---|---|---|
| `gwp` | latest | install + operate |
| `helm` | >= 3.14 | required by gwp at install time |
| `kubectl` | any recent | always |
| `k3d` | >= 5.7 | local dev / e2e only |
| `go` | >= 1.24 | e2e harness only |

## Design

See [docs/design.md](docs/design.md) for architecture, RBAC shape, watch list
wiring, uninstall sequence, and multi-tier proxy topology.
