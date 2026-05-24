# Operator Manual

Two installation paths:

- **CLI path** (`gwp`): the primary path. Single binary, embeds the chart and
  pre-rendered CRD YAML. No OCI access or chart downloads at install time.
- **Helm path**: direct Helm + `hack/install-crds.sh`. Useful for CI pipelines
  that already manage Helm directly, or for inspecting/customising chart templates.

---

## Concepts

### Two namespaces per pair

| Namespace | Contents |
|---|---|
| `tr-system-{i}` | Release Secret, EG controller, SA, RBAC, ConfigMap |
| `tr-dataplane-{i}` | Gateways, EnvoyProxies, proxy Deployments + Services, HTTPRoutes |

`tr-system-{i}` is the Helm release namespace. `tr-dataplane-{i}` is a
chart-declared resource. In GatewayNamespace mode EG places proxy Deployments in
the Gateway's namespace. Since Gateways live in `tr-dataplane-{i}`, proxies
land there too.

### Layer model

| Layer | Installed by | Resources |
|---|---|---|
| 1: cluster | `gwp crds install` | Gateway API CRDs, EG CRDs |
| 2: per pair | `gwp pair install` | GatewayClass, controller, RBAC, namespaces |
| 3: per tenant | operator (`kubectl apply`) | EnvoyProxies, Gateways, HTTPRoutes |

The only coupling from Layer 3 to Layer 2 is `gatewayClassName: tr-{i}` in each
Gateway manifest. Everything else in `tr-dataplane-{i}` is operator-owned.

### Naming

All names derive from `--prefix` (default `tr`) and pair index:

| Field | Value (prefix=tr, index=1) |
|---|---|
| Release name | `eg-pair-1` |
| System namespace | `tr-system-1` |
| Dataplane namespace | `tr-dataplane-1` |
| GatewayClass | `tr-1` |
| Controller name | `gateway.envoyproxy.io/tr-1` |

---

## CLI path (`gwp`)

### Prerequisites

```bash
gwp     # download from https://github.com/dio/gateway-pairs/releases
          # or: brew install dio/tap/gwp
helm    # >= 3.14 (required by gwp for chart install)
kubectl
```

### 1. Create a cluster (local dev)

```bash
k3d cluster create gw-pairs \
  --agents 1 \
  --image rancher/k3s:v1.32.2-k3s1 \
  --k3s-arg --disable=traefik@server:*
```

### 2. Install CRDs (once per cluster)

```bash
gwp crds install
```

Detects provider-managed Gateway API CRDs (GKE, AKS) and skips them automatically.
On a provider-managed cluster:

```bash
gwp crds install --skip-gateway-api-crds
```

Inspect what is installed and who manages it:

```bash
gwp crds detect
```

### 3. Install a pair

```bash
gwp pair install 1

# Custom prefix
gwp --prefix myapp pair install 1

# Pass extra chart values
gwp pair install 1 --set "gateway-helm.config.envoyGateway.extensionApis.enableEnvoyPatchPolicy=true"
```

`gwp pair install` automatically injects the three required per-pair flags:
- `controllerName`: unique per pair, prevents controllers fighting over each
  other's GatewayClasses
- `watch.type` and `watch.namespaces`: scopes the controller to its own two
  namespaces only

### 4. Get the coupling fields for Layer 3 manifests

```bash
gwp pair info 1
```

Output:
```
Pair 1:
  gatewayClassName:    tr-1
  dataplaneNamespace:  tr-dataplane-1
  allowedRoutes label: tr/gateway-routes=true
```

### 5. Apply Layer 3 resources

```bash
kubectl apply -n tr-dataplane-1 -f envoyproxies.yaml
kubectl apply -n tr-dataplane-1 -f gateways.yaml     # gatewayClassName: tr-1
kubectl apply -n tr-dataplane-1 -f httproutes.yaml
```

Each Gateway references the pair's GatewayClass and its own EnvoyProxy:

```yaml
spec:
  gatewayClassName: tr-1
  infrastructure:
    parametersRef:
      group: gateway.envoyproxy.io
      kind: EnvoyProxy
      name: <your-tier-name>   # must exist in tr-dataplane-1
```

### 6. Check status

