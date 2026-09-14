#!/usr/bin/env bash
#
# update-test-crds.sh: download the third-party CRDs the controller watches into test/crds,
# for envtest and the e2e suite. Versions match the charts pinned in hack/cluster-up.sh.
set -euo pipefail

ARGO_ROLLOUTS_VERSION="v1.10.0"        # argo-rollouts chart 2.43.1
PROMETHEUS_OPERATOR_VERSION="v0.93.1"  # kube-prometheus-stack chart 90.1.2

DEST="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/test/crds"
mkdir -p "$DEST"

fetch() {
  local url="$1" file="$2"
  echo "==> $file"
  curl --fail --silent --show-error --location "$url" --output "$DEST/$file"
}

for crd in rollout analysis-template; do
  fetch "https://raw.githubusercontent.com/argoproj/argo-rollouts/${ARGO_ROLLOUTS_VERSION}/manifests/crds/${crd}-crd.yaml" \
    "argoproj.io_${crd//-/}s.yaml"
done

for crd in servicemonitors prometheusrules; do
  fetch "https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/${PROMETHEUS_OPERATOR_VERSION}/example/prometheus-operator-crd/monitoring.coreos.com_${crd}.yaml" \
    "monitoring.coreos.com_${crd}.yaml"
done
