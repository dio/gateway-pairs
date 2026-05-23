# gateway-pairs

Envoy Gateway multi-pair deployment: one cluster, N isolated
controller+dataplane pairs. Each pair is one Helm release of `eg-pair`.
CRDs are installed once via `eg-crds` + `hack/install-crds.sh`.

## Skills

Load these when working in this repo:

- **gateway-pairs-e2e** (`/.agents/skills/gateway-pairs-e2e/SKILL.md`)
  Running, debugging, and extending the k3d e2e suite.

- **gateway-pairs-chart-authoring** (`/.agents/skills/gateway-pairs-chart-authoring/SKILL.md`)
  Modifying the eg-crds or eg-pair charts, values conventions, RBAC shape,
  and PoC gaps to close.

## Hard rules

- All `kubectl` and `helm` commands must use `--context k3d-<cluster>`.
  Never fall through to the user's current kube context.
- CRDs are installed via `hack/install-crds.sh`, not `helm install eg-crds`.
  The eg-crds chart only tracks metadata.
- `make e2e` is the canonical e2e entry point. Always use `-count=1`.
- Pair index is an integer. All names derive from it via `_helpers.tpl`.
  Do not hardcode `tr-system-1` or similar strings outside the helpers.
- Watch list in the EG ConfigMap must include BOTH the system namespace and
  the dataplane namespace. Omitting the system namespace causes
  Accepted-but-not-Programmed Gateways.

## Layout

```
charts/
  eg-crds/          CRD lifecycle chart (install once)
  eg-pair/          Controller+dataplane pair chart (one release per pair)
e2e/                testify/suite harness (build tag: e2e)
hack/
  install-crds.sh   CRD install script (helm template | kubectl apply)
docs/
  design.md         Architecture, RBAC model, CRD conflict scenarios
.agents/
  skills/           Hermes Agent in-repo skills
```
