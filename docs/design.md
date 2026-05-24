# Design

## Multi-pair model

One Helm release of `eg-pair` = one isolated pair. Each pair uses two namespaces:

```
{prefix}-release-{id}   Helm release Secret only. No workloads.
{prefix}-system-{id}    Everything: EG controller, proxy, Gateway, tenant HTTPRoutes.
```

In GatewayNamespace mode EG places the generated proxy Deployment in the Gateway's
namespace. The Gateway is declared in the system namespace, so controller + proxy +
tenant HTTPRoutes all co-exist there. `allowedRoutes: from: Same` -- no cross-namespace
attachment needed.

Resources by scope:

| Resource | Scope | Created by |
|---|---|---|
| Namespace `{prefix}-release-{id}` | cluster | Helm `--create-namespace` |
| Namespace `{prefix}-system-{id}` | cluster | chart pre-install hook (weight -5) |
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

## Namespace naming

All names derive from three chart values: `pair.namePrefix`, `pair.index`, and
`pair.nameSuffix`. The derivation rules are:

- `namePrefix` is appended as-is but gets an auto-hyphen appended if non-empty and
  not already ending with `-`.
- `nameSuffix` is appended as-is. The caller controls any separator (including none).
- When `nameSuffix` is empty and `index > 0`, the suffix defaults to `-{index}`.
- When `nameSuffix` is empty and `index == 0`, no suffix is added (single/unnamed pair).

The role fragment (`release`, `system`) is inserted between the prefixed part and the suffix.
GatewayClass names omit the role fragment (prefix + suffix only).

### Examples

| namePrefix | index | nameSuffix | system NS | GatewayClass |
|---|---|---|---|---|
| `tr` | `1` | `""` | `tr-system-1` | `tr-1` |
| `tars` | `1` | `""` | `tars-system-1` | `tars-1` |
| `tars` | `0` | `""` | `tars-system` | `tars` |
| `tars` | `1` | `1` | `tars-system1` | `tars-1` |
| `""` | `1` | `""` | `system-1` | `1` |
| `""` | `0` | `""` | `system` | *(empty)* |

The `index=0, nameSuffix=""` case is for single/unnamed pairs where no numeric
identifier is needed. `index=0` with an explicit `nameSuffix` gives full control:
`namePrefix=prod, index=0, nameSuffix=-eu` → `prod-system-eu`.

### Helm install examples

```bash
# default: tr-system-1, tr-release-1, GatewayClass tr-1
helm upgrade --install eg-pair-1 ./charts/eg-pair \
  --namespace tr-release-1 --create-namespace \
  --set pair.index=1

# tars prefix: tars-system-1, tars-release-1, GatewayClass tars-1
helm upgrade --install eg-pair-1 ./charts/eg-pair \
  --namespace tars-release-1 --create-namespace \
  --set pair.namePrefix=tars --set pair.index=1

# no prefix: system-1, release-1, GatewayClass 1
helm upgrade --install eg-pair-1 ./charts/eg-pair \
  --namespace release-1 --create-namespace \
  --set pair.namePrefix="" --set pair.index=1

# suffix without hyphen: tars-system1, tars-release1, GatewayClass tars-1
helm upgrade --install eg-pair-1 ./charts/eg-pair \
  --namespace tars-release1 --create-namespace \
  --set pair.namePrefix=tars --set pair.index=1 --set pair.nameSuffix=1
```

## Release namespace

`{prefix}-release-{id}` is created by `helm --create-namespace` and holds only
the Helm release Secret. No workloads live here.

A dedicated release namespace avoids Helm ownership conflicts: `--create-namespace`
creates the namespace without Helm ownership annotations. If the chart also declared
a namespace with the same name, Helm would reject the install with:

```
invalid ownership metadata; label validation error: missing key "app.kubernetes.io/managed-by"
```

Keeping the release namespace separate (chart-unmanaged) eliminates this entirely.

## Authentication model

Gateway Namespace mode shifts xDS authentication from mTLS to JWT:

