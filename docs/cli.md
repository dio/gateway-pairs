# gwp CLI reference

`gwp` is a single binary that manages Envoy Gateway controller+dataplane pairs.
It wraps Helm for chart installs and kubectl for cluster queries. Helm is still
the install mechanism for the `eg-pair` chart; the CLI handles the per-pair
flag injection that raw `helm install` requires and that the chart cannot
self-derive (see [design.md](design.md) for why).

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

## Global flags

All flags below apply to every subcommand.

| Flag | Default | Description |
|---|---|---|
| `--context` | current-context | kubeconfig context name |
| `--kubeconfig` | `~/.kube/config` | path to kubeconfig |
| `--prefix` | `tr` | name prefix for all derived resource names |
| `--no-prefix` | false | use no prefix (produces `system-1`, `dataplane-1`) |
| `--suffix` | `` | string suffix override (e.g. `prod` → `tr-system-prod`) |
| `--no-suffix` | false | use no suffix (produces `tr-system`, `tr-dataplane`) |
| `-o, --output` | `text` | output format: `text` or `json` |

### Naming matrix

The prefix and suffix flags combine as follows (index=1):

| flags | system NS | dataplane NS | GatewayClass |
|---|---|---|---|
| *(default)* | `tr-system-1` | `tr-dataplane-1` | `tr-1` |
| `--prefix myapp` | `myapp-system-1` | `myapp-dataplane-1` | `myapp-1` |
| `--no-prefix` | `system-1` | `dataplane-1` | `1` |
| `--suffix prod` | `tr-system-prod` | `tr-dataplane-prod` | `tr-prod` |
| `--no-suffix` | `tr-system` | `tr-dataplane` | `tr` |
| `--no-prefix --suffix prod` | `system-prod` | `dataplane-prod` | `prod` |
| `--no-prefix --no-suffix` | `system` | `dataplane` | *(empty)* |

Use `--no-prefix` / `--no-suffix` rather than `--prefix ""` / `--suffix ""`
to avoid shell quoting issues in Makefiles and CI.

---

## Embedded assets

The binary carries two asset groups via `//go:embed`:

```
charts/
  eg-pair/                    Helm chart (full tree, including subchart tarball)
  crds/
    gateway-api-standard.yaml   pre-rendered Gateway API standard channel CRDs
    gateway-api-experimental.yaml
    envoy-gateway.yaml          pre-rendered EG CRDs
```

The `crds/` YAML files are generated at build time (`make generate-crds`) and
are gitignored. CI and goreleaser run `make generate-assets` before building the
binary. The `eg-pair/` chart directory is committed and embedded via `all:eg-pair`
(the `all:` prefix includes gitignored files such as the subchart `.tgz`).

---

## `gwp version`

```
gwp version [-o json]
```

Prints the binary version and bundled component versions.

```
gwp v0.1.0
  eg-version: v1.8.0
  commit:     abc1234
  built:      2026-05-24T10:00:00Z
```

JSON:

```json
{"version":"v0.1.0","egVersion":"v1.8.0","commit":"abc1234","date":"2026-05-24T10:00:00Z"}
```

---

## `gwp crds detect`

```
gwp crds detect [-o json]
```

Inspects the cluster and reports the installation state of:
- Gateway API CRDs (standard or experimental channel)
- Envoy Gateway CRDs

Reports who manages the CRDs when they are provider-managed (GKE, AKS, etc.)
so you know whether to skip or force installation.

**States:**

| State | Meaning |
|---|---|
| `not-installed` | CRD not found on the cluster |
| `installed` | CRD present, managed by gwp / helm / kubectl |
| `provider-managed` | CRD present, managed by a cloud provider controller |

**Text output:**

```
Gateway API CRDs:
  state:   not-installed
  channel: standard
  version: (none)

Envoy Gateway CRDs:
  state:   not-installed
```

**JSON output:**

```json
{
  "gatewayAPI": {
    "state": "not-installed",
    "bundleVersion": "",
    "channel": "standard",
    "providerManager": ""
  },
  "envoyGateway": {
    "state": "not-installed"
  }
}
```

