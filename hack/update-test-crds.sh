#!/usr/bin/env bash
#
# update-test-crds.sh: download the third-party CRDs the controller watches into test/crds,
# for envtest and the e2e suite. Versions match the charts pinned in hack/cluster-up.sh.
set -euo pipefail

ARGO_ROLLOUTS_VERSION="v1.10.0"        # argo-rollouts chart 2.43.1
PROMETHEUS_OPERATOR_VERSION="v0.93.1"  # kube-prometheus-stack chart 90.1.2
ACK_IAM_CONTROLLER_VERSION="v1.9.0"   # ACK iam-chart 1.9.0 (Day 8)
ACK_S3_CONTROLLER_VERSION="v1.12.1"   # ACK s3-chart 1.12.1 (Day 8)

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

# The two ACK kinds a claim with storage produces. The rest of each controller's CRDs aren't needed.
fetch "https://raw.githubusercontent.com/aws-controllers-k8s/iam-controller/${ACK_IAM_CONTROLLER_VERSION}/config/crd/bases/iam.services.k8s.aws_roles.yaml" \
  "iam.services.k8s.aws_roles.yaml"
fetch "https://raw.githubusercontent.com/aws-controllers-k8s/s3-controller/${ACK_S3_CONTROLLER_VERSION}/config/crd/bases/s3.services.k8s.aws_buckets.yaml" \
  "s3.services.k8s.aws_buckets.yaml"
