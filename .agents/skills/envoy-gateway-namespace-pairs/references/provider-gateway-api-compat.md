# Provider-Managed Gateway API Compatibility

## The problem

EG v1.8.0 is built and tested against Gateway API v1.5.1. When a provider manages
Gateway API CRDs, the installed version may be older. Skipping installation is not
sufficient -- we need to check whether the provider version is compatible.

## Version floor by EG version

| EG Version | Bundled Gateway API | Minimum functional floor | Notes |
|---|---|---|---|
| v1.8.0 | v1.5.1 | v1.3.0 | v1.2.x missing .spec.infrastructure on Gateway |
| v1.3.x | ~v1.2.x | v1.1.0 | verify from EG release notes |

"Minimum functional floor" means EG starts and basic routing works. Features
may be degraded. "Bundled" is what `gateway-crds-helm` ships and what `gwp` embeds.

## What is missing at each version gap

### Provider at v1.2.x (e.g. older GKE Standard)

Missing (hard gaps -- EG functional impact):
- `listenersets.gateway.networking.k8s.io` -- EG logs watch error in healthz; controller healthz fails
- `.spec.infrastructure` on Gateway -- EG cannot attach infrastructure
- Several `*/status` subresource fields -- EG cannot report status correctly

Missing (soft gaps -- feature unavailable, EG continues):
- `BackendTLSPolicy` in standard channel (was experimental in v1.2)

### Provider at v1.3.x or v1.4.x

- `listenersets` still missing (added v1.5) -- soft gap, EG tolerates it with a warning
- Most required fields present

### Provider at v1.5.x+

Full compatibility with EG v1.8.0.

## CompatibilityMatrix Go type (internal/compat/matrix.go)

Baked into `gwp` at build time alongside embedded CRDs:

```go
type EGCompatibility struct {
    EGVersion string
    // MinGatewayAPIVersion: EG can start but features may be degraded
    MinGatewayAPIVersion string
    // TestedGatewayAPIVersion: what gateway-crds-helm ships; CI-tested
    TestedGatewayAPIVersion string
    // RequiredCRDs: missing any = hard block (EG won't start correctly)
    RequiredCRDs []string
    // OptionalCRDs: missing = feature unavailable, EG continues
    OptionalCRDs []string
    // RequiredFields: missing = hard block; probed against live CRD schema
    RequiredFields []CRDFieldProbe
}

type CRDFieldProbe struct {
    CRD   string // e.g. "gateways.gateway.networking.k8s.io"
    Field string // jsonpath into OpenAPI schema
    Since string // Gateway API version that added this field
}

// EG v1.8.0 entry:
var V1_8_0 = EGCompatibility{
    EGVersion:               "v1.8.0",
    MinGatewayAPIVersion:    "v1.3.0",
    TestedGatewayAPIVersion: "v1.5.1",
    RequiredCRDs: []string{
        "gateways.gateway.networking.k8s.io",
        "gatewayclasses.gateway.networking.k8s.io",
        "httproutes.gateway.networking.k8s.io",
        "referencegrants.gateway.networking.k8s.io",
    },
    OptionalCRDs: []string{
        "listenersets.gateway.networking.k8s.io", // added v1.5
        "backendtlspolicies.gateway.networking.k8s.io",
        "tcproutes.gateway.networking.k8s.io",
    },
    RequiredFields: []CRDFieldProbe{
        {
            CRD:   "gateways.gateway.networking.k8s.io",
            Field: ".spec.versions[?(@.name==\"v1\")].schema.openAPIV3Schema.properties.spec.properties.infrastructure",
            Since: "v1.3.0",
        },
    },
}
```

Entries are authored once per EG major version and baked in at build time.
They do not change within a minor series.

## gwp preflight output (provider-managed cluster example)

```
$ gwp preflight --id prod

[WARN] gateway-api CRDs managed by gke-networking-controller: v1.2.0 standard
       EG v1.8.0 tested against v1.5.1 -- checking compatibility

       Required CRDs:         all present
       Optional CRDs missing: listenersets.gateway.networking.k8s.io (added v1.5.0)
                              → ListenerSet feature unavailable
       Required fields:
         gateways.gateway.networking.k8s.io .spec.infrastructure  [added v1.3.0]
                              → MISSING: Gateway infrastructure attachment will not work

[FAIL] Provider Gateway API v1.2.0 is missing required fields for EG v1.8.0.
       Options:
         1. Upgrade GKE node pool to get Gateway API >= v1.3.0
         2. Use EG version compatible with v1.2.0 (eg v1.3.x or earlier)
         3. Install Gateway API CRDs yourself: gwp crds install --force-gateway-api-crds
            (takes ownership from provider -- may conflict with GKE reconciler)

[WARN] Option 3 is dangerous on provider-managed clusters. The provider may
       reconcile the CRDs back to v1.2.0 on node pool upgrade or maintenance.
```

## Detection algorithm for gwp preflight

```go
type CompatResult struct {
    ProviderVersion  string
    BundledVersion   string
    HardBlocks       []string  // missing required CRDs or fields
    SoftWarnings     []string  // missing optional CRDs
    ProviderManager  string    // empty if self-managed
}

// Step 1: check bundle-version annotation (installed = non-empty)
// Step 2: check managedFields[*].manager for known providers
// Step 3: parse semver from bundle-version
// Step 4: probe required CRDs (kubectl get crd <name> --ignore-not-found)
// Step 5: probe required fields via CRD OpenAPI schema
//   kubectl get crd gateways.gateway.networking.k8s.io
//     -o jsonpath='{.spec.versions[?(@.name=="v1")].schema.openAPIV3Schema
//                  .properties.spec.properties.infrastructure}'
//   Empty = field missing
```

## Known provider manager strings (2025-2026)

- `gke-networking-controller` -- GKE Standard Gateway API add-on
- `gke-gateway-api` -- GKE Autopilot
- `aks-gateway-api-controller` -- AKS managed Gateway API
- `addon-manager` -- GKE addon manager (may appear alongside gke-networking-controller)

Heuristic: if any `managedFields[*].manager` matches a known provider string,
treat as provider-managed.

Limitation: some providers do not set a distinctive manager string. In that case
fall back to checking for provider-specific annotations (e.g.
`addon.kubernetes.io/addon-name`, `gke.io/managed-by`). Document the limitation
in preflight output so operators know the heuristic can miss.

## `--force-gateway-api-crds` risk on provider-managed clusters

Installing our CRDs over provider-managed ones uses `kubectl apply --server-side`.
This changes the field manager from the provider controller to `kubectl`. On the
next node pool upgrade or cluster maintenance, the provider's controller sees a
CRD it no longer owns and may:
1. Silently ignore it (best case, version stays at ours)
2. Reconcile it back to the provider version (common on GKE)
3. Error and block the upgrade (rare but possible)

The result of (2) is that EG silently regresses to a broken version after a
maintenance event, potentially weeks after the original install. This is extremely
hard to diagnose.

**Recommendation:** never use `--force-gateway-api-crds` on provider-managed
clusters in production. Use a newer provider channel (GKE rapid, AKS preview)
to get a newer Gateway API version, or pin to an EG version compatible with
the provider's Gateway API version.

## GKE channel → Gateway API version mapping (approximate, verify at install time)

| GKE Channel | Approx Gateway API version |
|---|---|
| Stable | v1.1.x -- v1.2.x |
| Regular | v1.2.x -- v1.3.x |
| Rapid | v1.4.x -- v1.5.x |

Check actual version: `kubectl get crd gateways.gateway.networking.k8s.io -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}'`
