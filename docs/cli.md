# CLI Design

A single binary, `gwp`, that wraps the operational surface of gateway-pairs:
preflight checks, CRD lifecycle management, pair install/status/teardown,
and post-install verification. It is a tool for operators and CI pipelines,
not a replacement for Helm -- Helm is still the install mechanism. `gwp`
handles what Helm cannot: detection, validation, ordering, and readable
cluster state.

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
    status [index]      show health of one pair or all pairs
    verify <index>      re-run post-install checks without reinstalling
    delete <index>      uninstall a pair and clean up cluster-scoped resources
    list                list all pairs detected in the cluster
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
`gateway.networking.k8s.io/channel` annotations.

Four outcomes:

| State | Annotation | Recommended action |
|---|---|---|
| Not installed | absent | install with `gwp crds install` |
| Self-managed, same version | present, no owner annotation | `--skip-gateway-api-crds` safe to skip or force-upgrade |
| Provider-managed | present + owner annotation (e.g. `gke.io/managed`) | must skip: `gwp crds install --skip-gateway-api-crds` |
| Wrong channel (experimental vs standard) | present + channel mismatch | warn: downgrading removes CRDs with live objects |

```
[OK]   gateway-api CRDs not installed -- will install v1.2.1 (standard)
[WARN] gateway-api CRDs v1.1.0 (standard) already installed, will upgrade to v1.2.1
[WARN] gateway-api CRDs managed by provider (gke-networking): skipping
[FAIL] gateway-api CRDs installed on experimental channel; requested standard
       downgrading removes TCPRoute/BackendTLSPolicy CRDs -- check for live objects first
       pass --allow-channel-downgrade to proceed (dangerous)
```

Provider-managed detection heuristic: check for a well-known owner annotation
(`gke.io/managed-by`, `addon-manager.kubernetes.io/mode`, etc.) OR check if
the CRD's `managedFields` contains a field manager that is not `kubectl` /
`helm`. The annotation check is more reliable; document that it may miss
some providers.

### 5. EG CRD detection

Check for `envoyproxies.gateway.envoyproxy.io`. If missing, `gwp crds install`
is required.

```
[OK]   envoy-gateway CRDs v1.8.0 installed
[WARN] envoy-gateway CRDs not installed -- run: gwp crds install
```

### 6. GatewayClass name conflicts (if `--pair <index>` passed)

Check if `tr-{index}` already exists and is not owned by this Helm release.

```
[OK]   GatewayClass tr-1 does not exist
[FAIL] GatewayClass tr-1 exists and is not managed by eg-pair-1
       found controllerName: some-other-controller
```

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
[OK]   gateway-api CRDs not installed -- will install v1.2.1 (standard)
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
    bundle-version: v1.2.1
    channel:        standard
    managed-by:     helm (field manager: helm)

  httproutes.gateway.networking.k8s.io
    bundle-version: v1.2.1
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
- `--channel standard|experimental` -- default: standard
- `--eg-version v1.8.0` -- EG version to install
- `--gateway-api-version v1.2.1` -- Gateway API version

```
$ gwp crds install

Detecting existing CRDs...
  gateway-api: not installed
  envoy-gateway: not installed

Installing gateway-api v1.2.1 (standard) ...  done
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

Wraps `helm upgrade --install eg-pair-{i}`, then runs post-install verification
(equivalent to e2e tests 04-07 but as a blocking CLI command). Exits non-zero
if verification fails within the timeout.

Flags:
- `--timeout 3m` -- total verification timeout (default: 3m)
- `--chart-path ./charts/eg-pair` -- path to local chart (default: OCI ref when published)
- `--eg-version v1.8.0` -- controller image tag
- `--dry-run` -- `helm template` only, print manifests, no apply
- `--set key=value` -- passed through to helm

```
$ gwp pair install 1

Installing eg-pair-1 into tr-system-1...
  helm upgrade --install eg-pair-1 ... --wait --timeout 120s

Waiting for controller (tr-system-1/envoy-gateway) to be Available...  ok (23s)
Waiting for GatewayClass tr-1 to be Accepted...                        ok (1s)
Waiting for Gateway eg/tr-system-1 to be Programmed...                 ok (47s)
Waiting for Envoy proxy Deployment in tr-dataplane-1...                 ok (52s)

Pair 1 ready.

  system namespace:    tr-system-1
  dataplane namespace: tr-dataplane-1
  gateway class:       tr-1
  gateway:             tr-system-1/eg  (Programmed)
  proxy deployment:    tr-dataplane-1/envoy-<generated>  (1/1 ready)
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

Shows the current health of one pair or all pairs. Read-only. No cluster
mutations.

```
$ gwp pair status

PAIR  SYSTEM-NS     DATAPLANE-NS     GW-CLASS  GATEWAY     CONTROLLER  PROXY
1     tr-system-1   tr-dataplane-1   tr-1      Programmed  Available   1/1
2     tr-system-2   tr-dataplane-2   tr-2      Programmed  Available   1/1
3     tr-system-3   tr-dataplane-3   tr-3      Accepted    Available   0/1  <--
```

For pair 3, which is degraded:

```
$ gwp pair status 3

Pair 3:
  controller:  tr-system-3/envoy-gateway     Available (1/1)
  gatewayclass: tr-3                         Accepted=True
  gateway:     tr-system-3/eg               Accepted=True, Programmed=False  <-- degraded
  proxy:       tr-dataplane-3/envoy-<name>   0/1 ready

Gateway conditions:
  Programmed=False  reason: Invalid
  message: "failed to get TLS secret: secret not found"

Hint: watch list in ConfigMap may not include tr-system-3.
     Run: gwp pair verify 3 --diagnose
```

### Status output for CI

```
$ gwp pair status --output json

[
  {"index":1,"controller":"Available","gatewayClass":"Accepted","gateway":"Programmed","proxy":"1/1"},
  {"index":2,"controller":"Available","gatewayClass":"Accepted","gateway":"Programmed","proxy":"1/1"}
]
```

Exit code: 0 if all pairs healthy, 1 if any degraded.

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

Language: Go. Shares the k8s client machinery already in the e2e suite.
The `internal/kube` package from the e2e suite becomes a proper library.

Flag conventions:
- `--context` overrides kubeconfig current-context (mirrors kubectl)
- `--kubeconfig` path (mirrors kubectl)
- `--unsafe-context` allows non-k3d contexts
- `--output text|json|yaml` for machine-readable output
- `--timeout` for any command that polls

Helm invocation: `exec.Command("helm", ...)` with `--kube-context` injected.
Do not use the Helm Go SDK -- the exec model is simpler and matches what the
Makefile already does. The SDK pulls in 50+ dependencies and has breaking
API changes between minor versions.

Kubernetes client: `k8s.io/client-go` dynamic client for CRD detection and
status reading. Typed clients for well-known resources (Deployments, Namespaces,
etc.). The e2e suite already has the `mustKubectl` / `eventually` patterns --
the CLI internalizes those as library functions instead of shelling out to
`kubectl`.

The one exception: `kubectl apply --server-side` for CRD install. The
`--server-side` apply path requires the server-side apply merge strategy which
is not trivially reproducible with the Go client without reimplementing the
applier. Shell out to `kubectl apply --server-side` for CRDs only.

Exit codes:
- 0: all checks passed / operation succeeded
- 1: one or more checks failed / operation failed
- 2: usage error (unknown flag, missing argument)
