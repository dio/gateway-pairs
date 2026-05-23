#!/usr/bin/env bash
# Install Gateway API + Envoy Gateway CRDs into the target cluster.
#
# Usage:
#   KTX=k3d-gw-pairs-e2e EG_VERSION=v1.8.0 ./hack/install-crds.sh
#
# Flags (env vars):
#   KTX                     kubeconfig context (default: current-context)
#   EG_VERSION              Envoy Gateway version (default: v1.8.0)
#                           Also determines which Gateway API version is installed --
#                           gateway-crds-helm ships the exact Gateway API build EG expects.
#   CHANNEL                 standard|experimental (default: standard)
#   SKIP_GATEWAY_API_CRDS   Set to 1 to skip Gateway API CRDs (provider-managed clusters).
#                           Detection is automatic; use this to force-skip.
#   FORCE_GATEWAY_API        Set to 1 to install/upgrade Gateway API CRDs even when
#                           they already exist at a compatible version.
#   UNSAFE_CONTEXT          Set to 1 to allow non-k3d contexts.
set -euo pipefail

KTX="${KTX:-$(kubectl config current-context)}"
EG_VERSION="${EG_VERSION:-v1.8.0}"
CHANNEL="${CHANNEL:-standard}"
SKIP_GATEWAY_API_CRDS="${SKIP_GATEWAY_API_CRDS:-0}"
FORCE_GATEWAY_API="${FORCE_GATEWAY_API:-0}"

# ── context safety ────────────────────────────────────────────────────────────

if [[ "$KTX" != k3d-* ]] && [[ "${UNSAFE_CONTEXT:-0}" != "1" ]]; then
  echo "error: context '$KTX' is not a k3d context. Set UNSAFE_CONTEXT=1 to override." >&2
  exit 1
fi

# ── provider-managed detection ────────────────────────────────────────────────
# bundle-version annotation is set by ANY install (kubectl, helm, GKE, AKS).
# It does NOT indicate provider ownership by itself.
#
# Real signal: managedFields[*].manager. Known provider managers:
#   GKE:  gke-networking-controller, gke-gateway-api
#   AKS:  aks-gateway-api-controller
#   EKS:  (no managed gateway api as of 2025)
# We also skip the heuristic if SKIP_GATEWAY_API_CRDS=1 is set explicitly.
#
# Heuristic: if a field manager other than kubectl/helm/apply is found, the
# CRDs are likely provider-managed. We warn but do not block -- the operator
# can pass SKIP_GATEWAY_API_CRDS=1 to confirm.

KNOWN_USER_MANAGERS="kubectl|kubectl-client-side-apply|helm"
PROVIDER_MANAGERS=(
  gke-networking-controller
  gke-gateway-api
  aks-gateway-api-controller
  addon-manager
)