**Provider-managed example (GKE Standard):**

```
Gateway API CRDs:
  state:           provider-managed
  managed-by:      gke-networking-controller
  bundle-version:  v1.5.1
  channel:         standard
```

---

## `gwp crds install`

```
gwp crds install [flags]
```

Installs Gateway API and Envoy Gateway CRDs from the embedded pre-rendered YAML
using `kubectl apply --server-side`. No OCI registry access required.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--channel` | `standard` | Gateway API channel: `standard` or `experimental` |
| `--skip-gateway-api-crds` | false | skip Gateway API CRDs (provider-managed clusters) |
| `--force-gateway-api-crds` | false | install Gateway API CRDs even when already present |

**Behaviour:**

- If Gateway API CRDs are already present and not `--force`, skips them.
- If provider-managed, skips unless `--force-gateway-api-crds` is set (rare).
- EG CRDs are always installed / upgraded via server-side apply (idempotent).

**Safe to re-run.** Server-side apply is idempotent; re-running upgrades CRDs
in place without affecting existing CR objects.

---

## `gwp pair install <index>`

```
gwp pair install <index> [flags]
```

Installs or upgrades an `eg-pair` Helm release. Uses `helm upgrade --install`,
so re-running on an existing pair upgrades it in place. Does not error on
"already exists".

**What it computes and injects automatically:**

| Helm `--set` | Value | Why |
|---|---|---|
| `pair.namePrefix` | `--prefix` value | chart derives NS names from this |
| `pair.nameSuffix` | `--suffix` value | string suffix override (when `--suffix` or `--no-suffix`) |
| `pair.index` | `<index>` (0 when suffix active) | chart derives numeric suffix from this |
| `gateway-helm.config.envoyGateway.gateway.controllerName` | `gateway.envoyproxy.io/<gwclass>` | unique per pair; prevents controllers colliding |
| `gateway-helm.config.envoyGateway.provider.kubernetes.watch.type` | `Namespaces` | scope controller to its two namespaces |
| `gateway-helm.config.envoyGateway.provider.kubernetes.watch.namespaces` | `{system-NS, dataplane-NS}` | the two namespaces for this pair |

These three `gateway-helm.*` flags cannot be derived by the chart itself (Helm
resolves subchart values before templates run). The CLI computes them from
`--prefix` / `--suffix` / `--no-*` and injects them.

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--set` | (none) | additional `--set` flags passed to helm (repeatable) |
| `--helm-timeout` | `5m` | timeout for `helm upgrade --install` |
| `--wait-timeout` | `3m` | timeout for post-install readiness polling |

**What it waits for after install:**

1. Controller Deployment `envoy-gateway` in SystemNS becomes Available.
2. GatewayClass accepted by the controller (`Accepted=True` condition).

**Example:**

```bash
gwp pair install 1
gwp --prefix myapp pair install 2
gwp --suffix prod pair install 1
gwp --no-prefix --no-suffix pair install 1   # system/dataplane namespaces
gwp pair install 1 --set "gateway-helm.config.envoyGateway.extensionApis.enableEnvoyPatchPolicy=true"
```

---

## `gwp pair delete <index>`

```
gwp pair delete <index>
```

Uninstalls an `eg-pair` Helm release using the correct teardown sequence:

1. Delete all Gateways in the dataplane NS with `--wait` so EG deprovisions
   the proxy Deployment before the controller exits.
2. Delete all EnvoyProxy CRs in the dataplane NS.
3. Wait until EG-managed Deployments and Services are gone.
4. `helm uninstall` the release.
5. Delete both namespaces explicitly.

Skipping step 1 and removing the controller first leaves proxy pods stuck in
`Terminating` for up to 360s (EG default `terminationGracePeriodSeconds`).

For fast teardown with no live connections, POST `/quitquitquit` to the proxy
admin API before deleting. See MANUAL.md section 8 for the script.

---

