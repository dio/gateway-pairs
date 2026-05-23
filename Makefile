EG_VERSION ?= v1.8.0
CLUSTER ?= gw-pairs-e2e
KTX = k3d-$(CLUSTER)
PAIR ?= 1

.PHONY: cluster cluster-delete crds-install pair-install pair-delete e2e lint helm-lint

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

## pair-install: install one eg-pair release (PAIR=1 by default)
pair-install:
	helm --kube-context $(KTX) upgrade --install eg-pair-$(PAIR) ./charts/eg-pair \
	  --namespace tr-system-$(PAIR) --create-namespace \
	  --set pair.index=$(PAIR) \
	  --skip-crds \
	  --wait --timeout 120s
	kubectl --context $(KTX) rollout status deployment/envoy-gateway \
	  -n tr-system-$(PAIR) --timeout=120s

## pair-delete: delete one eg-pair release (PAIR=1 by default)
pair-delete:
	helm --kube-context $(KTX) uninstall eg-pair-$(PAIR) -n tr-system-$(PAIR) || true

## e2e: run full e2e suite (creates cluster, installs CRDs, installs 2 pairs, verifies)
e2e:
	cd e2e && RUN_PAIRS_E2E=1 go test -v -count=1 -tags=e2e -run TestGatewayPairs ./...

## helm-lint: lint both charts
helm-lint:
	helm lint ./charts/eg-crds
	helm lint ./charts/eg-pair

## lint: alias
lint: helm-lint
