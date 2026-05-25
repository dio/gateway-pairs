// Package flux provides e2e tests for the Flux CD integration with gateway-pairs.
//
// Tests verify that a Flux helm-controller can reconcile the eg-pair HelmRelease
// against a local k3d OCI registry -- no remote Git repo or GitHub required.
package flux

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

func manifestMust(t *template.Template, data any) string {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("template %s: %v", t.Name(), err))
	}
	return buf.String()
}

// HelmRepositoryParams holds values for the HelmRepository manifest template.
type HelmRepositoryParams struct {
	// Name is the HelmRepository object name (e.g. "eg-pair").
	Name string
	// Namespace is the target namespace (typically "flux-system").
	Namespace string
	// RegistryInCluster is the oci:// URL visible from inside the cluster
	// (e.g. "oci://gw-flux-registry:5000/dio/gateway-pairs/charts").
	RegistryInCluster string
	// Interval is the polling interval for Flux to check for new chart versions.
	Interval string
}

// HelmReleaseParams holds values for the HelmRelease manifest template.
type HelmReleaseParams struct {
	// Name is the HelmRelease object name (e.g. "eg-pair-1").
	Name string
	// Namespace is the HelmRelease namespace (typically "flux-system").
	Namespace string
	// TargetNamespace is the Kubernetes namespace for the Helm release
	// (e.g. "tr-system-1" -- the EG controller namespace).
	TargetNamespace string
	// ChartVersion is the Helm chart version to install (e.g. "0.0.0-e2e").
	ChartVersion string
	// SourceName is the HelmRepository name (must exist in SourceNamespace).
	SourceName string
	// SourceNamespace is the namespace where the HelmRepository lives.
	SourceNamespace string
	// Interval is the reconciliation interval.
	Interval string

	// Pair identity -- used to derive the three required gateway-helm values.
	PairIndex  int
	NamePrefix string
}

// systemNS returns the EG controller namespace for this pair.
func (p HelmReleaseParams) systemNS() string {
	if p.NamePrefix == "" {
		return fmt.Sprintf("system-%d", p.PairIndex)
	}
	return fmt.Sprintf("%s-system-%d", p.NamePrefix, p.PairIndex)
}

// dataplaneNS returns the Gateway/proxy namespace for this pair.
func (p HelmReleaseParams) dataplaneNS() string {
	if p.NamePrefix == "" {
		return fmt.Sprintf("dataplane-%d", p.PairIndex)
	}
	return fmt.Sprintf("%s-dataplane-%d", p.NamePrefix, p.PairIndex)
}

// gatewayClassName returns the GatewayClass name for this pair.
func (p HelmReleaseParams) gatewayClassName() string {
	if p.NamePrefix == "" {
		return fmt.Sprintf("%d", p.PairIndex)
	}
	return fmt.Sprintf("%s-%d", p.NamePrefix, p.PairIndex)
}

// controllerName returns the unique EG controllerName for this pair.
func (p HelmReleaseParams) controllerName() string {
	return fmt.Sprintf("gateway.envoyproxy.io/%s", p.gatewayClassName())
}

// watchNamespaces returns the Helm brace-list syntax for the watch namespace set.
func (p HelmReleaseParams) watchNamespaces() string {
	return fmt.Sprintf("{%s,%s}", p.systemNS(), p.dataplaneNS())
}

var helmRepositoryTmpl = template.Must(template.New("helmrepository").Parse(`apiVersion: source.toolkit.fluxcd.io/v1beta2
kind: HelmRepository
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
spec:
  type: oci
  url: {{ .RegistryInCluster }}
  interval: {{ .Interval }}
  insecure: true
`))

var helmReleaseTmpl = template.Must(template.New("helmrelease").Funcs(template.FuncMap{
	"systemNS":      func(p HelmReleaseParams) string { return p.systemNS() },
	"dataplaneNS":   func(p HelmReleaseParams) string { return p.dataplaneNS() },
	"controllerName": func(p HelmReleaseParams) string { return p.controllerName() },
	"watchNamespaces": func(p HelmReleaseParams) string { return p.watchNamespaces() },
}).Parse(`apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: {{ .Name }}
  namespace: {{ .Namespace }}
spec:
  interval: {{ .Interval }}
  targetNamespace: {{ .TargetNamespace }}
  install:
    createNamespace: true
    crds: Skip
    remediation:
      retries: 3
  upgrade:
    crds: Skip
    remediation:
      retries: 3
  chart:
    spec:
      chart: eg-pair
      version: "{{ .ChartVersion }}"
      sourceRef:
        kind: HelmRepository
        name: {{ .SourceName }}
        namespace: {{ .SourceNamespace }}
      interval: {{ .Interval }}
  values:
    pair:
      index: {{ .PairIndex }}
      namePrefix: {{ .NamePrefix }}
    controller:
      topologyInjector: false
    gateway-helm:
      config:
        envoyGateway:
          gateway:
            controllerName: {{ controllerName . }}
          provider:
            kubernetes:
              deploy:
                type: GatewayNamespace
              watch:
                type: Namespaces
                namespaces:
                  - {{ systemNS . }}
                  - {{ dataplaneNS . }}
      topologyInjector:
        enabled: false
`))

// HelmRepositoryManifest returns the YAML manifest for a Flux HelmRepository.
func HelmRepositoryManifest(p HelmRepositoryParams) string {
	if p.Interval == "" {
		p.Interval = "30s"
	}
	return manifestMust(helmRepositoryTmpl, p)
}

// HelmReleaseManifest returns the YAML manifest for a Flux HelmRelease for one eg-pair.
func HelmReleaseManifest(p HelmReleaseParams) string {
	if p.Interval == "" {
		p.Interval = "30s"
	}
	if p.SourceNamespace == "" {
		p.SourceNamespace = p.Namespace
	}
	return manifestMust(helmReleaseTmpl, p)
}

// CombinedManifest concatenates multiple YAML manifests with --- separators.
func CombinedManifest(manifests ...string) string {
	return strings.Join(manifests, "\n---\n")
}
