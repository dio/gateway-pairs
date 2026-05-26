# Rate-Limit Deployment Control

**Date:** 2026-05-26  
**Status:** Implemented  
**Feature:** `--ratelimit-disabled` and `--ratelimit-image` flags for `gwp pair install`

## Overview

Gateway-pairs now exposes control over the Envoy Gateway rate-limit deployment:

1. **Disable rate-limit entirely** — useful for deployments that don't need rate limiting
2. **Override the rate-limit image** — useful for air-gapped or vendored container registries

Both features work via three integration points: chart values, CLI flags, and the Go API.

## Quick Start

### Via CLI

**Disable rate-limit:**
```bash
gwp pair install 1 --ratelimit-disabled
```

**Override image:**
```bash
gwp pair install 1 --ratelimit-image myregistry.io/ratelimit:v2.0
```

**Both:**
```bash
gwp pair install 1 --ratelimit-disabled --ratelimit-image myregistry.io/custom-ratelimit:latest
```

### Via Helm values file

```yaml
# custom-values.yaml
ratelimit:
  enabled: false
  image: "myregistry.io/ratelimit:v2.0"
```

Then install:
```bash
gwp pair install 1 --values custom-values.yaml
```

### Via Go API

```go
c := gwpapi.New(gwpapi.Options{Prefix: "tr"})
c.PairInstall(ctx, 1, gwpapi.PairInstallOptions{
  RatelimitDisabled: true,
  RatelimitImage:    "myregistry.io/ratelimit:v2.0",
})
```

### Via raw --set (escape hatch)

If you need more granular control, use `--set`:

```bash
gwp pair install 1 \
  --set gateway-helm.config.envoyGateway.provider.kubernetes.rateLimitDeployment.replicas=0 \
  --set gateway-helm.config.envoyGateway.provider.kubernetes.rateLimitDeployment.image.repository=myregistry.io/ratelimit \
  --set gateway-helm.config.envoyGateway.provider.kubernetes.rateLimitDeployment.image.tag=v2.0
```

## Implementation Details

### Chart Values

Added to `charts/eg-pair/values.yaml`:

```yaml
ratelimit:
  enabled: null          # null = use gateway-helm default
  image: null            # null = use gateway-helm default
```

### Helm Flags Generated

| Feature | Helm --set Flag |
|---------|-----------------|
| Disable | `gateway-helm.config.envoyGateway.provider.kubernetes.rateLimitDeployment.replicas=0` |
| Image repository | `gateway-helm.config.envoyGateway.provider.kubernetes.rateLimitDeployment.image.repository=<repo>` |
| Image tag | `gateway-helm.config.envoyGateway.provider.kubernetes.rateLimitDeployment.image.tag=<tag>` |

### Image Format

Both of these work:

```
myregistry.io/ratelimit:v2.0      # with tag
myregistry.io/ratelimit            # without tag (uses default from upstream)
```

The implementation uses `parseImageTag()` to split on the last `:` separator.

## Testing

The implementation was validated against gateway-helm v1.8.0:

- ✅ Setting `replicas=0` via `--set` successfully injects into the EnvoyGateway CRD config
- ✅ Envoy Gateway controller reads the config and creates the Deployment with 0 replicas
- ✅ No Helm chart modifications needed — works with v1.8.0 as-is

See `/Users/dio/src/dio/transit/RATELIMIT_TEST_QUICK_REFERENCE.txt` for test details.

## Integration Points Modified

| File | Change |
|------|--------|
| `charts/eg-pair/values.yaml` | Added `ratelimit.enabled` and `ratelimit.image` |
| `pair/pair.go` | Added `RatelimitDisabled` and `RatelimitImage` fields; added logic to generate `--set` flags |
| `gwpapi/gwpapi.go` | Exposed fields in `PairInstallOptions` and wired through to `pair.Install()` |
| `internal/cli/root.go` | Added `--ratelimit-disabled` and `--ratelimit-image` CLI flags |

## Examples

### Disable rate-limit in a single-pair dev setup

```bash
gwp pair install 1 --ratelimit-disabled
```

### Multi-pair with custom images

```bash
# Deploy 3 pairs with custom rate-limit image
gwp pair install 1 --ratelimit-image myregistry.io/ratelimit:v1.5
gwp pair install 2 --ratelimit-image myregistry.io/ratelimit:v1.5
gwp pair install 3 --ratelimit-image myregistry.io/ratelimit:v1.5
```

### Air-gapped deployment

When pulling from an internal registry with all images pre-loaded:

```bash
gwp pair install 1 \
  --ratelimit-image internal-registry.company.com:5000/ratelimit:latest
```

## Backward Compatibility

✅ **Fully backward compatible.**

- If neither `--ratelimit-disabled` nor `--ratelimit-image` are specified, gateway-helm's defaults are used
- Existing deployments continue to work without changes
- The feature is purely additive

## Future Enhancements

- Expose other gateway-helm configuration options (e.g., controller replicas, resource limits) via chart values
- Consider templating `values.yaml` to generate example multi-pair configs
