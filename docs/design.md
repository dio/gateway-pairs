# Design

## Summary

`gateway-pairs` deploys N isolated Envoy Gateway controller+dataplane pairs
inside a single Kubernetes cluster. Each pair is one Helm release of `eg-pair`.
CRDs are installed once cluster-wide via `eg-crds` + `hack/install-crds.sh`.

---

## Multi-pair model

One Helm release = one isolated pair. One namespace per pair:

```
{prefix}-system-{id}    Helm release Secret + EG controller + proxy + Gateway + HTTPRoutes
GatewayClass {prefix}-{id}   cluster-scoped, unique per pair
```

The namespace is both the Helm release namespace (`--namespace` at install) and
the workload namespace. `helm uninstall` removes it along with all tracked
resources -- no namespace leak.

### Install

```bash
helm upgrade --install eg-pair-1 ./charts/eg-pair \
  --namespace tr-system-1 --create-namespace \
  --set pair.index=1 --skip-crds
```

`--create-namespace` creates the namespace. The chart declares it as a normal
Namespace resource so Helm adopts it into the release and removes it on uninstall.

### Uninstall

The proxy pod has a finalizer (`gateway-exists-finalizer.gateway.networking.k8s.io`)
that only the running controller can clear. Delete the Gateway first:

```bash
kubectl delete gateway eg -n tr-system-1 --wait=true --timeout=60s
kubectl wait --for=delete deployment \
  -l gateway.envoyproxy.io/owning-gateway-name=eg \
  -n tr-system-1 --timeout=60s
helm uninstall eg-pair-1 -n tr-system-1
# namespace terminates cleanly -- no explicit kubectl delete needed
```

Skipping the Gateway deletion causes the namespace to hang in `Terminating`
indefinitely because the proxy pod finalizer can never be cleared after the
controller is gone.

---

## Namespace naming

All names derive from `pair.namePrefix`, `pair.index`, and `pair.nameSuffix`.

Rules:
- `namePrefix`: auto-appended `-` if non-empty and not already ending with one.
- `nameSuffix`: appended as-is (caller controls separator).
- `nameSuffix` empty + `index > 0` → suffix defaults to `-{index}`.
- `nameSuffix` empty + `index == 0` → no suffix (single/unnamed pair).
- GatewayClass name omits the role fragment -- just prefix+suffix.

| namePrefix | index | nameSuffix | systemNS | GatewayClass |
|---|---|---|---|---|
| `tr` | `1` | `""` | `tr-system-1` | `tr-1` |
| `tars` | `1` | `""` | `tars-system-1` | `tars-1` |
| `tars` | `0` | `""` | `tars-system` | `tars` |
| `tars` | `1` | `1` | `tars-system1` | `tars-1` |
| `""` | `1` | `""` | `system-1` | `1` |
| `""` | `0` | `""` | `system` | *(empty)* |

---

## Resources by scope

| Resource | Scope | Created by |
|---|---|---|
| Namespace `{prefix}-system-{id}` | cluster | `--create-namespace` + chart resource |
| `GatewayClass {prefix}-{id}` | cluster | chart template |
| `ClusterRole eg-pair-{id}-tokenreviews` | cluster | chart template |
| `ClusterRoleBinding eg-pair-{id}-tokenreviews` | cluster | chart template |
| `ClusterRole eg-pair-{id}-gateway-controller` | cluster | chart template |
| `ClusterRoleBinding eg-pair-{id}-gateway-controller` | cluster | chart template |
| `ServiceAccount envoy-gateway` | system NS | chart template |
| `Deployment envoy-gateway` (controller) | system NS | chart template |
| `Service envoy-gateway` (xDS + metrics) | system NS | chart template |
| `ConfigMap envoy-gateway-config` | system NS | chart template |
| `EnvoyProxy eg` | system NS | chart template |
| `Gateway eg` | system NS | chart template |
| `Role infra-manager` | system NS | chart template |
| `RoleBinding infra-manager` | system NS | chart template |
| Envoy proxy `Deployment` | system NS | EG controller (generated) |
| Envoy proxy `Service` | system NS | EG controller (generated) |
| `HTTPRoute` | system NS | **tenant-managed** |

`helm uninstall` removes everything in this table. No manual cleanup needed
provided the Gateway deletion sequence above is followed first.

---

## Authentication: mTLS → JWT

Gateway Namespace mode shifts xDS auth from mTLS to JWT:

- **Default mode**: mutual TLS -- both proxy and controller present certificates.
- **Gateway Namespace mode**: server-side TLS + JWT token validation.
  - Proxy pods mount projected ServiceAccount JWT tokens (short-lived, auto-rotated).
  - Controller validates via `TokenReview` API.
  - Only the CA cert is available in the system namespace. No client certs.

Requires `tokenreviews/create` at cluster scope on the EG ServiceAccount.
If missing: `tokenreviews.authentication.k8s.io is forbidden` in controller logs,
proxies connect to xDS but stay Unready.

---

## Controller isolation: unique controllerName per pair

EG controllers watch all `GatewayClass` resources cluster-wide. If N controllers
share the same `controllerName`, each one tries to reconcile all N GatewayClasses.
A controller for pair 1 will try to find `EnvoyProxy tr-system-2/eg` for pair 2's
GatewayClass -- its cache only covers `tr-system-1` -- and log constantly:

```
failed to find envoyproxy tr-system-2/eg: unknown namespace for the cache
```

Fix: derive `controllerName` from the GatewayClass name so it is unique per pair:

```yaml
# ConfigMap envoy-gateway-config:
gateway:
  controllerName: gateway.envoyproxy.io/tr-1   # matches GatewayClass name

# GatewayClass:
spec:
  controllerName: gateway.envoyproxy.io/tr-1   # must match exactly
```

Both resources are generated by `_helpers.tpl` from the same value -- they
cannot drift. The upstream `gateway-helm` chart uses a single shared
`gatewayclass-controller` because it manages one GatewayClass. Multi-pair
deployments must diverge from this default.

---

## Watch list

The controller only needs to watch its own system namespace:

```yaml
watch:
  type: Namespaces
  namespaces:
  - tr-system-1   # controller reads its own TLS secret and manages all resources here
```

With one namespace per pair and `allowedRoutes: from: Same`, there is no
separate dataplane or tenant namespace to watch.

Omitting the system namespace from the watch list causes Gateways to be
`Accepted=True` but never `Programmed=True` -- the controller cannot read
its own TLS cert. Most common misconfiguration.

---

## HTTPRoutes and allowedRoutes

Everything lives in the system namespace. `from: Same` requires no selector,
no extra labels, no ReferenceGrant:

```yaml
listeners:
- name: http
  port: 80
  protocol: HTTP
  allowedRoutes:
    namespaces:
      from: Same
```

HTTPRoute `parentRefs` needs only the name:

```yaml
parentRefs:
- name: eg
```

---

## Certgen (EG v1.8+)

EG requires a TLS cert at `/certs/tls.crt` for its webhook server. The chart
runs `envoy-gateway certgen` as a `pre-install` Job to generate it. The cert is
written into a Secret named `envoy-gateway` in the system namespace and mounted
by the controller Deployment at `/certs`.

Topology injector is disabled by default (`--disable-topology-injector`). In a
multi-pair cluster each pair's certgen would try to register a cluster-scoped
`MutatingWebhookConfiguration` that watches all proxy pods -- pairs would
interfere with each other.

---

## Planned: subchart migration

The current chart hand-maintains certgen, RBAC, and the controller Deployment.
The next PR will add `gateway-helm` as a Helm subchart dependency:

```yaml
# charts/eg-pair/Chart.yaml
dependencies:
- name: gateway-helm
  version: "v1.8.0"
  repository: "oci://docker.io/envoyproxy"
```

`eg-pair` then becomes a thin wrapper that:
1. Passes values to `gateway-helm` (deploy type, controllerName, watch list)
2. Adds the GatewayClass, EnvoyProxy, and Gateway resources on top

RBAC, certgen, and the controller Deployment are maintained by upstream.
EG version bumps reduce to: bump `EG_VERSION`, run `helm dependency update`
and `make generate-crds`, done.

The `controllerName` cannot be set as a template expression in values -- it
must be passed as a `--set` flag by the installer (`gwp pair install` computes
it from the pair id).

---

## Planned: multi-tier proxies (L1/L2 for transit integrations)

EG v1.1+ supports `Gateway.spec.infrastructure.parametersRef → EnvoyProxy`,
overriding the GatewayClass-level default for a specific Gateway. One pair can
host multiple tiers under one controller:

```
GatewayClass tr-1
  Gateway/l1 → EnvoyProxy/l1   (custom image, l1 filter chain)
  Gateway/l2 → EnvoyProxy/l2   (shared image, l2 upstream config)
  HTTPRoutes → attach to l1 or l2 via parentRefs.name
```

Each tier gets its own proxy Deployment in the system namespace. `EnvoyPatchPolicy`
targets a specific Gateway by name -- patches are tier-scoped.

The `eg-pair` chart will grow a `tiers` list in values that generates one
Gateway + one EnvoyProxy per tier (range loop). Backward compat: `tiers` unset
falls back to the current single `gateway.name=eg` behavior.

---

## CRD conflict strategy

| Scenario | Action |
|---|---|
| Fresh cluster | install Gateway API + EG CRDs via `gateway-crds-helm` |
| Provider-managed Gateway API (GKE, AKS) | skip Gateway API CRDs, install EG CRDs only |
| User-installed, same channel | server-side apply upgrades cleanly |
| Channel mismatch (experimental → standard) | block -- check live objects first |

Always use `gateway-crds-helm` as the single version source. EG v1.8.0 bundles
Gateway API **v1.5.1**. Never pull Gateway API CRDs from a separate source.

Provider-managed detection: `managedFields[*].manager` (NOT the `bundle-version`
annotation -- that is written by any install path). Known managers:
`gke-networking-controller`, `gke-gateway-api`, `aks-gateway-api-controller`, `addon-manager`.

```bash
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.managedFields[*].manager}' 2>/dev/null
```

CRDs must be installed via `helm template | kubectl apply --server-side` -- never
`helm install`. Helm's 1 MB release Secret limit breaks on large CRD bundles.