```bash
# All pairs
gwp pair status

# One pair with Layer 3 detail
gwp pair status 1

# List releases
gwp pair list
```

### 7. Install multiple pairs

```bash
gwp pair install 1
gwp pair install 2
gwp pair install 3
```

Pairs are fully isolated. Each has its own controller, GatewayClass, and two
namespaces. Deleting or upgrading one pair has no effect on others.

### 8. Uninstall a pair

`gwp pair delete` handles the full teardown sequence:

```bash
gwp pair delete 1
```

**What it does internally:**

1. Deletes all Gateways in `tr-dataplane-{i}` with `--wait` so EG deprovisions
   the proxy Deployment before the controller exits.
2. Waits until EG-managed Deployments and Services are gone.
3. `helm uninstall` (removes the controller, ClusterRoles, both namespaces).
4. Deletes both namespaces explicitly.

**Why the sequence matters:**

Proxy pods have no Kubernetes finalizers; they use ownerReferences. But
`terminationGracePeriodSeconds` defaults to 360s (EG formula: `drainTimeout + 300s`).
Deleting the controller before the pod exits leaves the pod Terminating with nothing
to cancel its grace period. The namespace then blocks for up to 6 minutes.

Deleting the Gateway first lets EG remove the Deployment cleanly. Once the
Deployment is gone, any remaining pod is just waiting out its grace period.

**For fast teardown (no live connections):**

POST `/quitquitquit` to the Envoy admin API before calling `gwp pair delete`.
This triggers an immediate graceful shutdown: the pod exits as soon as the
connection drain completes, which is instant with no live traffic.

The admin API listens on `127.0.0.1:19000` (localhost-only). EG uses distroless
images (no shell), so `kubectl exec` won't work. Use port-forward:

```bash
# Find a Running proxy pod
PROXY_POD=$(kubectl get pods -n tr-dataplane-1 \
  -l app.kubernetes.io/managed-by=envoy-gateway \
  --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')

# Port-forward and send /quitquitquit
kubectl port-forward -n tr-dataplane-1 pod/$PROXY_POD 19000:19000 &
FWD_PID=$!
# Poll until tunnel is ready (CI runners can be slow to bind)
until curl -s -X POST --connect-timeout 1 http://127.0.0.1:19000/quitquitquit; do
  sleep 0.2
done
kill $FWD_PID

# Wait for pod to exit, then delete the pair
kubectl wait pod/$PROXY_POD -n tr-dataplane-1 --for=delete --timeout=30s
gwp pair delete 1
```

Must be called **before** `gwp pair delete` / Gateway deletion; port-forward to
a Terminating pod is unreliable once SIGTERM is delivered.

**Alternative: reduce drainTimeout in EnvoyProxy:**

```yaml
spec:
  shutdown:
    drainTimeout: "1s"   # terminationGracePeriodSeconds = 301s (not 360s)
```

Useful when `/quitquitquit` is impractical, but still 5 minutes minimum.

---

## Helm path

Use this when you need to inspect or customize chart templates directly, or when
integrating with an existing Helm-based CI pipeline.

### Prerequisites

```bash
kubectl
helm    # >= 3.14
k3d     # >= 5.7 (local dev only)
```

### 1. Create a cluster (local dev)

```bash
k3d cluster create gw-pairs \
  --agents 1 \
  --image rancher/k3s:v1.32.2-k3s1 \
  --k3s-arg --disable=traefik@server:*

# or via Makefile
make cluster
```

### 2. Install CRDs (once per cluster)

```bash
make crds-install
# or directly:
KTX=k3d-gw-pairs EG_VERSION=v1.8.0 ./hack/install-crds.sh
```

On a provider-managed cluster:

```bash
SKIP_GATEWAY_API_CRDS=1 KTX=my-context make crds-install
```

### 3. Install a pair

```bash
make pair-install PAIR=1
make pair-install PAIR=2 PAIR_PREFIX=myapp
```

Behind the scenes:

```bash
helm upgrade --install eg-pair-1 ./charts/eg-pair \
  --namespace tr-system-1 --create-namespace \
  --set pair.index=1 \
  --set pair.namePrefix=tr \
  --set "gateway-helm.config.envoyGateway.gateway.controllerName=gateway.envoyproxy.io/tr-1" \
  --set "gateway-helm.config.envoyGateway.provider.kubernetes.watch.type=Namespaces" \
  --set "gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces={tr-system-1,tr-dataplane-1}" \
  --skip-crds \
  --wait --timeout 120s
```