- **Default mode**: mTLS -- both client (proxy) and server (controller) present TLS
  certificates. Control plane and data plane share the controller namespace.
- **Gateway Namespace mode**: server-side TLS + JWT token validation.
  - Proxy pods use projected ServiceAccount JWT tokens (short-lived, auto-mounted).
  - Controller validates tokens via `TokenReview` API.
  - Only the CA certificate is available in the system namespace. No client certs.

This requires `tokenreviews/create` at cluster scope on the EG ServiceAccount.
If missing, proxies stay unready and EG logs show:
```
tokenreviews.authentication.k8s.io is forbidden
```

## Watch list

The controller's watch list must include the system namespace:

```yaml
watch:
  type: Namespaces
  namespaces:
  - {prefix}-system-{id}   # required: controller reads its own TLS secret here
```

Omitting it causes Gateways to be `Accepted=True` but never `Programmed=True`.
This is the most common misconfiguration.

## Gateway controller isolation

Each pair's EG controller must only reconcile its own GatewayClass. This is achieved
via a unique `controllerName` per pair -- the controller name matches the GatewayClass
name:

```yaml
# EnvoyGateway config (ConfigMap)
gateway:
  controllerName: gateway.envoyproxy.io/{prefix}-{id}

# GatewayClass
spec:
  controllerName: gateway.envoyproxy.io/{prefix}-{id}
```

If all pairs share `gateway.envoyproxy.io/gatewayclass-controller` (the upstream
default), every controller tries to reconcile every GatewayClass. Controllers that
don't have the foreign GatewayClass's system namespace in their watch cache fail with:
```
failed to find envoyproxy: unknown namespace for the cache
```

## HTTPRoutes and allowedRoutes

Proxy and Gateway both live in the system namespace. Tenants deploy HTTPRoutes there
too. The Gateway listener uses `from: Same` -- no cross-namespace attachment:

```yaml
listeners:
- name: http
  port: 80
  protocol: HTTP
  allowedRoutes:
    namespaces:
      from: Same
```

HTTPRoute in the same namespace needs no `parentRefs.namespace`:

```yaml
parentRefs:
- name: eg
```

## Uninstall sequence

The proxy pod has a finalizer (`gateway-exists-finalizer.gateway.networking.k8s.io`)
that only the running controller can clear. If the controller is deleted first the
finalizer is never removed and the namespace sticks in `Terminating`.

Correct order:
1. Delete the `Gateway` resource -- controller deprovisions proxy and clears finalizer.
2. Wait for proxy `Deployment` to be gone.
3. `helm uninstall`.
4. Delete namespace (`--wait=false` is safe; it will terminate cleanly now).

```bash
kubectl delete gateway eg -n {system-ns} --wait=true --timeout=60s
kubectl wait --for=delete deployment \
  -l gateway.envoyproxy.io/owning-gateway-name=eg \
  -n {system-ns} --timeout=60s
helm uninstall eg-pair-{id} -n {release-ns}
kubectl delete namespace {release-ns} {system-ns} --ignore-not-found --wait=false
```

## Merged Gateways incompatibility

Gateway Namespace Mode is **not supported** with Merged Gateways deployments.
These two features are mutually exclusive.

## CRD conflict strategy

| Scenario | Action |
|---|---|
| Fresh cluster | install Gateway API + EG CRDs |
| Provider-managed Gateway API (GKE, AKS autopilot) | skip Gateway API CRDs, install EG CRDs only |
| User-installed Gateway API, same channel | skip or `--force-gateway-api-crds` to upgrade |
| Channel mismatch (experimental vs standard) | block: downgrade removes CRDs with live objects |

Provider-managed detection: inspect `managedFields[*].manager`. Known managers:
`gke-networking-controller`, `gke-gateway-api`, `aks-gateway-api-controller`,
`addon-manager`. The `bundle-version` annotation alone is not a reliable signal --
it is written by any install path, not just provider-managed ones.

```bash
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.managedFields[*].manager}' 2>/dev/null
```
