// Package testutil provides shared utilities for gateway-pairs e2e tests.
package testutil

import (
	"bytes"
	"text/template"
)

func must(t *template.Template, data any) string {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(err)
	}
	return buf.String()
}

var testEnvoyProxyTmpl = template.Must(template.New("envoyproxy").Parse(`apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: eg-test
  namespace: {{ .Namespace }}
spec:
  provider:
    type: Kubernetes
    kubernetes:
      envoyService:
        name: eg-test
        type: ClusterIP
      envoyDeployment:
        pod:
          labels:
            eg-pair-test: "true"
`))

var testGatewayTmpl = template.Must(template.New("gateway").Parse(`apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: eg-test
  namespace: {{ .Namespace }}
spec:
  gatewayClassName: {{ .GatewayClassName }}
  infrastructure:
    parametersRef:
      group: gateway.envoyproxy.io
      kind: EnvoyProxy
      name: eg-test
  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: Same
`))

var echoDeploymentTmpl = template.Must(template.New("echo-deploy").Parse(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo
  namespace: {{ .Namespace }}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo
  template:
    metadata:
      labels:
        app: echo
    spec:
      containers:
      - name: echo
        image: ealen/echo-server:latest
        ports:
        - containerPort: 80
`))

var echoServiceTmpl = template.Must(template.New("echo-svc").Parse(`apiVersion: v1
kind: Service
metadata:
  name: echo
  namespace: {{ .Namespace }}
spec:
  selector:
    app: echo
  ports:
  - port: 80
    targetPort: 80
`))

var httpRouteTmpl = template.Must(template.New("httproute").Parse(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo
  namespace: {{ .Namespace }}
spec:
  parentRefs:
  - name: {{ .GatewayName }}
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /
    backendRefs:
    - name: echo
      port: 80
`))

func TestEnvoyProxyManifest(ns, _ string) string {
	return must(testEnvoyProxyTmpl, struct{ Namespace string }{ns})
}

func TestGatewayManifest(ns, gcName string) string {
	return must(testGatewayTmpl, struct {
		Namespace        string
		GatewayClassName string
	}{ns, gcName})
}

func EchoDeploymentManifest(ns string) string {
	return must(echoDeploymentTmpl, struct{ Namespace string }{ns})
}

func EchoServiceManifest(ns string) string {
	return must(echoServiceTmpl, struct{ Namespace string }{ns})
}

func HTTPRouteManifest(gatewayName, ns string) string {
	return must(httpRouteTmpl, struct {
		Namespace   string
		GatewayName string
	}{ns, gatewayName})
}
