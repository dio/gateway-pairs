# API

The `api` package exposes all gwp operations as a Go API for tools or
controllers that want to embed gateway-pairs lifecycle management without
shelling out to the `gwp` binary.

Import path: `github.com/dio/gateway-pairs/gwpapi`

---

## Quickstart

```go
import "github.com/dio/gateway-pairs/gwpapi"

c := gwpapi.New(gwpapi.Options{
    KubeContext: "k3d-mycluster",
    Prefix:      "tr",
})

// Install CRDs
if err := c.CRDInstall(ctx, gwpapi.CRDInstallOptions{}); err != nil {
    log.Fatal(err)
}

// Install pair 1
if err := c.PairInstall(ctx, 1, gwpapi.PairInstallOptions{}); err != nil {
    log.Fatal(err)
}

// Check status
s, err := c.PairGet(ctx, 1)
fmt.Println(s.GatewayClass.Accepted) // true
```

---

## Client

```go
func New(opts Options) *Client
```

Creates a Client. All methods on Client are safe to call concurrently as long
as they target different pair indices.

```go
type Options struct {
    KubeContext string // default: current-context
    Kubeconfig  string // default: ~/.kube/config
    Prefix      string // default: "tr"
}
```

---

## CRD operations

### CRDDetect

```go
func (c *Client) CRDDetect(ctx context.Context) (CRDDetectResult, error)
```

Detects what Gateway API and EG CRDs are installed and how they are managed
(self-managed vs provider-managed by GKE/AKS/etc).

```go
type CRDDetectResult struct {
    GatewayAPI GatewayAPIInfo
    EG         EGInfo
}

type GatewayAPIInfo struct {
    State           State  // NotInstalled | SelfManaged | ProviderManaged
    BundleVersion   string // e.g. "v1.5.1"
    Channel         string // "standard" or "experimental"
    ProviderManager string // non-empty when ProviderManaged
}

type EGInfo struct {
    State   State
    Version string // e.g. "v1.8.0"
}
```

### CRDInstall

```go
func (c *Client) CRDInstall(ctx context.Context, opts CRDInstallOptions) error
```

Detects and installs Gateway API + EG CRDs. Automatically skips
provider-managed Gateway API CRDs. Uses pre-rendered CRD bytes embedded in
the binary (requires `make generate-crds` to have been run at build time).

```go
type CRDInstallOptions struct {
    SkipGatewayAPI  bool      // force-skip Gateway API CRDs
    ForceGatewayAPI bool      // install even when already present
    Channel         string    // "standard" or "experimental" (default: "standard")
    Out             io.Writer // progress output (default: os.Stdout)
}
```

---

## Pair operations

### PairInstall

```go
func (c *Client) PairInstall(ctx context.Context, index int, opts PairInstallOptions) error
```

Installs or upgrades an eg-pair Helm release. Extracts the embedded chart,
injects the three required per-pair flags (`controllerName`, `watch.type`,
`watch.namespaces`), then polls for controller and GatewayClass readiness
before returning.

```go
type PairInstallOptions struct {
    ExtraSet    []string      // additional --set flags passed to helm
    HelmTimeout time.Duration // helm upgrade --install timeout (default: 5m)
    WaitTimeout time.Duration // readiness polling timeout (default: 3m)
    Out         io.Writer     // progress output (default: os.Stdout)
}
```

### PairDelete

```go
func (c *Client) PairDelete(ctx context.Context, index int, out io.Writer) error
```

Uninstalls a pair. Warns if EG-managed proxy Deployments still exist in the
dataplane namespace (which would cause the namespace to hang in Terminating).
Callers should delete all Gateways before calling PairDelete.

### PairGet

```go
func (c *Client) PairGet(ctx context.Context, index int) (*PairStatus, error)
```

Returns the current status of a single pair.

```go
type PairStatus struct {
    Index        int
    Names        names.Pair          // all derived names for this pair
    HelmStatus   string              // "deployed" | "not-installed" | ...
    Controller   ControllerStatus
    GatewayClass GatewayClassStatus
    L3Gateways   []GatewayStatus     // operator-applied Gateways in dataplane NS
}

type ControllerStatus struct {
    Available bool
    Ready     string // "1/1", "0/1"
}

type GatewayClassStatus struct {
    Accepted bool
    Reason   string // non-empty when Accepted=false
}

type GatewayStatus struct {
    Name           string
    Programmed     bool
    EnvoyProxyName string // from infrastructure.parametersRef
    ProxyReady     string // "1/1", "-"
}
```

### PairList

```go
func (c *Client) PairList(ctx context.Context) ([]*PairStatus, error)
```

Returns the status of all installed pairs, discovered via `helm list`.

### PairInfo

```go
func (c *Client) PairInfo(index int) names.Pair
```

Returns the derived names for a pair without hitting the cluster. Useful for
constructing Layer 3 manifests programmatically.

```go
type names.Pair struct {
    Prefix         string
    Index          int
    SystemNS       string // e.g. "tr-system-1"
    DataplaneNS    string // e.g. "tr-dataplane-1"
    GatewayClass   string // e.g. "tr-1"
    ControllerName string // e.g. "gateway.envoyproxy.io/tr-1"
    ReleaseName    string // e.g. "eg-pair-1"
}
```

---

## Low-level packages

Tools that need finer-grained control can import the sub-packages directly:

| Package | Purpose |
|---|---|
| `github.com/dio/gateway-pairs/names` | Name derivation (no I/O, no dependencies) |
| `github.com/dio/gateway-pairs/crd` | CRD detection and installation |
| `github.com/dio/gateway-pairs/pair` | Pair install, delete, status, list |
| `github.com/dio/gateway-pairs/charts` | Access to embedded chart and CRD FS |

The `internal/` packages (`kube`, `helm`) are not exported.

---

## Embedded assets

The `charts` package exposes two `fs.FS` values:

```go
charts.FS()   // the eg-pair chart tree (eg-pair/...)
charts.CRDs() // pre-rendered CRD YAML files (gateway-api-standard.yaml, etc.)
```

CRD files are generated at build time (`make generate-crds`) and are absent on
a clean checkout. Calling `CRDInstall` without them returns a clear error:

```
embedded CRD "gateway-api-standard.yaml" not found -- run 'make generate-crds' first
```
