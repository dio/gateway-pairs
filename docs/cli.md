# CLI Design

A single binary, `gwp`, that wraps the operational surface of gateway-pairs:
preflight checks, CRD lifecycle management, pair install/status/teardown,
and post-install verification. It is a tool for operators and CI pipelines,
not a replacement for Helm -- Helm is still the install mechanism for the
`eg-pair` chart. `gwp` handles what Helm cannot: detection, validation,
ordering, and readable cluster state.

Every release of `gwp` ships with the charts and CRD manifests embedded
inside the binary. Operators do not need Helm repos, OCI registry access,
or separate chart downloads to install. They need one binary and `kubectl`.

---

## Embedded assets

The binary carries three asset groups, all via `//go:embed`:

```
internal/assets/
  charts/
    eg-crds/      -- our CRD metadata chart (full chart tree)
    eg-pair/      -- our pair chart (full chart tree)
  crds/
    gateway-api-standard.yaml     -- pre-rendered Gateway API standard CRDs
    gateway-api-experimental.yaml -- pre-rendered Gateway API experimental CRDs
    envoy-gateway.yaml            -- pre-rendered EG CRDs
```

The CRD YAML files under `internal/assets/crds/` are **generated at build
time**, not committed to the repo. They are produced by running
`helm template gateway-crds-helm` during the release build and embedded
via `//go:embed`. At runtime, `gwp crds install` pipes them straight to
`kubectl apply --server-side` -- no OCI registry access, no Helm required
for CRDs.

The charts under `internal/assets/charts/` ARE committed -- they are the
same files in `charts/`. A symlink or a `go generate` copy step keeps them
in sync; see the build section below.

### Why pre-render CRDs but not the pair chart

CRD YAML is cluster-wide, version-pinned, and has no per-install
parameterization. Pre-rendering it at build time is safe and makes
`gwp crds install` hermetic: the same binary on two different machines
installs identical CRD bytes regardless of network or OCI availability.

The `eg-pair` chart MUST be installed via Helm because Helm release tracking
is valuable: `helm uninstall`, `helm status`, `helm history`, and
`helm upgrade` all work correctly because Helm owns the release secret.
Pre-rendering it would lose that. Instead, at install time `gwp` extracts
the embedded chart to a temp dir and passes the path to `helm upgrade --install`.

---

## Build process

### Chart embedding (committed)

`internal/assets/charts/` mirrors `charts/` via a `go generate` step that
copies at build time:

```makefile
# Makefile
generate-assets: generate-crds
\t@mkdir -p internal/assets/charts
\t@cp -r charts/eg-crds internal/assets/charts/
\t@cp -r charts/eg-pair  internal/assets/charts/
```

The copy is cheap. The alternative (symlinks through `//go:embed`) does not
work -- `go:embed` does not follow symlinks.

### CRD generation (not committed, generated at build/release time)

```makefile
EG_VERSION ?= v1.8.0

generate-crds:
\t@mkdir -p internal/assets/crds
\thelm template gateway-api-crds oci://docker.io/envoyproxy/gateway-crds-helm \
\t  --version $(EG_VERSION) \
\t  --set crds.gatewayAPI.enabled=true \
\t  --set crds.gatewayAPI.channel=standard \
\t  --set crds.envoyGateway.enabled=false \
\t  > internal/assets/crds/gateway-api-standard.yaml
\thelm template gateway-api-crds oci://docker.io/envoyproxy/gateway-crds-helm \
\t  --version $(EG_VERSION) \
\t  --set crds.gatewayAPI.enabled=true \
\t  --set crds.gatewayAPI.channel=experimental \
\t  --set crds.envoyGateway.enabled=false \
\t  > internal/assets/crds/gateway-api-experimental.yaml
\thelm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
\t  --version $(EG_VERSION) \
\t  --set crds.gatewayAPI.enabled=false \
\t  --set crds.envoyGateway.enabled=true \
\t  > internal/assets/crds/envoy-gateway.yaml
\t@echo "generated CRDs for EG $(EG_VERSION)"

build: generate-assets
\tgo build -ldflags="-X main.version=$(VERSION) -X main.egVersion=$(EG_VERSION)" \
\t  -o bin/gwp ./cmd/gwp
```

`internal/assets/crds/` is in `.gitignore`. CI runs `make generate-crds`
before `make build`. Local builds also run it; the rule is idempotent.

### .gitignore additions

```
internal/assets/crds/
internal/assets/charts/
bin/
```

### Version baking

Three values baked in at link time:

```go
// cmd/gwp/main.go
var (
    version   = "dev"      // gwp release tag, e.g. v0.1.0
    egVersion = "v1.8.0"   // EG version the CRDs were generated from
    // gatewayAPIVersion is read at runtime from the embedded CRD annotations
    // to avoid a fourth baked variable that can drift.
)
```

`gwp version` output:

```
gwp v0.1.0
  bundled eg-version:          v1.8.0
  bundled gateway-api-version: v1.5.1 (standard)
  chart eg-crds:               0.1.0
  chart eg-pair:               0.1.0
```

`gatewayAPIBundleVersion` is extracted at runtime by parsing the
`gateway.networking.k8s.io/bundle-version` annotation from the first CRD
document in `gateway-api-standard.yaml`. No extra baked variable needed.

---

## gwp charts subcommand

Exports the embedded charts to disk for operators who prefer direct Helm
workflows or need to inspect, diff, or customize the templates.