detect_gateway_api() {
  # Returns: "not-installed", "self-managed:<version>:<channel>", or "provider-managed:<manager>:<version>:<channel>"
  local crd="gateways.gateway.networking.k8s.io"

  local bundle_version
  bundle_version=$(kubectl --context "$KTX" get crd "$crd" \
    -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' \
    2>/dev/null || true)

  if [[ -z "$bundle_version" ]]; then
    echo "not-installed"
    return
  fi

  local channel
  channel=$(kubectl --context "$KTX" get crd "$crd" \
    -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/channel}' \
    2>/dev/null || echo "unknown")

  # Check managedFields for provider managers
  local managers
  managers=$(kubectl --context "$KTX" get crd "$crd" \
    -o jsonpath='{.metadata.managedFields[*].manager}' \
    2>/dev/null || echo "")

  local provider_manager=""
  for pm in "${PROVIDER_MANAGERS[@]}"; do
    if echo "$managers" | grep -qw "$pm"; then
      provider_manager="$pm"
      break
    fi
  done

  if [[ -n "$provider_manager" ]]; then
    echo "provider-managed:${provider_manager}:${bundle_version}:${channel}"
  else
    echo "self-managed:${bundle_version}:${channel}"
  fi
}

# ── main logic ────────────────────────────────────────────────────────────────

echo "==> Gateway API CRD detection on context: $KTX"
DETECT_RESULT=$(detect_gateway_api)

INSTALL_GATEWAY_API=true

if [[ "$SKIP_GATEWAY_API_CRDS" == "1" ]]; then
  echo "    SKIP_GATEWAY_API_CRDS=1: skipping Gateway API CRDs"
  INSTALL_GATEWAY_API=false
elif [[ "$DETECT_RESULT" == "not-installed" ]]; then
  echo "    not installed -- will install (channel: $CHANNEL)"
elif [[ "$DETECT_RESULT" == provider-managed:* ]]; then
  PMGR=$(echo "$DETECT_RESULT" | cut -d: -f2)
  PVER=$(echo "$DETECT_RESULT" | cut -d: -f3)
  PCH=$(echo "$DETECT_RESULT" | cut -d: -f4)
  echo "    provider-managed by $PMGR: $PVER ($PCH) -- skipping"
  echo "    (set FORCE_GATEWAY_API=1 or SKIP_GATEWAY_API_CRDS=1 to control explicitly)"
  INSTALL_GATEWAY_API=false
elif [[ "$DETECT_RESULT" == self-managed:* ]]; then
  SVER=$(echo "$DETECT_RESULT" | cut -d: -f2)
  SCH=$(echo "$DETECT_RESULT" | cut -d: -f3)
  echo "    self-managed: $SVER ($SCH)"

  if [[ "$SCH" != "$CHANNEL" ]]; then
    echo ""
    echo "error: channel mismatch -- installed: $SCH, requested: $CHANNEL" >&2
    echo "       Downgrading experimental -> standard removes TCPRoute, BackendTLSPolicy CRDs." >&2
    echo "       Check for live objects before proceeding:" >&2
    echo "         kubectl --context $KTX get tcproutes.gateway.networking.k8s.io -A" >&2
    echo "       Then re-run with CHANNEL=$SCH or FORCE_GATEWAY_API=1 to override." >&2
    exit 1
  fi

  if [[ "$FORCE_GATEWAY_API" == "1" ]]; then
    echo "    FORCE_GATEWAY_API=1: will upgrade"
  else
    echo "    already installed and channel matches -- skipping (set FORCE_GATEWAY_API=1 to upgrade)"
    INSTALL_GATEWAY_API=false
  fi
fi

# ── install Gateway API CRDs via gateway-crds-helm ───────────────────────────
# We use gateway-crds-helm for BOTH Gateway API and EG CRDs to ensure the
# versions are co-tested. gateway-crds-helm ships the exact Gateway API version
# EG was built and tested against.
#
# IMPORTANT: helm template | kubectl apply --server-side is required.
# Direct `helm install` fails on large CRD bundles due to Helm's 1 MB release
# secret annotation limit.

if [[ "$INSTALL_GATEWAY_API" == "true" ]]; then
  echo "==> installing Gateway API CRDs ($CHANNEL channel) from gateway-crds-helm $EG_VERSION"
  helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
    --version "$EG_VERSION" \
    --set crds.gatewayAPI.enabled=true \
    --set "crds.gatewayAPI.channel=$CHANNEL" \
    --set crds.envoyGateway.enabled=false \
    | kubectl --context "$KTX" apply --server-side -f -
else
  echo "==> skipping Gateway API CRDs"
fi

echo "==> installing Envoy Gateway CRDs $EG_VERSION"
helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
  --version "$EG_VERSION" \
  --set crds.gatewayAPI.enabled=false \
  --set crds.envoyGateway.enabled=true \
  | kubectl --context "$KTX" apply --server-side -f -

echo ""
echo "==> CRDs installed. Verify:"
echo "    kubectl --context $KTX get crd \\"
echo "      gatewayclasses.gateway.networking.k8s.io \\"
echo "      gateways.gateway.networking.k8s.io \\"
echo "      httproutes.gateway.networking.k8s.io \\"
echo "      envoyproxies.gateway.envoyproxy.io \\"
echo "      envoypatchpolicies.gateway.envoyproxy.io"
