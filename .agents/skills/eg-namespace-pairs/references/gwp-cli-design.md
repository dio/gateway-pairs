# `gwp` CLI Design

Canonical source: `docs/cli.md` in `github.com/dio/gateway-pairs`.
This is a condensed reference for key implementation decisions.

## Subcommand tree

```
gwp preflight [--id <id>]
gwp crds detect
gwp crds install [--skip-gateway-api-crds] [--force-gateway-api-crds]
                 [--allow-channel-downgrade] [--channel standard|experimental]
                 [--eg-version v1.8.0] [--dry-run]
gwp crds status
gwp pair install <id>   [--timeout 5m] [--dry-run] [--eg-version v1.8.0] [--chart ...]
gwp pair status  [id]   [--output text|json|yaml]
gwp pair verify  <id>   [--diagnose]
gwp pair delete  <id>
gwp pair list
gwp charts list
gwp charts export [--output-dir ./my-charts]
gwp charts show <chart>
gwp version
```

Note: pair identifier is a string `id`, not a numeric `index`. `id: "1"` is
valid (numeric as string). The chart value is `pair.id` (planned; currently
`pair.index` as int is still in use). See pair identity design notes below.

## Pair identity: string id, not numeric index

`pair.index` (int) is the current implementation but planned to evolve to
`pair.id` (string). The suffix IS the id -- arbitrary strings work:
- `id: "prod"` → `tr-system-prod`, `tr-dataplane-prod`, `tr-prod` (GatewayClass)
- `id: "1"` → `tr-system-1`, `tr-dataplane-1`, `tr-1` (backward compat)
- `id: "eu-west"` → `tr-system-eu-west`, etc.

Keep `pair.index` as a deprecated alias that sets `pair.id` when unset.
The CLI surface is `gwp pair install <id>` (string arg), so implement the
chart change before the CLI is wired.

## Already-installed preflight check

Three conflict types, each requiring different resolution:

**Type 1: Helm release already exists**
- `helm status eg-pair-{id} -n {prefix}-release-{id}` succeeds
- `status: deployed` → upgrade via `helm upgrade --install`
- `status: failed` → same command recovers

**Type 2: GatewayClass conflict without Helm release**
- `tr-{id}` exists but no `meta.helm.sh/release-name` annotation
- Hard block: delete the orphan, choose different id, or force-cleanup

**Type 3: Namespace conflict without Helm release**
- `tr-system-{id}` exists but no release secret in `tr-release-{id}`
- Helm refuses: "invalid ownership metadata"
- Fix: annotate namespace for Helm ownership (`--adopt-namespaces` flag) OR delete and retry

## Embedded assets: charts + pre-rendered CRDs

The embed lives in `charts/embed.go` (package `charts`):

```go
// charts/embed.go
package charts

//go:embed eg-crds eg-pair all:crds
var fs_ embed.FS

func CRDs() fs.FS { sub, _ := fs.Sub(fs_, "crds"); return sub }
func Charts() fs.FS { return fs_ }
```

`all:crds` is required because `charts/crds/.gitkeep` is a hidden file.
Without `all:`, the embed fails when only `.gitkeep` is present on a clean checkout.

Files in `charts/crds/`:
- `gateway-api-standard.yaml` -- pre-rendered, gitignored, generated at build
- `gateway-api-experimental.yaml` -- pre-rendered, gitignored
- `envoy-gateway.yaml` -- pre-rendered, gitignored
- `.gitkeep` -- committed; ensures embed compiles on fresh clone

Charts (`eg-crds/`, `eg-pair/`) are committed source, not generated. No copy step needed.

### Why pre-render CRDs but NOT the pair chart

CRDs: cluster-wide, version-pinned, zero per-install parameterization.
Pre-rendering makes `gwp crds install` hermetic and offline-capable.

Pair chart: must remain live because Helm release tracking is load-bearing.
`helm uninstall`, `helm upgrade`, `helm history` all depend on the release secret.

### Build process

```makefile
EG_VERSION ?= v1.8.0

generate-crds:
    @mkdir -p charts/crds
    helm template ... oci://docker.io/envoyproxy/gateway-crds-helm \
      --version $(EG_VERSION) \
      --set crds.gatewayAPI.enabled=true --set crds.gatewayAPI.channel=standard \
      --set crds.envoyGateway.enabled=false \
      > charts/crds/gateway-api-standard.yaml
    # ... same for experimental and envoy-gateway.yaml

build: generate-crds
    go build -ldflags="..." -o bin/gwp ./cmd/gwp
```

`charts/crds/*.yaml` is gitignored. `charts/crds/.gitkeep` is committed.

### Version baking

Two ldflags variables:
- `main.version` -- gwp release tag
- `main.egVersion` -- EG version used to generate embedded CRDs

`gatewayAPIBundleVersion` is NOT baked. Read at runtime from the
`gateway.networking.k8s.io/bundle-version` annotation in the embedded YAML.
Prevents drift on EG patch releases that bump the bundled Gateway API version.

## goreleaser setup

```yaml
before:
  hooks:
    - go mod tidy
    - make generate-crds EG_VERSION={{ .Env.EG_VERSION }}
```

`EG_VERSION` in release workflow `env:` block, not hardcoded in goreleaser config.

**goreleaser v2 has NO native Helm chart publishing stanza.** Use a separate
`publish-charts` job after goreleaser. See `goreleaser-homebrew-pattern.md`.

## gwp pair delete: correct sequence to avoid stuck Terminating namespaces

When the proxy Deployment is in the system namespace (GatewayNamespace mode
puts proxy in the Gateway's namespace), uninstalling without first removing
the Gateway leaves proxy finalizers dangling:

```
gwp pair delete prod:
  1. kubectl delete gateway eg -n tr-system-prod   -- EG cleans up proxy
  2. (wait ~5s for proxy Deployment deletion)
  3. helm uninstall eg-pair-prod -n tr-release-prod
  4. kubectl delete namespace tr-release-prod tr-system-prod tr-dataplane-prod --wait=false
  5. poll until all three namespaces gone
```

Skipping step 1 causes the namespace to hang Terminating indefinitely.

## What Helm cannot do (why gwp exists)

- Detect and skip provider-managed CRDs automatically
- Enforce install ordering (CRDs before pairs)
- Post-install verification with root-cause hints
- Orphan detection (pairs without a Helm release)
- CI gate: `gwp pair status --output json` exits 1 if any pair degraded
- Channel mismatch block with live-objects check instructions
- Correct pair delete sequence (delete Gateway before helm uninstall)

## Implementation constraints

- Go binary, statically linked, CGO_ENABLED=0.
- `exec.Command("helm", ...)` for install/uninstall. NOT the Helm Go SDK.
- `kubectl apply --server-side` shelled out for CRDs (server-side apply
  merge strategy not worth reimplementing; Gateway API v1.5+ has CEL rules).
- Everything else: `k8s.io/client-go` dynamic client.
- Non-k3d context rejected unless `--unsafe-context` passed.

## Exit codes

- 0: pass / succeeded
- 1: failure
- 2: usage error