The three `gateway-helm.*` flags are required and cannot be derived by the chart
itself (Helm subchart values are resolved before template rendering). `make pair-install`
and `gwp pair install` both compute and inject them automatically.

### 4. Uninstall a pair

```bash
# 1. Delete Gateways first so EG deprovisions the proxy Deployment cleanly.
#    --wait blocks until EG removes the Deployment and clears ownerRefs.
kubectl delete gateways --all -n tr-dataplane-1 --wait=true --timeout=2m
kubectl delete envoyproxies --all -n tr-dataplane-1 --ignore-not-found

# 2. Wait for EG-managed Deployments and Services to be gone.
kubectl wait deployment \
  -l app.kubernetes.io/managed-by=envoy-gateway \
  -n tr-dataplane-1 --for=delete --timeout=2m

# 3. Uninstall the Helm release (removes controller, ClusterRoles, both namespaces).
helm uninstall eg-pair-1 -n tr-system-1

# 4. Delete both namespaces explicitly (helm uninstall may not remove the release NS).
kubectl delete namespace tr-system-1 tr-dataplane-1 --ignore-not-found --wait=false
```

For fast teardown with no live connections, send `/quitquitquit` to each proxy pod
before step 1 (see [CLI path section 8](#8-uninstall-a-pair) for the script).

---

## Troubleshooting

### GatewayClass stays `Unknown` after install

`controllerName` in the ConfigMap does not match the GatewayClass. Happens when
the three `gateway-helm.*` flags were not passed.

```bash
kubectl get configmap envoy-gateway-config -n tr-system-1 \
  -o jsonpath='{.data.envoy-gateway\.yaml}' | grep controllerName
# Should be: gateway.envoyproxy.io/tr-1
# If it shows: gateway.envoyproxy.io/gatewayclass-controller
# Fix: re-run gwp pair install 1  (or make pair-install PAIR=1)
```

### Namespace stuck `Terminating` after uninstall

Proxy pod finalizer was never cleared: the controller was removed before the
Gateway was deleted.

```bash
# Check for stuck finalizers
kubectl get pods -n tr-dataplane-1
kubectl describe pod <stuck-pod> -n tr-dataplane-1 | grep Finalizer

# Force-remove (last resort)
kubectl patch pod <stuck-pod> -n tr-dataplane-1 \
  -p '{"metadata":{"finalizers":[]}}' --type=merge
```

Always delete Gateways before uninstalling. `gwp pair delete` warns when
EG-managed Deployments still exist.

### Proxy not ready: `tokenreviews.authentication.k8s.io is forbidden`

The `tokenreviews` ClusterRole is missing. Reinstall the pair; the chart owns
this ClusterRole and binding.

### Controller logs `unknown namespace for the cache`

Two controllers share the same `controllerName`. Each pair must have a unique
name. Check all running controllers:

```bash
kubectl get configmap envoy-gateway-config --all-namespaces \
  -o jsonpath='{range .items[*]}{.metadata.namespace}: {.data.envoy-gateway\.yaml}{"\n"}{end}' \
  | grep controllerName
```

Re-install the conflicting pair with the correct index. `gwp pair install`
derives unique names automatically.

### Gateway `Programmed=False` with `AddressNotAssigned`

Expected for ClusterIP gateways (no external IP), so the top-level
`Gateway.status.conditions[Programmed]` stays False. Check listener-level
conditions instead:

```bash
kubectl get gateway eg-test -n tr-dataplane-1 \
  -o jsonpath='{range .status.listeners[*]}{range .conditions[*]}{.type}={.status} {end}{end}'
# Programmed=True when the proxy is connected and xDS is wired
```

---

## Reference

- [docs/design.md](docs/design.md): architecture, RBAC shape, watch list wiring, uninstall sequence
- [docs/api.md](docs/api.md): Go embedding API (`gwpapi` package)
- [e2e/README.md](e2e/README.md): e2e test suite, environment variables, test sequence
