# Single-tier example
#
# A single Envoy proxy serving all traffic for pair 1.
# Install the pair first:
#
#   gwp crds install
#   gwp pair install 1
#
# Then apply this directory:
#
#   kubectl apply -n tr-dataplane-1 -f examples/single-tier/
#
# Coupling fields (from: gwp pair info 1):
#   gatewayClassName:   tr-1
#   dataplaneNamespace: tr-dataplane-1
