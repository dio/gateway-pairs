# Design

## Multi-pair model

One Helm release of `eg-pair` = one isolated controller+dataplane pair.
Resources by scope:

| Resource | Scope | Owner |
|---|---|---|
| Namespace tr-system-{i} | cluster | eg-pair release |
| Namespace tr-dataplane-{i} | cluster | eg-pair release |
| GatewayClass tr-{i} | cluster | eg-pair release |
| ClusterRole tokenreviews | cluster | eg-pair release |
| ClusterRoleBinding tokenreviews | cluster | eg-pair release |
| EG controller Deployment | tr-system-{i} | eg-pair release |
| EnvoyGateway ConfigMap | tr-system-{i} | eg-pair release |
| EnvoyProxy CR | tr-system-{i} | eg-pair release |
| Gateway | tr-system-{i} | eg-pair release (or tenant) |
| infra-manager Role | tr-dataplane-{i} | eg-pair release |
| infra-manager RoleBinding | tr-dataplane-{i} | eg-pair release |
| Envoy proxy Deployment | tr-dataplane-{i} | EG controller (generated) |
| Envoy proxy Service | tr-dataplane-{i} | EG controller (generated) |

## Authentication model

Gateway Namespace mode shifts xDS authentication from mTLS to JWT. Envoy
proxy pods (in tr-dataplane-{i}) authenticate to the controller using
projected ServiceAccount JWT tokens. The controller validates them via
TokenReview. This requires a cluster-scoped `tokenreviews/create` permission
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

Omitting tr-system-{i} causes Gateways to be Accepted but never Programmed.

## allowedRoutes wiring

HTTPRoutes live in tr-dataplane-{i}. The Gateway listener uses a namespace
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
namespace; no extra labeling is needed.

## CRD conflict strategy

| Scenario | Action |
|---|---|
| Fresh cluster | install Gateway API + EG CRDs |
| Provider-managed Gateway API (GKE, AKS autopilot) | skip Gateway API CRDs, install EG CRDs only |
| User-installed Gateway API, same or older version | force-upgrade via server-side apply |
| Wrong channel (experimental vs standard) | manual: check before install, do not downgrade blindly |

Detection command:

```bash
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' 2>/dev/null
```

Empty output = not installed or unmanaged. Non-empty = provider-managed.