```
gwp charts
  list            list embedded charts with versions
  export          export all charts to a directory
  show <chart>    equivalent of: helm show values <chart>
```

### gwp charts list

```
$ gwp charts list

CHART     VERSION  APP-VERSION
eg-crds   0.1.0    v1.8.0
eg-pair   0.1.0    v1.8.0

Bundled CRDs:
  Gateway API  v1.5.1  standard
  Gateway API  v1.5.1  experimental
  Envoy Gateway v1.8.0
```

### gwp charts export

```
$ gwp charts export --output-dir ./my-charts

Exporting charts to ./my-charts/
  wrote ./my-charts/eg-crds/
  wrote ./my-charts/eg-pair/

To install manually:
  kubectl apply --server-side -f <(helm template ./my-charts/eg-crds)
  helm upgrade --install eg-pair-1 ./my-charts/eg-pair \
    --namespace tr-system-1 --create-namespace \
    --set pair.index=1 --skip-crds
```

### gwp charts show

```
$ gwp charts show eg-pair

# Default values for eg-pair.
pair:
  index: 1
controller:
  image:
    repository: docker.io/envoyproxy/gateway
    tag: v1.8.0
...
```

Implemented as `helm show values <tmpdir/chart>` where `<tmpdir>` is the
embedded chart extracted to a temp dir. Or, since `values.yaml` is a
known file in the embedded FS, read it directly without shelling to Helm.

---

## gwp crds install with embedded CRDs

Once embedded CRDs exist, `gwp crds install` no longer calls Helm or hits
OCI at runtime. It reads from the embedded FS and pipes to kubectl:

```go
// internal/crd/install.go

//go:embed ../../internal/assets/crds
var embeddedCRDs embed.FS

func InstallGatewayAPICRDs(ctx context.Context, channel, kubectlContext string) error {
    filename := "internal/assets/crds/gateway-api-standard.yaml"
    if channel == "experimental" {
        filename = "internal/assets/crds/gateway-api-experimental.yaml"
    }
    data, err := embeddedCRDs.ReadFile(filename)
    if err != nil {
        return err
    }
    return applyServerSide(ctx, kubectlContext, bytes.NewReader(data))
}

func InstallEGCRDs(ctx context.Context, kubectlContext string) error {
    data, err := embeddedCRDs.ReadFile("internal/assets/crds/envoy-gateway.yaml")
    if err != nil {
        return err
    }
    return applyServerSide(ctx, kubectlContext, bytes.NewReader(data))
}

func applyServerSide(ctx context.Context, kubectlContext string, r io.Reader) error {
    cmd := exec.CommandContext(ctx,
        "kubectl", "--context", kubectlContext,
        "apply", "--server-side", "-f", "-",
    )
    cmd.Stdin = r
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}
```

No `io.Pipe` needed -- the embedded bytes are already in memory. The
`helm template | kubectl` pipe (documented in the previous section) is
only needed for the PoC phase before `generate-crds` is wired up.

---

## gwp pair install with embedded chart

The `eg-pair` chart is extracted to a temp dir and passed to Helm:

```go
// internal/pair/install.go

//go:embed ../../internal/assets/charts
var embeddedCharts embed.FS

func extractChart(name string) (string, func(), error) {
    dir, err := os.MkdirTemp("", "gwp-chart-*")
    if err != nil {
        return "", nil, err
    }
    cleanup := func() { os.RemoveAll(dir) }

    root := "internal/assets/charts/" + name
    err = fs.WalkDir(embeddedCharts, root, func(path string, d fs.DirEntry, err error) error {
        if err != nil || d.IsDir() {
            return err
        }
        rel, _ := filepath.Rel(root, path)
        dst := filepath.Join(dir, rel)
        if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
            return err
        }
        data, err := embeddedCharts.ReadFile(path)
        if err != nil {
            return err
        }
        return os.WriteFile(dst, data, 0644)
    })
    if err != nil {
        cleanup()
        return "", nil, err
    }
    return dir, cleanup, nil
}

func Install(ctx context.Context, index int, opts InstallOpts) error {
    chartDir, cleanup, err := extractChart("eg-pair")
    if err != nil {
        return err
    }
    defer cleanup()

    sysNS := fmt.Sprintf("tr-system-%d", index)
    release := fmt.Sprintf("eg-pair-%d", index)

    args := []string{
        "upgrade", "--install", release, chartDir,
        "--kube-context", opts.Context,
        "--namespace", sysNS,
        "--create-namespace",
        "--set", fmt.Sprintf("pair.index=%d", index),
        "--skip-crds",
        "--wait", "--timeout", opts.Timeout.String(),
    }
    for _, kv := range opts.ExtraSet {
        args = append(args, "--set", kv)
    }

    cmd := exec.CommandContext(ctx, "helm", args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("helm upgrade --install: %w", err)
    }

    return verify(ctx, index, opts.Context, opts.Timeout)
}
```

Temp dir cleanup is deferred. If Helm exits non-zero the error is
propagated and the temp dir is cleaned. No chart files are left on disk
after the command returns.

---

## Upgrade path

When a new `gwp` release ships with updated charts or CRD versions:

```bash
# upgrade CRDs first (embedded, no OCI needed)
gwp crds install --force-gateway-api-crds

# re-install pairs with the new chart (embedded)
gwp pair install 1
gwp pair install 2

# verify
gwp pair status
```

`gwp pair install` is idempotent -- it runs `helm upgrade --install`, which
upgrades an existing release if one exists.