## `gwp pair status [index]`

```
gwp pair status [index] [-o json]
```

Shows the health of one pair (when `index` is given) or all pairs.

**Single pair text output:**

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

The `[DRIFT]` marker appears when the installed EG version differs from the
version bundled in the current `gwp` binary. Run `gwp crds install --force`
then `gwp pair install <index>` to upgrade.

**JSON output includes:** `index`, `names`, `helmStatus`, `installedEgVersion`,
`bundledEgVersion`, `versionDrift`, `controller`, `gatewayClass`, `l3Gateways`.

---

## `gwp pair info <index>`

```
gwp pair info <index> [-o json]
```

Prints the coupling fields an operator needs when writing Layer 3 manifests
(EnvoyProxy, Gateway, HTTPRoute). No cluster access required.

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

  listeners:
  - allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            tr/gateway-routes: "true"
```

---

## `gwp pair list`

```
gwp pair list [-o json]
```

Lists all installed `eg-pair` releases discovered via `helm list`. Calls
`gwp pair status` for each.

```
PAIR   SYSTEM-NS     GW-CLASS   STATUS
1      tr-system-1   tr-1       deployed
2      tr-system-2   tr-2       deployed
```

---

## Output format

All commands support `-o json`. The JSON schema mirrors the text output
fields. Useful for CI and scripting:

```bash
# check if pair 1 controller is available
gwp pair status 1 -o json | jq '.controller.available'

# detect version drift across all pairs
gwp pair list -o json | jq '.[] | select(.versionDrift) | .names.systemNamespace'

# get the GatewayClass name for a pair
gwp pair info 1 -o json | jq -r '.gatewayClass'
```

---

## Implementation notes

### Language and package layout

```
cmd/gwp/            binary entry point; bakes version via -ldflags
names/              pure naming logic (no I/O); mirrors _helpers.tpl rules
crd/                CRD detect + install logic
pair/               pair install / delete / get / list / info
gwpapi/             public embedding API (single import point)
internal/
  kube/             exec kubectl wrapper (uses dio/sh)
  helm/             exec helm wrapper (uses dio/sh)
  cli/              cobra command tree
  fake/             in-process kubectl/helm fakes for unit tests
charts/
  eg-pair/          Helm chart (committed, embedded via //go:embed all:eg-pair)
  crds/             pre-rendered CRD YAML (gitignored, generated at build time)
```

### Helm invocation

The CLI execs `helm` via `dio/sh` rather than importing the Helm Go SDK.
The SDK pulls in 50+ transitive dependencies and has breaking API changes
between minor versions. Exec keeps the dependency surface minimal: no
`k8s.io/*` imports in the CLI package.

### CRD embedding

CRD YAML is pre-rendered at build time:

```makefile
make generate-crds   # helm template gateway-crds-helm | split by --set flags
```

The resulting files are embedded via `//go:embed all:crds`. At install time
`gwp crds install` reads from memory and pipes to `kubectl apply --server-side`.
No network access required after the binary is built.

### Chart embedding

`//go:embed all:eg-pair` embeds the full chart tree including
`eg-pair/charts/gateway-helm-v1.8.0.tgz` (the `all:` prefix bypasses
`.gitignore`). At install time `gwp pair install` extracts the chart to a temp
dir and passes the path to `helm upgrade --install`.

### Why the three controllerName/watch flags cannot be in the chart

Helm resolves subchart values before template rendering. A parent chart template
cannot compute a value and write it into a subchart's values block. The three
required flags must be injected at install time by the caller (the CLI or
Makefile). See design.md for the full explanation.

---

## Future work

`gwp preflight` -- pre-install cluster readiness check: RBAC, API server
reachability, CRD state, GatewayClass name conflicts, controller name
uniqueness. Useful before installing the first pair on an unfamiliar cluster.

`gwp pair verify` -- post-install health check deeper than `pair status`:
validates proxy readiness, tests HTTP connectivity through a temporary
Gateway+HTTPRoute, checks controllerName isolation between pairs.
