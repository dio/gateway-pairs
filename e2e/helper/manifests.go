// Package helper provides shared utilities for gateway-pairs e2e tests.
package helper

import "fmt"

func TestEnvoyProxyManifest(ns, gcName string) string {
	return fmt.Sprintf(`apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: eg-test
  namespace: %s
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
`, ns)
}

func TestGatewayManifest(ns, gcName string) string {
	return fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: eg-test
  namespace: %s
spec:
  gatewayClassName: %s
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
`, ns, gcName)
}

func EchoDeploymentManifest(ns string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo
  namespace: %s
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
`, ns)
}

func EchoServiceManifest(ns string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: echo
  namespace: %s
spec:
  selector:
    app: echo
  ports:
  - port: 80
    targetPort: 80
`, ns)
}

func HTTPRouteManifest(gatewayName, ns string) string {
	return fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo
  namespace: %s
spec:
  parentRefs:
  - name: %s
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /
    backendRefs:
    - name: echo
      port: 80
`, ns, gatewayName)
}