The operator does not need to know which chart version changed, which CRD
fields were added, or which EG image tag to use. The binary is the version.

---

## Air-gapped installs

`gwp crds install` and `gwp pair install` both work without internet access:
- CRD YAML is embedded and applied directly to the cluster
- The `eg-pair` chart is extracted from the binary and passed to local Helm

The only external dependency is the EG controller image
(`docker.io/envoyproxy/gateway:v1.8.0`). In air-gapped environments, mirror
that image to an internal registry and override:

```bash
gwp pair install 1 --set controller.image.repository=registry.internal/envoyproxy/gateway
```

---

## Release checklist

1. Bump `EG_VERSION` in Makefile if upgrading EG.
2. Run `make generate-crds` locally and inspect the diff of the generated YAML.
3. Run `make build` -- binary embeds fresh CRDs and charts.
4. Run `make e2e` -- installs from embedded assets, not from OCI.
5. Tag and push -- CI builds the release binary with the same `EG_VERSION`.
6. `gwp version` on the release binary should show the correct bundled versions.


## Why a CLI

The Makefile + `hack/install-crds.sh` combination works for a developer on
a single machine, but breaks down when:

- The install order matters and is not enforced (CRDs before pairs, both
  namespaces in the watch list before the controller starts).
- A cluster has provider-managed Gateway API CRDs and the operator has to
  manually detect whether to skip them or not.
- N pairs are installed and you need a single-pane view of which are healthy,
  which are stuck, and why.
- CI needs a non-zero exit code when the cluster is not ready for the workload.
- A new pair would conflict with an existing GatewayClass name or a Namespace
  that was not created by Helm.

`gwp` solves the detection and ordering problems, expresses them as clear
error messages, and exposes the full install+verify lifecycle as composable
subcommands.

---

## Binary name and repo placement

```
gwp                     (short: gateway pairs)
cmd/gwp/main.go         entry point
internal/
  kube/                 kube client, context validation
  crd/                  CRD detection + install logic
  pair/                 pair status, install, verify
  preflight/            preflight check runners
```

Single statically-linked binary. No runtime dependencies beyond a valid
kubeconfig and `helm` on PATH for the install subcommands. For the pure
read/detect subcommands (`preflight`, `status`) only a kubeconfig is needed.

---

## Subcommand tree

```
gwp
  preflight             run all preflight checks before installing anything
  crds
    detect              show what Gateway API / EG CRDs exist and who manages them
    install             apply CRDs with the right strategy for this cluster
    status              show installed bundle versions and channels
  pair
    install <index>     install one pair (wraps helm, adds post-install verify)
    status [index]      show health of one pair or all pairs (Layer 2 + Layer 3)
    info <index>        print coupling fields for writing Layer 3 manifests
    verify <index>      re-run post-install checks without reinstalling
    delete <index>      uninstall a pair and clean up cluster-scoped resources
    list                list all pairs detected in the cluster
  charts
    list                list embedded charts and bundled CRD versions
    export              export embedded charts to a directory
    show <chart>        print default values for an embedded chart
  version               print gwp version
```

---

## `gwp preflight`

Runs a battery of checks before any install. Exits non-zero on the first
blocking failure. Non-blocking warnings are printed but do not fail.

Checks, in order:

### 1. Context safety

Reject any non-k3d context unless `--unsafe-context` is passed. Rationale:
all e2e and dev flows use k3d clusters; a production operator should explicitly
acknowledge they are targeting a real cluster.

```
[OK]   context: k3d-gw-pairs-e2e
[WARN] context is not a k3d cluster; pass --unsafe-context to proceed
```

`--unsafe-context` sets a flag, does not suppress the warning.

### 2. Server reachability + version

```
[OK]   server reachable: v1.32.2
[FAIL] server unreachable: connection refused
```

Minimum server version: 1.28 (Gateway API v1 GA requires it).

### 3. Current user RBAC

Check whether the authenticated user (or ServiceAccount) has the permissions
needed to install the charts. This is a dry-run SelfSubjectAccessReview for
each of:

- `namespaces` create (cluster-scoped)
- `clusterroles` create
- `clusterrolebindings` create
- `roles` create in target namespaces
- `rolebindings` create in target namespaces
- `deployments` create in target namespaces

```
[OK]   can create namespaces
[OK]   can create clusterroles
[FAIL] cannot create clusterrolebindings: forbidden
```

### 4. Gateway API CRD detection

Checks for `gateways.gateway.networking.k8s.io`. Reads the
`gateway.networking.k8s.io/bundle-version` and
`gateway.networking.k8s.io/channel` annotations, then inspects
`managedFields[*].manager` for provider fingerprints.

**Critical: `bundle-version` alone is not a provider-managed signal.**
That annotation is written by ANY install path -- `kubectl apply`, `helm
template`, GKE, AKS, everyone. Non-empty means "installed", nothing more.
The real signal is the field manager name in `managedFields`.

Known provider managers (checked by `gwp`):
- `gke-networking-controller`, `gke-gateway-api` -- GKE Standard
- `aks-gateway-api-controller` -- AKS
- `addon-manager` -- GKE autopilot / addon-manager pattern

Four outcomes:

| State | Signal | Recommended action |
|---|---|---|
| Not installed | `bundle-version` absent | install with `gwp crds install` |
| Self-managed, same channel | bundle-version present, known manager | skip (already correct) or `--force-gateway-api-crds` to upgrade |
| Provider-managed | known provider manager in managedFields | auto-skip; `gwp crds install` installs only EG CRDs |
| Channel mismatch | installed channel != requested channel | block: downgrading removes CRDs with live objects |

