# Multi-tier example (L1 edge + L2 backend pattern)
#
# Two independent Envoy proxies under one EG controller (one pair).
# Each tier has its own EnvoyProxy CR, Gateway, and HTTPRoutes.
# EG places each proxy Deployment in tr-dataplane-1 (GatewayNamespace mode).
#
# Install the pair first:
#
#   gwp crds install
#   gwp pair install 1
#
# Then apply this directory:
#
#   kubectl apply -n tr-dataplane-1 -f examples/multi-tier/
#
# Coupling fields (from: gwp pair info 1):
#   gatewayClassName:   tr-1
#   dataplaneNamespace: tr-dataplane-1
#
# Architecture:
#   client → Gateway/l1 (EnvoyProxy/l1) → routes to L2
#          → Gateway/l2 (EnvoyProxy/l2) → routes to backend services
