# gwp CLI

`gwp` manages Envoy Gateway controller+dataplane pairs. It wraps Helm for chart
installs and kubectl for cluster queries. The binary embeds the `eg-pair` chart
and pre-rendered CRD YAML so no OCI registry access is needed at install time.

> **Generated flag reference:** `make docs` writes one `.md` per command into
> `docs/commands/`. The flag tables below are copied from those files and reflect
> the current binary exactly. Run `make docs` after changing flag definitions.

---

## Command tree

```
gwp
  version
  crds
    detect
    install
  pair
    install <index>
    delete  <index>
    status  [index]
    info    <index>
    list
```

---

## gwp

Manage Envoy Gateway controller+dataplane pairs

### Options

```
      --context string      kubeconfig context (default: current-context)
  -h, --help                help for gwp
      --kubeconfig string   path to kubeconfig file (default: ~/.kube/config)
      --no-prefix           use no prefix: produces system-1, dataplane-1 instead of tr-system-1, tr-dataplane-1
      --no-suffix           use no suffix: produces tr-system, tr-dataplane instead of tr-system-1, tr-dataplane-1. Useful for single-pair deployments where numbering is unnecessary.
  -o, --output string       output format: text or json (default "text")
      --prefix string       name prefix for all derived resource names (e.g. "tr" → tr-system-1, tr-1) (default "tr")
      --suffix string       string suffix override (e.g. "prod" → tr-system-prod, GatewayClass tr-prod). When set, replaces the numeric index in all names; use --suffix instead of an index.
```

All flags above are global: they apply to every subcommand.

### Naming matrix

The prefix/suffix flags combine to produce all derived resource names. With
index=1:

| flags | system NS | dataplane NS | GatewayClass |
|---|---|---|---|
| *(default)* | `tr-system-1` | `tr-dataplane-1` | `tr-1` |
| `--prefix myapp` | `myapp-system-1` | `myapp-dataplane-1` | `myapp-1` |
| `--no-prefix` | `system-1` | `dataplane-1` | `1` |
| `--suffix prod` | `tr-system-prod` | `tr-dataplane-prod` | `tr-prod` |
| `--no-suffix` | `tr-system` | `tr-dataplane` | `tr` |
| `--no-prefix --suffix prod` | `system-prod` | `dataplane-prod` | `prod` |
| `--no-prefix --no-suffix` | `system` | `dataplane` | *(empty)* |

Use `--no-prefix` / `--no-suffix` instead of `--prefix ""` / `--suffix ""`
to avoid shell quoting issues in Makefiles and CI scripts.

`--no-suffix` is useful for single-pair deployments where numbering is
unnecessary. `--no-prefix --no-suffix` gives bare `system` / `dataplane`
namespaces with no decoration.

### Output format

All commands support `-o json`. JSON is suitable for CI scripting:

```bash
gwp pair status 1 -o json | jq '.controller.available'
gwp pair list -o json | jq '.[] | select(.versionDrift) | .names.systemNamespace'
gwp pair info 1 -o json | jq -r '.gatewayClass'
gwp crds detect -o json | jq '.gatewayAPI.state'
```

---

## gwp version

Print gwp version and bundled component versions.

```
gwp version [flags]
```

### Options

```
  -h, --help   help for version
```

### Example output

```
gwp v0.1.0
  eg-version: v1.8.0
  commit:     abc1234
  built:      2026-05-24T10:00:00Z
```

JSON: `{"version":"v0.1.0","egVersion":"v1.8.0","commit":"abc1234","date":"..."}`

---

## gwp crds detect

Show what CRDs are installed and who manages them.

```
gwp crds detect [flags]
```

### Options

```
  -h, --help   help for detect
```

Inspects the cluster for Gateway API and Envoy Gateway CRDs. Reports the
installation state and, for provider-managed clusters (GKE, AKS, etc.), which
controller owns the CRDs so you know whether to skip or force installation.

### States

| State | Meaning |
|---|---|
| `not-installed` | CRD not found |
| `installed` | Present, self-managed |
| `provider-managed` | Present, owned by a cloud controller |

### Example output (text)

```
Gateway API CRDs:
  state:   not-installed
  channel: standard

Envoy Gateway CRDs:
  state:   not-installed
```