```
[OK]   gateway-api CRDs not installed -- will install (from gateway-crds-helm v1.8.0)
[OK]   gateway-api CRDs v1.5.1 standard already installed -- skipping (--force-gateway-api-crds to upgrade)
[WARN] gateway-api CRDs managed by gke-networking-controller (v1.5.1 standard) -- skipping
[FAIL] gateway-api CRDs on experimental channel; requested standard
       downgrading removes TCPRoute/BackendTLSPolicy CRDs -- check for live objects first
       pass --allow-channel-downgrade to proceed (dangerous)
```

**Gateway API version is not a separate flag.** `gwp crds install` always
installs the Gateway API version bundled by `gateway-crds-helm` at the
requested `--eg-version`. This ensures the Gateway API and EG CRDs are
always the co-tested pair. There is no `--gateway-api-version` flag.

### 5. EG CRD detection

Check for `envoyproxies.gateway.envoyproxy.io`. If missing, `gwp crds install`
is required.

```
[OK]   envoy-gateway CRDs v1.8.0 installed
[WARN] envoy-gateway CRDs not installed -- run: gwp crds install
```

### 6. Cluster-scoped resource conflicts (if `--pair <index>` passed)

Cluster-scoped resources must be unique per pair. Two pairs sharing any of
these will conflict silently or loudly depending on the resource type. `gwp`
checks all of them before install.

#### GatewayClass name

`tr-{i}` must not exist unless it is owned by this Helm release. If it exists
and belongs to another release (or was created out of band), installing will
fail or produce a broken state where two controllers claim the same class.

```
[OK]   GatewayClass tr-1 not found
[FAIL] GatewayClass tr-1 exists, owner: eg-pair-2 (different release)
       Two controllers cannot share a GatewayClass name.
       Options:
         Choose a different pair index: gwp pair install --id 3
         Remove the conflicting resource: kubectl delete gatewayclass tr-1
```

#### controllerName uniqueness

Each EG controller uses a `controllerName` derived from its GatewayClass name
(`gateway.envoyproxy.io/tr-{i}`). This value is baked into the
`envoy-gateway-config` ConfigMap in the system namespace.

If two controllers share a `controllerName`, each one watches all GatewayClasses
cluster-wide and tries to reconcile GatewayClasses that belong to the other
controller. Since each controller's cache only covers its own two namespaces,
it cannot find the other pair's EnvoyProxy and logs continuously:

```
failed to find envoyproxy tr-system-2/eg for GatewayClass tr-2:
unable to get: tr-system-2/eg because of unknown namespace for the cache
```

The scan: list all ConfigMaps named `envoy-gateway-config` across all
namespaces and extract the `controllerName` field. This requires only
`get configmap` across namespaces, which the kubeconfig user typically has.

```
[OK]   controllerName gateway.envoyproxy.io/tr-1 not in use by any other controller
[FAIL] controllerName gateway.envoyproxy.io/tr-1 already in use
       ConfigMap envoy-gateway-config in namespace tr-system-3 uses the same value.
       This pair would conflict with the controller in tr-system-3.
       Resolve by choosing a different pair id (--id 4) or removing the
       conflicting pair first.
```

#### ClusterRole names

`eg-pair-tr-{i}-tokenreviews` and `eg-pair-tr-{i}-gateway-controller` must not
exist unless owned by this release. Stale ClusterRoles from a previously failed
or force-deleted install indicate unclean state. They are not a hard block (RBAC
is additive and the new install will overwrite them) but they signal that a
previous uninstall did not clean up correctly.

```
[OK]   ClusterRole eg-pair-tr-1-tokenreviews not found
[WARN] ClusterRole eg-pair-tr-1-tokenreviews exists without a matching Helm release
       This is stale state from a previous install. The new install will overwrite it.
       To clean up manually: kubectl delete clusterrole eg-pair-tr-1-tokenreviews
```

#### Summary table

| Resource | Conflict type | Action on conflict |
|---|---|---|
| `GatewayClass tr-{i}` | Hard block | Different id or delete the existing resource |
| `controllerName tr-{i}` | Hard block | Different id or remove the conflicting controller |
| `ClusterRole eg-pair-tr-{i}-*` | Soft warn | New install overwrites; stale state only |
| `ClusterRoleBinding eg-pair-tr-{i}-*` | Soft warn | Same |

### 7. Namespace conflicts (if `--pair <index>` passed)

Check if `tr-system-{i}` or `tr-dataplane-{i}` exist and are not Helm-managed.

```
[OK]   namespaces tr-system-1 and tr-dataplane-1 do not exist
[WARN] namespace tr-system-1 exists -- will be adopted by Helm install
[FAIL] namespace tr-system-1 exists and is not managed by eg-pair-1
```

### 8. Pair index uniqueness

If `--pair <index>` is passed, check no other pair with the same index is
already installed (i.e. no `eg-pair-{index}` Helm release in any namespace).

```
[OK]   no existing release eg-pair-1
[FAIL] Helm release eg-pair-1 already installed in tr-system-1
       use: gwp pair status 1   to inspect
       use: gwp pair delete 1   to remove
```

### Full preflight output example

