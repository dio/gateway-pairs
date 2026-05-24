# Design

## Multi-pair model

One Helm release of `eg-pair` = one isolated controller+dataplane pair.
Each pair uses two namespaces:

```
tr-release-{i}    Helm release Secret only. No workloads.
tr-system-{i}     Everything: EG controller, proxy, Gateway, tenant HTTPRoutes.
```

In GatewayNamespace mode EG places the generated proxy Deployment in the
Gateway's namespace. The Gateway is declared in `tr-system-{i}`, so the proxy
lands there alongside the controller. Tenants deploy HTTPRoutes in the same
namespace (Gateway `allowedRoutes: from: Same`).

There is no separate dataplane namespace. The `tr-dataplane-{i}` concept was
a naming mistake -- it implied isolation that doesn't exist with
`from: Same` routing. The system namespace IS the tenant namespace for
each pair.

Resources by scope:

| Resource | Scope | Created by |
|---|---|---|
| Namespace `tr-release-{i}` | cluster | Helm `--create-namespace` |
| Namespace `tr-system-{i}` | cluster | chart pre-install hook |
| `GatewayClass tr-{i}` | cluster | chart template |
| `ClusterRole eg-pair-{i}-tokenreviews` | cluster | chart template |
| `ClusterRoleBinding eg-pair-{i}-tokenreviews` | cluster | chart template |
| `ClusterRole eg-pair-{i}-gateway-controller` | cluster | chart template |
| `ClusterRoleBinding eg-pair-{i}-gateway-controller` | cluster | chart template |
| `ServiceAccount envoy-gateway` | tr-system-{i} | chart template |
| EG controller `Deployment envoy-gateway` | tr-system-{i} | chart template |
| `Service envoy-gateway` | tr-system-{i} | chart template |
| `ConfigMap envoy-gateway-config` | tr-system-{i} | chart template |
| `EnvoyProxy eg` | tr-system-{i} | chart template |
| `Gateway eg` | tr-system-{i} | chart template |
| `Role infra-manager` | tr-system-{i} | chart template |
| `RoleBinding infra-manager` | tr-system-{i} | chart template |
| Envoy proxy `Deployment` | tr-system-{i} | EG controller (generated) |
| Envoy proxy `Service` | tr-system-{i} | EG controller (generated) |
| `HTTPRoute` | tr-system-{i} | **tenant-managed** |

Resources by scope:

| Resource | Scope | Created by |
|---|---|---|
| Namespace `tr-release-{i}` | cluster | Helm `--create-namespace` |
| Namespace `tr-system-{i}` | cluster | chart template |
| Namespace `tr-dataplane-{i}` | cluster | chart template |
| `GatewayClass tr-{i}` | cluster | chart template |
| `ClusterRole eg-pair-{i}-tokenreviews` | cluster | chart template |
| `ClusterRoleBinding eg-pair-{i}-tokenreviews` | cluster | chart template |
| `ClusterRole eg-pair-{i}-gateway-controller` | cluster | chart template |
| `ClusterRoleBinding eg-pair-{i}-gateway-controller` | cluster | chart template |
| `ServiceAccount envoy-gateway` | tr-system-{i} | chart template |
| EG controller `Deployment envoy-gateway` | tr-system-{i} | chart template |
| `Service envoy-gateway` | tr-system-{i} | chart template |
| `ConfigMap envoy-gateway-config` | tr-system-{i} | chart template |
| `EnvoyProxy eg` | tr-system-{i} | chart template |
| `Gateway eg` | tr-system-{i} | chart template |
| `Role infra-manager` | tr-dataplane-{i} | chart template |
| `RoleBinding infra-manager` | tr-dataplane-{i} | chart template |
| Envoy proxy `Deployment` | tr-dataplane-{i} | EG controller (generated) |
| Envoy proxy `Service` | tr-dataplane-{i} | EG controller (generated) |

## Why three namespaces

**`tr-release-{i}`** is the Helm release namespace. Helm stores the release Secret
here. Using a dedicated release namespace avoids the ownership conflict that arises
when the chart declares a namespace with the same name as the release namespace.
Helm creates the release namespace via `--create-namespace` without Helm ownership
annotations; if the chart then declares a resource with that same name, Helm rejects
it with an ownership validation error on re-install. The dedicated release namespace
eliminates this ambiguity.

**`tr-system-{i}`** is declared in the chart templates, so Helm fully owns it and
`helm uninstall` removes it along with all its contents.

**`tr-dataplane-{i}`** is also chart-declared. The EG controller places generated
Envoy proxy resources here via Gateway Namespace mode.

`helm uninstall eg-pair-{i} --namespace tr-release-{i}` removes all three
namespaces and all cluster-scoped resources (GatewayClass, ClusterRoles,
ClusterRoleBindings) tracked by the release.

## Authentication model

Gateway Namespace mode shifts xDS authentication from mTLS to JWT. Envoy
proxy pods (in `tr-dataplane-{i}`) authenticate to the controller using
projected ServiceAccount JWT tokens. The controller validates them via
`TokenReview`. This requires a cluster-scoped `tokenreviews/create` permission
on the EG ServiceAccount.

## Watch list rule

Both namespaces MUST appear in the watch list:

```yaml
watch:
  type: Namespaces
  namespaces:
  - tr-system-1    # required: controller reads its own TLS secret here
  - tr-dataplane-1 # required: controller manages infra resources here
```

Omitting `tr-system-{i}` causes Gateways to be Accepted but never Programmed.

## allowedRoutes wiring

HTTPRoutes live in `tr-dataplane-{i}`. The Gateway listener uses a namespace
selector to permit cross-namespace attachment:

```yaml
allowedRoutes:
  namespaces:
    from: Selector
    selector:
      matchLabels:
        kubernetes.io/metadata.name: tr-dataplane-1
```

`kubernetes.io/metadata.name` is automatically set by Kubernetes on every
namespace. No extra labeling is needed. The chart derives the selector from
`pair.index` -- do not override it manually.

## CRD conflict strategy

| Scenario | Action |
|---|---|
| Fresh cluster | install Gateway API + EG CRDs |
| Provider-managed Gateway API (GKE, AKS autopilot) | skip Gateway API CRDs, install EG CRDs only |
| User-installed Gateway API, same channel | skip (already correct) or force-upgrade |
| Channel mismatch (experimental vs standard) | block: downgrade removes TCPRoute/BackendTLSPolicy CRDs |

Detection command:

```bash
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.managedFields[*].manager}' 2>/dev/null
```

Known provider managers: `gke-networking-controller`, `gke-gateway-api`,
`aks-gateway-api-controller`, `addon-manager`.