Provider-managed example:

```
Gateway API CRDs:
  state:           provider-managed
  managed-by:      gke-networking-controller
  bundle-version:  v1.5.1
  channel:         standard
```

---

## gwp crds install

Install Gateway API and Envoy Gateway CRDs.

```
gwp crds install [flags]
```

### Options

```
      --channel string           Gateway API channel: standard or experimental (default "standard")
      --force-gateway-api-crds   install Gateway API CRDs even when already present
  -h, --help                     help for install
      --skip-gateway-api-crds    skip Gateway API CRDs (use for provider-managed clusters)
```

Installs from embedded pre-rendered YAML using `kubectl apply --server-side`.
No OCI registry access required. Safe to re-run: server-side apply is
idempotent.

**Behaviour:**

- Gateway API CRDs already present: skipped unless `--force-gateway-api-crds`.
- Provider-managed Gateway API CRDs: skipped unless `--force-gateway-api-crds`
  (rare; only needed when downgrading, which risks removing CRD versions).
- EG CRDs: always applied (idempotent upgrade).

**Upgrade:** When bumping EG version, install the new `gwp` binary and run
`gwp crds install --force-gateway-api-crds` before `gwp pair install`.

---

## gwp pair install

Install or upgrade a pair.

```
gwp pair install <index> [flags]
```

### Options

```
      --helm-timeout duration   timeout for helm upgrade --install (default 5m0s)
  -h, --help                    help for install
      --set stringArray         additional --set flags passed to helm (repeatable)
      --wait-timeout duration   timeout for post-install readiness polling (default 3m0s)
```

Uses `helm upgrade --install`. Re-running on an existing pair upgrades it in
place without error.

**What the CLI injects automatically:**

| `--set` key | Value | Why |
|---|---|---|
| `pair.namePrefix` | `--prefix` value | chart derives NS names from this |
| `pair.nameSuffix` | `--suffix` value | string suffix when `--suffix`/`--no-suffix` active |
| `pair.index` | `<index>` (0 when suffix active) | chart derives numeric suffix |
| `gateway-helm.config...controllerName` | `gateway.envoyproxy.io/<gwclass>` | unique per pair; prevents controller collisions |
| `gateway-helm.config...watch.type` | `Namespaces` | scopes controller to its two namespaces |
| `gateway-helm.config...watch.namespaces` | `{system-NS,dataplane-NS}` | the two namespaces for this pair |

The three `gateway-helm.*` flags cannot be derived by the chart itself (Helm
resolves subchart values before templates run).

**Post-install waits for:**

1. Controller Deployment `envoy-gateway` in SystemNS: Available.
2. GatewayClass `Accepted=True`.

**Examples:**

```bash
gwp pair install 1
gwp --prefix myapp pair install 2
gwp --suffix prod pair install 1
gwp --no-prefix --no-suffix pair install 1
gwp pair install 1 --set "gateway-helm.config.envoyGateway.extensionApis.enableEnvoyPatchPolicy=true"
```

---

## gwp pair delete

Uninstall a pair.

```
gwp pair delete <index> [flags]
```

### Options

```
  -h, --help   help for delete
```

Runs the correct teardown sequence:

1. Delete all Gateways in the dataplane NS with `--wait` so EG deprovisions
   the proxy Deployment before the controller exits.
2. Delete all EnvoyProxy CRs in the dataplane NS.
3. Wait until EG-managed Deployments and Services are gone.
4. `helm uninstall`.
5. Delete both namespaces explicitly.

Skipping step 1 leaves proxy pods stuck in Terminating for up to 360s (EG
default `terminationGracePeriodSeconds = drainTimeout + 300s`).

For fast teardown with no live connections, POST `/quitquitquit` to the proxy
Envoy admin API (`127.0.0.1:19000`) via `kubectl port-forward` before calling
`gwp pair delete`. See MANUAL.md section 8 for the script.

---

## gwp pair status

Show health of one pair or all pairs.

```
gwp pair status [index] [flags]
```

### Options

```
  -h, --help   help for status
```

Without `[index]`, shows all installed pairs (same as `gwp pair list` with
detailed output).

**Text output (single pair):**