```
$ gwp preflight --pair 1

Preflight checks for pair 1 on context k3d-gw-pairs-e2e
-------------------------------------------------------
[OK]   context: k3d-gw-pairs-e2e (k3d)
[OK]   server reachable: v1.32.2
[OK]   can create namespaces, clusterroles, clusterrolebindings
[OK]   gateway-api CRDs not installed -- will install v1.5.1 (standard)
[OK]   envoy-gateway CRDs not installed -- run: gwp crds install
[OK]   GatewayClass tr-1 does not exist
[OK]   namespaces tr-system-1, tr-dataplane-1 do not exist
[OK]   no existing release eg-pair-1

1 warning, 0 failures. Ready to install.

  gwp crds install
  gwp pair install 1
```

When all checks pass it prints the recommended next commands. Operators can
copy-paste the output into a runbook.

---

## `gwp crds detect`

Read-only. Shows what is installed, who manages it, and what version.

```
$ gwp crds detect

Gateway API CRDs:
  gateways.gateway.networking.k8s.io
    bundle-version: v1.5.1
    channel:        standard
    managed-by:     helm (field manager: helm)

  httproutes.gateway.networking.k8s.io
    bundle-version: v1.5.1
    channel:        standard

Envoy Gateway CRDs:
  envoyproxies.gateway.envoyproxy.io
    version: v1.8.0 (from label app.kubernetes.io/version)

  envoypatchpolicies.gateway.envoyproxy.io
    version: v1.8.0
```

On a GKE cluster with managed Gateway API:

```
Gateway API CRDs:
  gateways.gateway.networking.k8s.io
    bundle-version: v1.2.0
    channel:        standard
    managed-by:     provider (field managers: gke-networking-controller)
    NOTE: do not overwrite; use --skip-gateway-api-crds on install
```

---

## `gwp crds install`

Applies CRDs using `helm template | kubectl apply --server-side` (the correct
path for large bundles). Runs `gwp crds detect` first and acts on the result.

Flags:
- `--skip-gateway-api-crds` -- skip Gateway API CRDs regardless of detect result
- `--force-gateway-api-crds` -- install/upgrade Gateway API CRDs even when already present
- `--allow-channel-downgrade` -- allow experimental -> standard downgrade (dangerous)
- `--channel standard|experimental` -- default: standard
- `--eg-version v1.8.0` -- EG version; also determines which Gateway API version is installed
- `--dry-run` -- validate manifests against the cluster without persisting (server-side dry-run)

The Gateway API version is not a separate input. `gateway-crds-helm` at `--eg-version`
ships the exact co-tested pair. For EG v1.8.0 that is Gateway API v1.5.1.
```
$ gwp crds install

Detecting existing CRDs...
  gateway-api: not installed
  envoy-gateway: not installed

Installing gateway-api v1.5.1 (standard) ...  done
Installing envoy-gateway v1.8.0 ...            done

CRDs ready. Run: gwp pair install 1
```

On a provider-managed cluster:

```
$ gwp crds install

Detecting existing CRDs...
  gateway-api: v1.2.0 standard -- provider-managed (skipping)
  envoy-gateway: not installed

Installing envoy-gateway v1.8.0 ...  done

CRDs ready. Run: gwp pair install 1
```

---

## `gwp pair install <index>`

Wraps `helm upgrade --install eg-pair-{i}`, then runs post-install verification.
Exits non-zero if verification fails within the timeout.

### What `gwp pair install` computes

The chart depends on `gateway-helm` as a subchart. Two values cannot be static
chart defaults because they are unique per pair. `gwp pair install` derives them
from the pair identity and passes them as `--set` flags to Helm automatically.

**`controllerName`** (derived from GatewayClass name, unique per pair):

```
gateway.envoyproxy.io/{prefix}-{id}
```

Without uniqueness, controllers fight over each other's GatewayClasses across
the cluster. See the controller isolation section in `docs/design.md`.

