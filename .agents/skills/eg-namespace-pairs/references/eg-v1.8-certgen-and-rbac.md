# EG v1.8 Certgen, RBAC, and Proxy Placement

## Certgen Job (pre-install hook)

EG v1.8 requires a TLS cert at `/certs/tls.crt`. Without it, the controller
crashes: `open /certs/tls.crt: no such file or directory`.

The certgen Job must run BEFORE the controller Deployment is created.
Helm hook ordering: lowest weight runs first.

```yaml
# System namespace creation (weight -5, must come before certgen)
annotations:
  "helm.sh/hook": pre-install,pre-upgrade
  "helm.sh/hook-weight": "-5"
  "helm.sh/hook-delete-policy": before-hook-creation
  # NOTE: no hook-succeeded -- namespace must persist after certgen runs

# Certgen SA, Role, RoleBinding (weight -1)
annotations:
  "helm.sh/hook": pre-install,pre-upgrade
  "helm.sh/hook-weight": "-1"
  "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded,hook-failed

# Certgen Job (weight 0, default)
annotations:
  "helm.sh/hook": pre-install,pre-upgrade
  "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded,hook-failed
```

Certgen container command:
```yaml
command:
- envoy-gateway
- certgen
{{- if not .Values.controller.topologyInjector }}
- --disable-topology-injector
{{- end }}
env:
- name: ENVOY_GATEWAY_NAMESPACE
  value: {{ $sysNS }}   # MUST be the pair's system namespace, not the release NS
```

Certgen writes a Secret named `envoy-gateway` into `$sysNS`.

## Topology injector (disabled by default in multi-pair)

In a multi-pair cluster each EG instance would register a
`MutatingWebhookConfiguration` named `envoy-gateway-topology-injector.{sysNS}`.
Multiple webhooks watching the same cluster-wide proxy pod admission would
interfere with each other's proxies. Disable for all multi-pair deployments:

```yaml
controller:
  topologyInjector: false  # values.yaml default
```

## Deployment volume mount for cert

```yaml
volumeMounts:
- name: certs
  mountPath: /certs
  readOnly: true
volumes:
- name: certs
  secret:
    secretName: envoy-gateway
```

## Proxy placement (critical understanding)

In GatewayNamespace mode, EG places the generated proxy Deployment in the
**Gateway object's namespace**. The Gateway is declared in `tr-system-{i}`,
so the proxy lands in `tr-system-{i}` alongside the controller.

Consequence: infra-manager Role needs `create/patch` permissions in
`tr-system-{i}`, not just in a separate dataplane namespace. If infra-manager
only covers the dataplane namespace:

```
serviceaccounts "eg" is forbidden: cannot patch resource "serviceaccounts"
in API group "" in the namespace "tr-system-1"
```

## infra-manager Role location

The Role must be in the SAME namespace as the Gateway (= system namespace).
With `from: Same` routing there is no dataplane namespace, so only one
infra-manager Role is needed:

```yaml
kind: Role
metadata:
  name: infra-manager
  namespace: {{ $sysNS }}   # system namespace only
rules:
- apiGroups: [""]
  resources: [serviceaccounts, services, configmaps]
  verbs: [create, get, list, watch, delete, deletecollection, patch, update]
- apiGroups: [apps]
  resources: [deployments, daemonsets]
  verbs: [create, get, list, watch, delete, deletecollection, patch, update]
- apiGroups: [autoscaling]
  resources: [horizontalpodautoscalers]
  verbs: [create, get, list, watch, delete, deletecollection, patch, update]
- apiGroups: [policy]
  resources: [poddisruptionbudgets]
  verbs: [create, get, list, watch, delete, deletecollection, patch, update]
- apiGroups: [certificates.k8s.io]
  resources: [clustertrustbundles]
  verbs: [get, list, watch]
```

## Full upstream ClusterRole (gateway-helm v1.8.0)

Retrieve and diff with:
```bash
helm template eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.8.0 --namespace envoy-gateway-system 2>&1 \
  | sed -n '/envoy-gateway-rbac.yaml/,/^---/p'
```

Resources that are commonly missed when hand-authoring the ClusterRole:
- `listenersets` (Gateway API v1.5, new in EG v1.8)
- `pods`, `pods/binding` (topology watch)
- `serviceaccounts` (cluster-wide, for proxy SA creation)
- `multicluster.x-k8s.io/serviceimports`
- All `gateway.envoyproxy.io` status subresources:
  `envoypatchpolicies/status`, `clienttrafficpolicies/status`,
  `backendtrafficpolicies/status`, `securitypolicies/status`,
  `envoyextensionpolicies/status`, `backends/status`

Missing any of these causes the controller to start but fail with
`healthz check failed` in logs and constant `Failed to watch` errors.
RBAC changes take effect after pod restart.

## Debugging healthz failures

```bash
kubectl logs -n tr-system-1 deploy/envoy-gateway | grep forbidden
```

Common patterns:
- `serviceaccounts is forbidden ... in namespace tr-system-1` → infra-manager Role missing in system NS
- `listenersets.gateway.networking.k8s.io is forbidden` → ClusterRole missing listenersets
- `tokenreviews.authentication.k8s.io is forbidden` → tokenreviews ClusterRole missing

After fixing RBAC: `kubectl rollout restart deployment/envoy-gateway -n tr-system-1`
