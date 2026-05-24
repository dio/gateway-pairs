EG_VERSION   ?= v1.8.0
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
CLUSTER      ?= gw-pairs-e2e
KTX           = k3d-$(CLUSTER)
PAIR         ?= 1
# PAIR_PREFIX controls all namespace and GatewayClass names.
# Default: tr  →  tr-release-1, tr-system-1, tr-dataplane-1, GatewayClass tr-1
# Override: PAIR_PREFIX=tars make pair-install  →  tars-release-1, tars-system-1, ...
# Set PAIR_PREFIX="" to drop the prefix entirely: release-1, system-1, etc.
PAIR_PREFIX  ?= tr

# Derived names from PAIR_PREFIX + PAIR. Mirror _helpers.tpl logic.
_SEP         := $(if $(PAIR_PREFIX),-)
RELEASE_NS    = $(PAIR_PREFIX)$(_SEP)release-$(PAIR)
SYSTEM_NS     = $(PAIR_PREFIX)$(_SEP)system-$(PAIR)

BIN = bin/gwp

.PHONY: all build generate-crds tidy tidy-check vet \
        helm-lint cluster cluster-delete crds-install pair-install pair-delete e2e clean

all: build

## build: build gwp binary (requires generate-crds first)
build: generate-crds
	go build \
	  -ldflags="-s -w \
	    -X main.version=$(VERSION) \
	    -X main.egVersion=$(EG_VERSION) \
	    -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) \
	    -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
	  -o $(BIN) ./cmd/gwp

## generate-crds: pre-render CRD YAML from gateway-crds-helm into charts/crds/
generate-crds:
	@mkdir -p charts/crds
	helm template gateway-api-crds oci://docker.io/envoyproxy/gateway-crds-helm \
	  --version $(EG_VERSION) \
	  --set crds.gatewayAPI.enabled=true \
	  --set crds.gatewayAPI.channel=standard \
	  --set crds.envoyGateway.enabled=false \
	  > charts/crds/gateway-api-standard.yaml
	helm template gateway-api-crds oci://docker.io/envoyproxy/gateway-crds-helm \
	  --version $(EG_VERSION) \
	  --set crds.gatewayAPI.enabled=true \
	  --set crds.gatewayAPI.channel=experimental \
	  --set crds.envoyGateway.enabled=false \
	  > charts/crds/gateway-api-experimental.yaml
	helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
	  --version $(EG_VERSION) \
	  --set crds.gatewayAPI.enabled=false \
	  --set crds.envoyGateway.enabled=true \
	  > charts/crds/envoy-gateway.yaml
	@echo "generated CRDs for EG $(EG_VERSION)"

## tidy: go mod tidy across all modules
tidy:
	go mod tidy
	cd e2e && go mod tidy
	go work sync

## tidy-check: verify modules are tidy (CI)
tidy-check:
	go mod tidy && git diff --exit-code go.mod go.sum
	cd e2e && go mod tidy && git diff --exit-code go.mod go.sum

## vet: go vet all modules
vet:
	go vet ./...
	cd e2e && go vet -tags=e2e ./...

## helm-lint: lint both charts
helm-lint:
	helm lint ./charts/eg-crds
	helm lint ./charts/eg-pair

## cluster: create k3d cluster
cluster:
	k3d cluster create $(CLUSTER) \
	  --agents 0 \
	  --image rancher/k3s:v1.32.2-k3s1 \
	  --k3s-arg --disable=traefik@server:* \
	  --k3s-arg "--kubelet-arg=allowed-unsafe-sysctls=net.ipv4.ip_unprivileged_port_start@server:*"
	kubectl --context $(KTX) wait nodes/k3d-$(CLUSTER)-server-0 \
	  --for=condition=Ready --timeout=120s

## cluster-delete: delete k3d cluster
cluster-delete:
	k3d cluster delete $(CLUSTER)

## crds-install: install Gateway API + EG CRDs (once per cluster)
crds-install:
	KTX=$(KTX) EG_VERSION=$(EG_VERSION) ./hack/install-crds.sh

## pair-install: install one eg-pair release (PAIR=1, PAIR_PREFIX=tr by default)
pair-install:
	helm --kube-context $(KTX) upgrade --install eg-pair-$(PAIR) ./charts/eg-pair \
	  --namespace $(RELEASE_NS) --create-namespace \
	  --set pair.index=$(PAIR) \
	  --set pair.namePrefix=$(PAIR_PREFIX) \
	  --skip-crds \
	  --wait --timeout 120s
	kubectl --context $(KTX) rollout status deployment/envoy-gateway \
	  -n $(SYSTEM_NS) --timeout=120s

## pair-delete: delete one eg-pair release (PAIR=1, PAIR_PREFIX=tr by default)
pair-delete:
	helm --kube-context $(KTX) uninstall eg-pair-$(PAIR) -n $(RELEASE_NS) || true

## e2e: run full e2e suite (PAIR_PREFIX=tr by default)
e2e:
	cd e2e && PAIR_PREFIX=$(PAIR_PREFIX) RUN_PAIRS_E2E=1 \
	  go test -v -count=1 -tags=e2e -run TestGatewayPairs -timeout 15m ./...

## clean: remove build artifacts and generated CRDs
clean:
	rm -rf $(BIN) bin/ dist/ charts/crds/*.yaml