**`watch.namespaces`** (the pair's two namespaces):

```
[{prefix}-system-{id}, {prefix}-dataplane-{id}]
```

The controller must watch both to read its own TLS secret (system NS) and to
manage Gateways, EnvoyProxies, and proxy resources (dataplane NS).

The full Helm invocation `gwp pair install 1` produces:

```
helm upgrade --install eg-pair-1 <chart> \
  --namespace tr-system-1 --create-namespace \
  --set "gateway-helm.config.envoyGateway.gateway.controllerName=gateway.envoyproxy.io/tr-1" \
  --set "gateway-helm.config.envoyGateway.provider.kubernetes.watch.type=Namespaces" \
  --set "gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces={tr-system-1,tr-dataplane-1}" \
  --skip-crds
```

All other `gateway-helm` values (`deploy.type: GatewayNamespace`,
`topologyInjector.enabled: false`) are static defaults in `eg-pair/values.yaml`
and need no per-install override.

### Flags

- `--timeout 3m` -- total verification timeout (default: 3m)
- `--chart ./charts/eg-pair` -- override chart source; accepts a local path or OCI ref.
  Default: embedded chart extracted to a temp dir; no network access needed.
- `--eg-version v1.8.0` -- controller image tag
- `--dry-run` -- render and validate manifests without applying
- `--set key=value` -- passed through to helm

```
$ gwp pair install 1

Installing eg-pair-1 into tr-system-1...

Waiting for controller (tr-system-1/envoy-gateway) to be Available...  ok (23s)
Waiting for GatewayClass tr-1 to be Accepted...                        ok (2s)

Pair 1 ready. Apply Layer 3 resources:

  kubectl apply -n tr-dataplane-1 -f envoyproxies.yaml
  kubectl apply -n tr-dataplane-1 -f gateways.yaml     # gatewayClassName: tr-1
  kubectl apply -n tr-dataplane-1 -f httproutes.yaml

  gwp pair info 1   for the exact values needed in your Gateway manifests.
```


If Gateway gets stuck at Accepted-but-not-Programmed (the watch list failure
mode), `gwp` detects it, reads the ConfigMap, and prints a diagnostic:

```
[FAIL] Gateway eg/tr-system-1 stuck at Accepted=True, Programmed=False after 60s

  Diagnosis: EnvoyGateway ConfigMap watch list may be incomplete.
  Current watch namespaces:
    - tr-dataplane-1
  Expected:
    - tr-system-1   <-- MISSING (controller needs this to read its own TLS secret)
    - tr-dataplane-1

  Fix: gwp pair install 1 --set watch.mode=Namespaces
       (this will re-apply the ConfigMap with both namespaces)
```

If tokenreviews is missing:

```
[FAIL] Envoy proxy pods in tr-dataplane-1 not ready after 90s

  Diagnosis: EG controller logs show tokenreviews forbidden.
  tokenreviews ClusterRoleBinding eg-pair-1-tokenreviews:
    subjects: [envoy-gateway/tr-system-1]  -- present
  tokenreviews ClusterRole eg-pair-1-tokenreviews:
    rules: [authentication.k8s.io/tokenreviews create]  -- present

  This is unexpected. Run:
    kubectl describe clusterrolebinding eg-pair-1-tokenreviews
    kubectl logs -n tr-system-1 deploy/envoy-gateway | grep -i token
```

---

## `gwp pair status [index]`

Shows the current health of one pair or all pairs. Reports both Layer 2
(chart-managed infrastructure) and Layer 3 (operator-applied tiers). Read-only.

```
$ gwp pair status

PAIR  SYSTEM-NS     DATAPLANE-NS     GW-CLASS  CONTROLLER  L2-GATEWAY  L3-GATEWAYS  PROXIES
1     tr-system-1   tr-dataplane-1   tr-1      Available   Programmed  3            3/3
2     tr-system-2   tr-dataplane-2   tr-2      Available   Programmed  2            2/2
3     tr-system-3   tr-dataplane-3   tr-3      Available   Accepted    0            0/0  <--
```

For a single pair with full Layer 3 detail:

```
$ gwp pair status 1

Pair 1 (tr-1):
  System namespace:    tr-system-1
  Dataplane namespace: tr-dataplane-1
  GatewayClass:        tr-1              Accepted=True
  controllerName:      gateway.envoyproxy.io/tr-1

Layer 2 (chart-managed):
  Controller:          envoy-gateway     Available (1/1)

Layer 3 (operator-managed in tr-dataplane-1):
  EnvoyProxy  l1    (envoyService.name: l1,   image: custom-envoy:v1)
  EnvoyProxy  l2-a  (envoyService.name: l2-a, image: custom-envoy:v1)
  EnvoyProxy  l2-b  (envoyService.name: l2-b, image: custom-envoy:v1)

  Gateway     l1    gatewayClassName: tr-1  parametersRef: l1    Programmed=True
  Gateway     l2-a  gatewayClassName: tr-1  parametersRef: l2-a  Programmed=True
  Gateway     l2-b  gatewayClassName: tr-1  parametersRef: l2-b  Programmed=True

  Proxy Deployments (generated by EG):
    envoy-tr-dataplane-1-l1-<hash>    1/1
    envoy-tr-dataplane-1-l2-a-<hash>  1/1
    envoy-tr-dataplane-1-l2-b-<hash>  1/1

  HTTPRoutes: 3
    l1-public     parentRefs: [l1]
    l2-a          parentRefs: [l2-a]
    l2-b          parentRefs: [l2-b]
```

Layer 3 resources are discovered by listing Gateways, EnvoyProxies, and
HTTPRoutes in `tr-dataplane-{i}` that reference the pair's GatewayClass.
`gwp` does not own them; it only reads and reports their state.

### What `gwp pair status` validates for Layer 3

For each operator-applied Gateway in the dataplane namespace:

- `gatewayClassName` matches the pair's GatewayClass (`tr-{i}`)
- `infrastructure.parametersRef` points at an existing EnvoyProxy in the
  same namespace
- The referenced EnvoyProxy exists
- The Gateway is `Programmed=True`
- The generated proxy Deployment is ready

A Gateway with a mismatched `gatewayClassName` (pointing at a different pair
or a nonexistent GatewayClass) is flagged:

```
[WARN] Gateway tr-dataplane-1/l1: gatewayClassName=tr-99 does not match this pair (tr-1)
       This Gateway is not managed by the tr-1 controller.
```

A Gateway with a missing `parametersRef` target is flagged:

```
[WARN] Gateway tr-dataplane-1/l2-a: infrastructure.parametersRef points at
       EnvoyProxy tr-dataplane-1/l2-a which does not exist.
       Gateway will stay Programmed=False until the EnvoyProxy is applied.
```

### Status output for CI

```
$ gwp pair status --output json

[
  {"index":1,"controller":"Available","gatewayClass":"Accepted",
   "l3Gateways":[
     {"name":"l1","programmed":true,"envoyProxy":"l1","proxyReady":true},
     {"name":"l2-a","programmed":true,"envoyProxy":"l2-a","proxyReady":true}
   ]},
  ...
]
```

Exit code: 0 if all pairs and all Layer 3 Gateways healthy, 1 if any degraded.

---

## `gwp pair info <index>`

Prints the coupling fields an operator needs when writing Layer 3 manifests
for this pair. No health checks, just facts.

```
$ gwp pair info 1

Pair 1:
  gatewayClassName:    tr-1
  dataplaneNamespace:  tr-dataplane-1
  allowedRoutes label: tr/gateway-routes=true

Use these values in your Gateway manifests:

  spec:
    gatewayClassName: tr-1
    infrastructure:
      parametersRef:
        group: gateway.envoyproxy.io
        kind: EnvoyProxy
        name: <your-tier-name>       # must exist in tr-dataplane-1

  listeners:
  - allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            tr/gateway-routes: "true"
```

`gwp pair info` is intentionally minimal: it gives operators the three
values they need and nothing else. It is also safe to call against a pair
that is not yet fully healthy.



---

## `gwp pair verify <index>`

Re-runs post-install checks without reinstalling. Useful after a manual fix
to confirm the pair recovered.

```
$ gwp pair verify 1

Verifying pair 1...
  controller Available:    ok
  GatewayClass Accepted:   ok
  Gateway Programmed:      ok
  proxy ready:             ok

Pair 1 healthy.
```

With `--diagnose`, if anything is wrong it runs the diagnostic read sequence
(ConfigMap watch list check, tokenreviews binding check, EG log tail).

---

## `gwp pair delete <index>`

Wraps `helm uninstall eg-pair-{i}` and verifies that cluster-scoped resources
(GatewayClass, ClusterRole, ClusterRoleBinding, Namespaces) were removed.
Helm should handle this via release tracking, but orphaned cluster-scoped
resources are a real footgun when the release secret is deleted manually.

```
$ gwp pair delete 1

Uninstalling eg-pair-1 from tr-system-1...  done
Verifying cluster-scoped resource cleanup:
  GatewayClass tr-1:          removed
  ClusterRole eg-pair-1-*:    removed
  Namespace tr-system-1:      removed
  Namespace tr-dataplane-1:   removed

Pair 1 deleted.
```

If anything is orphaned:

```
[WARN] ClusterRole eg-pair-1-gateway-controller still exists
       Not Helm-managed (may have been created out of band).
       Run: kubectl delete clusterrole eg-pair-1-gateway-controller
```

---

## `gwp pair list`

Discovers all pairs in the cluster by listing Namespaces with the
`eg-role=system` label. Does not require Helm to be configured -- works
read-only against the cluster.

```
$ gwp pair list

Pairs detected in cluster (by namespace label eg-role=system):

  INDEX  SYSTEM-NS     HELM-RELEASE  HELM-STATUS
  1      tr-system-1   eg-pair-1     deployed
  2      tr-system-2   eg-pair-2     deployed
  4      tr-system-4   (none)        orphaned  <-- namespace exists, no Helm release

Hint: orphaned pair 4 was not installed via Helm or the release was deleted.
     Run: gwp pair delete 4  to clean up cluster-scoped resources.
     Or:  gwp pair install 4  to re-install.
```

---

## Scenarios the CLI handles cleanly

### Fresh k3d cluster, install two pairs

```bash
gwp preflight --pair 1
gwp preflight --pair 2   # both pass
gwp crds install
gwp pair install 1
gwp pair install 2
gwp pair status
```

### GKE Standard with managed Gateway API

```bash
gwp crds detect
# OUTPUT: gateway-api v1.2.0 standard -- provider-managed (skipping)

gwp crds install
# auto-skips Gateway API CRDs, installs only EG CRDs

gwp pair install 1 --unsafe-context
```

### Upgrade EG version

```bash
gwp crds install --eg-version v1.9.0
gwp pair install 1 --eg-version v1.9.0
gwp pair install 2 --eg-version v1.9.0
gwp pair status
```

### Diagnose a degraded pair

```bash
gwp pair status 3
# shows Gateway Programmed=False

gwp pair verify 3 --diagnose
# reads ConfigMap, checks logs, prints root cause

# after manual fix:
gwp pair verify 3
# confirms recovery
```

### CI pipeline (fail fast on unhealthy cluster)

```bash
gwp pair status --output json
# exits 1 if any pair degraded; CI gate
```

### Orphan detection after partial teardown

```bash
gwp pair list
# shows orphaned pair 4

gwp pair delete 4
# cleans up cluster-scoped resources even without a Helm release
```

---

## What the CLI is NOT

- Not a Helm replacement. It calls Helm for installs and upgrades. The chart
  is the source of truth for resource shapes; the CLI is orchestration and
  observability on top.
- Not a continuous reconciler or operator. It is a one-shot tool for humans
  and CI pipelines.
- Not a multi-cluster manager. It talks to one cluster per invocation (the
  current kubeconfig context or `--context`).
- Not responsible for HTTPRoute management. Routes are tenant-owned. The CLI
  only surfaces Gateway and proxy readiness, not route attachment status.

---

## Implementation notes (for when we build this)

### Language and package layout

Go. The k8s client machinery already in `e2e/suite_test.go` becomes a proper
library under `internal/`:

```
cmd/gwp/main.go
internal/
  kube/        client factory, context validation, managedFields helpers
  crd/         CRD detection, install, version comparison
  pair/         pair status, install orchestration, verify, delete, list
  preflight/   preflight check runners (RBAC, server version, conflicts)
  helm/        exec-based Helm wrapper (upgrade --install, uninstall, template)
```

### Flag conventions

- `--context` -- override kubeconfig current-context (mirrors kubectl)
- `--kubeconfig` -- path (mirrors kubectl)
- `--unsafe-context` -- allow non-k3d contexts
- `--output text|json|yaml` -- machine-readable output
- `--timeout` -- for any command that polls

### hack/install-crds.sh is replaced by the CLI

`hack/install-crds.sh` stays in the repo for the PoC phase when the CLI does
not exist yet, but its entire logic lives inside `gwp crds detect` +
`gwp crds install`. Once the CLI is built, the script becomes a one-liner:

```bash
#!/usr/bin/env bash
exec gwp crds install "$@"
```

Or it is removed entirely and the Makefile `crds-install` target calls `gwp`
directly. The script is not a maintained artifact in its own right.

The migration: every env var the script accepts maps to a CLI flag.

| Script env var | CLI flag |
|---|---|
| `KTX` | `--context` |
| `EG_VERSION` | `--eg-version` |
| `CHANNEL` | `--channel` |
| `SKIP_GATEWAY_API_CRDS` | `--skip-gateway-api-crds` |
| `FORCE_GATEWAY_API` | `--force-gateway-api-crds` |
| `UNSAFE_CONTEXT` | `--unsafe-context` |

### Helm invocation

`exec.Command("helm", ...)` with `--kube-context` injected. Do not use the
Helm Go SDK -- it pulls in 50+ dependencies and has breaking API changes
between minor versions. The exec model is simpler and matches the Makefile.

For `gwp pair install`, the flow is:

1. `helm upgrade --install eg-pair-{i} ... --wait --timeout 120s`
2. poll for controller Deployment Available (client-go)
3. poll for GatewayClass Accepted (dynamic client)
4. poll for Gateway Programmed (dynamic client)
5. poll for Envoy proxy Deployment available (client-go)

Steps 2-5 are done in Go with `client-go`, not by shelling to `kubectl`.

### CRD detection: Go implementation

`managedFields` inspection in Go:

```go
// internal/crd/detect.go

type GatewayAPIState int
const (
    NotInstalled   GatewayAPIState = iota
    SelfManaged
    ProviderManaged
)

var knownProviderManagers = []string{
    "gke-networking-controller",
    "gke-gateway-api",
    "aks-gateway-api-controller",
    "addon-manager",
}

type DetectResult struct {
    State          GatewayAPIState
    BundleVersion  string
    Channel        string
    ProviderManager string // non-empty when ProviderManaged
}

func DetectGatewayAPICRDs(ctx context.Context, client dynamic.Interface) (DetectResult, error) {
    crd, err := client.Resource(crdGVR).Get(ctx, "gateways.gateway.networking.k8s.io", metav1.GetOptions{})
    if apierrors.IsNotFound(err) {
        return DetectResult{State: NotInstalled}, nil
    }
    if err != nil {
        return DetectResult{}, err
    }

    annotations := crd.GetAnnotations()
    bundleVersion := annotations["gateway.networking.k8s.io/bundle-version"]
    channel := annotations["gateway.networking.k8s.io/channel"]

    for _, mf := range crd.GetManagedFields() {
        for _, pm := range knownProviderManagers {
            if mf.Manager == pm {
                return DetectResult{
                    State:           ProviderManaged,
                    BundleVersion:   bundleVersion,
                    Channel:         channel,
                    ProviderManager: pm,
                }, nil
            }
        }
    }

    return DetectResult{
        State:         SelfManaged,
        BundleVersion: bundleVersion,
        Channel:       channel,
    }, nil
}
```

### CRD install: helm template piped to kubectl apply --server-side

The `helm template | kubectl apply --server-side` chain avoids Helm's 1 MB
release secret annotation limit. In Go, this is two exec.Command calls with
an `io.Pipe` connecting them:

```go
// internal/crd/install.go

func ApplyCRDs(ctx context.Context, helmArgs []string, kubectlContext string) error {
    pr, pw := io.Pipe()

    helmCmd := exec.CommandContext(ctx, "helm", helmArgs...)
    helmCmd.Stdout = pw
    helmCmd.Stderr = os.Stderr

    kubectlCmd := exec.CommandContext(ctx,
        "kubectl", "--context", kubectlContext,
        "apply", "--server-side", "-f", "-",
    )
    kubectlCmd.Stdin = pr
    kubectlCmd.Stdout = os.Stdout
    kubectlCmd.Stderr = os.Stderr

    if err := helmCmd.Start(); err != nil {
        pw.Close()
        return err
    }
    if err := kubectlCmd.Start(); err != nil {
        pw.CloseWithError(err)
        helmCmd.Wait()
        return err
    }

    helmErr := helmCmd.Wait()
    pw.CloseWithError(helmErr) // signals EOF or error to kubectl stdin
    kubectlErr := kubectlCmd.Wait()

    if helmErr != nil {
        return fmt.Errorf("helm template: %w", helmErr)
    }
    return kubectlErr
}
```

The `kubectl apply --server-side` exec is kept even in the CLI because
reimplementing server-side apply merge strategy in Go would mean pulling in
`sigs.k8s.io/controller-runtime` or the full `k8s.io/kubectl` package, both
of which carry significant dependency weight and are not trivially correct
for large CRD manifests with CEL validation rules.

### Exit codes

- 0: all checks passed / operation succeeded
- 1: one or more checks failed / operation failed
- 2: usage error (unknown flag, missing argument)
