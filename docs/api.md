# API

The `gwpapi` package exposes all gwp operations as a Go API for tools or
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

// Pre-install checks
r, _ := c.Preflight(ctx, gwpapi.PreflightOptions{PairIndex: 1})
if r.Failures > 0 {
    log.Fatalf("preflight failed: %d failure(s)", r.Failures)
}

// Install CRDs
if err := c.CRDInstall(ctx, gwpapi.CRDInstallOptions{}); err != nil {
    log.Fatal(err)
}

// Install pair 1
if err := c.PairInstall(ctx, 1, gwpapi.PairInstallOptions{}); err != nil {
    log.Fatal(err)
}

// Verify health
vr, _ := c.PairVerify(ctx, 1, gwpapi.PairVerifyOptions{})
if !vr.Healthy {
    log.Fatalf("pair 1 not healthy")
}

// Inspect embedded charts
charts, _ := c.ChartsList()
fmt.Println(charts.Charts[0].AppVersion) // e.g. "v1.8.0"

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
| `github.com/dio/gateway-pairs/pair` | Pair install, delete, status, verify, list |
| `github.com/dio/gateway-pairs/preflight` | Pre-install cluster readiness checks |
| `github.com/dio/gateway-pairs/gwpcharts` | Embedded chart introspection and export |
| `github.com/dio/gateway-pairs/charts` | Access to embedded chart and CRD FS |

The `internal/` packages (`kube`, `helm`) are not exported.

---

## Preflight

```go
// Run all checks on a fresh cluster before installing pair 1.
r, err := c.Preflight(ctx, gwpapi.PreflightOptions{
    PairIndex:     1,
    UnsafeContext: false, // set true for non-k3d clusters
    Out:           os.Stdout,
})
// r.Failures > 0 means at least one check blocked.
// r.Warnings > 0 means warnings only (proceed is OK).
for _, check := range r.Checks {
    fmt.Printf("[%s] %s\n", check.Status, check.Message)
}
```

`PreflightOptions.PairIndex` enables pair-specific conflict checks (6-8):
GatewayClass name, controllerName uniqueness, namespace existence. Pass 0
to run only global checks (1-5).

---

## PairVerify

```go
vr, err := c.PairVerify(ctx, 1, gwpapi.PairVerifyOptions{
    Diagnose: false, // set true to print ConfigMap+logs on failure
    Out:      os.Stdout,
})
if !vr.Healthy {
    for _, ch := range vr.Checks {
        if !ch.OK {
            fmt.Printf("FAIL: %s -- %s\n", ch.Name, ch.Message)
        }
    }
}
```

Checks: controller Available, GatewayClass Accepted, L3 Gateways Programmed.
Exits without reinstalling. Use after `PairInstall` to confirm health, or after
a manual fix to verify recovery.

---

## Charts

```go
// List embedded charts and CRD bundles (no cluster access).
list, err := c.ChartsList()
for _, ch := range list.Charts {
    fmt.Printf("%s %s (EG %s)\n", ch.Name, ch.Version, ch.AppVersion)
}

// Export eg-pair chart to disk (for direct Helm use).
if err := c.ChartsExport("./my-charts"); err != nil { ... }

// Print default values.yaml.
values, err := c.ChartsShowValues("eg-pair")
fmt.Println(values)
```


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
