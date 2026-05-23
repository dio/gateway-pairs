#!/usr/bin/env bash
# Install Gateway API + Envoy Gateway CRDs into the target context.
# Usage: KTX=k3d-gw-pairs-e2e EG_VERSION=v1.8.0 ./hack/install-crds.sh
set -euo pipefail

KTX="${KTX:-$(kubectl config current-context)}"
EG_VERSION="${EG_VERSION:-v1.8.0}"
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.2.1}"
CHANNEL="${CHANNEL:-standard}"

# Safety: reject non-k3d contexts unless UNSAFE_CONTEXT=1 is set.
if [[ "$KTX" != k3d-* ]] && [[ "${UNSAFE_CONTEXT:-0}" != "1" ]]; then
  echo "error: context '$KTX' is not a k3d context. Set UNSAFE_CONTEXT=1 to override." >&2
  exit 1
fi

echo "==> detecting existing Gateway API CRDs on $KTX"
EXISTING_VERSION=$(kubectl --context "$KTX" get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' 2>/dev/null || true)

if [[ -n "$EXISTING_VERSION" ]]; then
  echo "    found provider-managed Gateway API CRDs: $EXISTING_VERSION (skipping Gateway API CRDs)"
  INSTALL_GATEWAY_API="false"
else
  echo "    no provider-managed CRDs found, will install Gateway API $GATEWAY_API_VERSION ($CHANNEL channel)"
  INSTALL_GATEWAY_API="true"
fi

if [[ "$INSTALL_GATEWAY_API" == "true" ]]; then
  echo "==> installing Gateway API CRDs ($CHANNEL)"
  kubectl --context "$KTX" apply --server-side -f \
    "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
fi

echo "==> installing Envoy Gateway CRDs $EG_VERSION"
helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
  --version "$EG_VERSION" \
  --set crds.gatewayAPI.enabled=false \
  --set crds.envoyGateway.enabled=true \
  | kubectl --context "$KTX" apply --server-side -f -

echo "==> CRDs installed"