```
Pair 1 (tr-1):
  System namespace:    tr-system-1
  Dataplane namespace: tr-dataplane-1
  Helm status:         deployed
  EG version:          v1.8.0  [DRIFT: gwp bundles v1.9.0]
  Controller:          tr-system-1/envoy-gateway  Available (1/1)
  GatewayClass:        tr-1  Accepted=True

Layer 3 (in tr-dataplane-1):
  eg-test  Programmed=True  proxy 1/1
```

The `[DRIFT]` tag appears when the installed EG version (from the Helm release
`appVersion`) differs from the version bundled in the current `gwp` binary.
Run `gwp crds install --force-gateway-api-crds` then `gwp pair install <index>`
to upgrade.

**JSON fields:** `index`, `names`, `helmStatus`, `installedEgVersion`,
`bundledEgVersion`, `versionDrift`, `controller`, `gatewayClass`, `l3Gateways`.

---

## gwp pair info

Print coupling fields for writing Layer 3 manifests.

```
gwp pair info <index> [flags]
```

### Options

```
  -h, --help   help for info
```

No cluster access required. Derives all names from `--prefix`, `--suffix`,
`--no-*`, and `<index>`.

**Example output:**

```
Pair 1:
  gatewayClassName:    tr-1
  dataplaneNamespace:  tr-dataplane-1
  allowedRoutes label: tr/gateway-routes=true

Use in your Gateway manifests:

  spec:
    gatewayClassName: tr-1
    infrastructure:
      parametersRef:
        group: gateway.envoyproxy.io
        kind: EnvoyProxy
        name: <your-tier-name>  # must exist in tr-dataplane-1
```

---

## gwp pair list

List all installed pairs.

```
gwp pair list [flags]
```

### Options

```
  -h, --help   help for list
```

Discovers pairs via `helm list` filtered to `eg-pair-*` releases, then calls
`gwp pair status` for each.

**Text output:**

```
PAIR   SYSTEM-NS     GW-CLASS   STATUS
1      tr-system-1   tr-1       deployed
2      tr-system-2   tr-2       deployed
```

---

## Embedded assets

```
charts/
  eg-pair/                          Helm chart (committed, //go:embed all:eg-pair)
  crds/
    gateway-api-standard.yaml       pre-rendered Gateway API standard CRDs
    gateway-api-experimental.yaml   pre-rendered Gateway API experimental CRDs
    envoy-gateway.yaml              pre-rendered EG CRDs
```

`charts/crds/` is gitignored and generated by `make generate-crds` at build
time. `charts/eg-pair/` is committed. The `all:` prefix on the embed directive
includes the gitignored subchart tarball (`gateway-helm-v*.tgz`).

---

## Implementation notes

### Package layout

```
cmd/gwp/           binary entry point; bakes version via -ldflags
cmd/gwp-gendocs/   doc generator (go run cmd/gwp-gendocs/main.go)
names/             pure naming logic; mirrors _helpers.tpl; zero deps
crd/               CRD detect + install
pair/              pair Install/Delete/Get/List/Info; JSON-tagged Status
gwpapi/            public Go embedding API
internal/
  kube/            exec kubectl via dio/sh
  helm/            exec helm via dio/sh
  cli/             cobra command tree; BuildRoot exported for gendocs
  fake/            in-process fakes for unit tests
```

### Why exec Helm, not the Helm SDK

The Helm Go SDK pulls 50+ transitive dependencies and has breaking API changes
between minor versions. Exec keeps the dependency surface minimal: no
`k8s.io/*` imports in the CLI package.

### Why the three controllerName/watch flags cannot live in the chart

Helm resolves subchart values before template rendering. A parent chart template
cannot compute a value and write it into a subchart's values block. The three
`gateway-helm.*` flags must be injected at install time by the caller.

### Keeping this doc in sync

Flag tables in this file are copied from `docs/commands/` output. After changing
any flag name, default, or description, run `make docs` and paste the updated
blocks into this file.

---

## Future work

`gwp preflight` -- pre-install cluster readiness check: RBAC, API server
reachability, CRD state, GatewayClass name conflicts, controller name
uniqueness.

`gwp pair verify` -- post-install health check deeper than `pair status`:
validates proxy readiness, tests HTTP connectivity through a temporary
Gateway+HTTPRoute, checks controllerName isolation between pairs.
