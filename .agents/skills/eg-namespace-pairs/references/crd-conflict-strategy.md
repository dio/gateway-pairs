# CRD Conflict Strategy

## Single version pin rule (critical)

Always use `gateway-crds-helm` for BOTH Gateway API and EG CRDs.
Never pull Gateway API from a separate `kubernetes-sigs/gateway-api` release URL.

EG version → Gateway API version mapping:
- EG v1.8.0 → Gateway API **v1.5.1** (standard), NOT v1.2.1

Verify bundled version:
```bash
helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
  --version v1.8.0 \
  --set crds.gatewayAPI.enabled=true \
  --set crds.gatewayAPI.channel=standard \
  --set crds.envoyGateway.enabled=false \
  2>/dev/null | grep 'bundle-version' | sort -u
# → gateway.networking.k8s.io/bundle-version: v1.5.1
```

## Provider-managed detection (correct approach)

`bundle-version` annotation is set by ANY install path (kubectl, helm, GKE, AKS).
Non-empty ≠ provider-managed. The correct signal is `managedFields[*].manager`.

```bash
# Step 1: is it installed at all?
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' \
  2>/dev/null
# Empty = not installed. Non-empty = installed by someone.

# Step 2: is it provider-managed?
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.managedFields[*].manager}' 2>/dev/null
```

Known provider manager names:
- `gke-networking-controller`, `gke-gateway-api` -- GKE Standard / Autopilot
- `aks-gateway-api-controller` -- AKS
- `addon-manager` -- GKE addon-manager pattern

## hack/install-crds.sh detection algorithm

```bash
EXISTING_VERSION=$(kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' 2>/dev/null || true)

if [[ -n "$EXISTING_VERSION" ]]; then
  # Check managedFields for known provider managers
  MANAGERS=$(kubectl get crd gateways.gateway.networking.k8s.io \
    -o jsonpath='{.metadata.managedFields[*].manager}' 2>/dev/null || echo "")
  
  for pm in gke-networking-controller gke-gateway-api aks-gateway-api-controller addon-manager; do
    if echo "$MANAGERS" | grep -qw "$pm"; then
      PROVIDER_MANAGED=true; break
    fi
  done
fi

if [[ "$PROVIDER_MANAGED" == "true" ]] || [[ "$SKIP_GATEWAY_API_CRDS" == "1" ]]; then
  echo "skipping Gateway API CRDs"
else
  # install from gateway-crds-helm
fi
```

## Four conflict scenarios

| Scenario | Action |
|---|---|
| Fresh cluster | `helm template gateway-crds-helm --set crds.gatewayAPI.enabled=true --set crds.envoyGateway.enabled=true \| kubectl apply --server-side -f -` |
| Provider-managed Gateway API | `helm template gateway-crds-helm --set crds.gatewayAPI.enabled=false --set crds.envoyGateway.enabled=true \| kubectl apply --server-side -f -` |
| User-installed, same channel | server-side apply upgrades cleanly |
| Channel mismatch (experimental → standard) | hard stop; check live TCPRoute/BackendTLSPolicy objects before proceeding |

## Install command (always server-side)

```bash
# Gateway API + EG CRDs (fresh cluster)
helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
  --version $EG_VERSION \
  --set crds.gatewayAPI.enabled=true \
  --set crds.gatewayAPI.channel=standard \
  --set crds.envoyGateway.enabled=false \
  | kubectl --context $KTX apply --server-side -f -

# EG CRDs only (provider-managed Gateway API)
helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
  --version $EG_VERSION \
  --set crds.gatewayAPI.enabled=false \
  --set crds.envoyGateway.enabled=true \
  | kubectl --context $KTX apply --server-side -f -
```

Direct `helm install` fails on large CRD bundles due to Helm's 1 MB release
secret annotation limit. Always use `helm template | kubectl apply --server-side`.

## Verify after install

```bash
kubectl get crd \
  gatewayclasses.gateway.networking.k8s.io \
  gateways.gateway.networking.k8s.io \
  httproutes.gateway.networking.k8s.io \
  referencegrants.gateway.networking.k8s.io \
  envoyproxies.gateway.envoyproxy.io \
  envoypatchpolicies.gateway.envoyproxy.io \
  backendtrafficpolicies.gateway.envoyproxy.io

# Check installed version
kubectl get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}'
# Should show v1.5.1 for EG v1.8.0
```

## Channel downgrade danger

Downgrading from experimental to standard removes `TCPRoute`, `BackendTLSPolicy`
CRDs. If live objects exist, this is data-loss. Always check before downgrading:

```bash
kubectl get tcproutes.gateway.networking.k8s.io -A 2>/dev/null
kubectl get backendtlspolicies.gateway.networking.k8s.io -A 2>/dev/null
```

## --force-gateway-api-crds on provider-managed clusters

Dangerous. The provider's control plane may reconcile CRDs back to its version
on the next node pool upgrade or maintenance window. EG then silently regresses
to a broken state. Document prominently; recommend using a newer provider channel
(GKE rapid, AKS preview) instead.
