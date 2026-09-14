#!/usr/bin/env bash
#
# cluster-up.sh: create the local "paved" k3d cluster with its image registry, and install
# the platform stack (cert-manager, Traefik, Argo Rollouts, kube-prometheus-stack, Argo CD). Argo CD
# then delivers the operator and the claims from git (deploy/argocd).
#
# With PAVED_AWS_PROFILE set, it also links the cluster to AWS for claims with storage
# (DECISIONS.md, ADR-025): it applies infra/aws with that profile's short-lived session, creates the
# cluster with a public service-account issuer, publishes the issuer's signing keys to S3, installs
# the ACK IAM and S3 controllers, and writes paved's AWS settings. Without it, nothing touches AWS.
#
# Idempotent: re-running reuses the registry and the cluster and upgrades each Helm release in
# place. Readiness is checked with `kubectl wait`, never `sleep`.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-paved}"
KUBE_CONTEXT="k3d-${CLUSTER_NAME}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-600s}"
VALUES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/values"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The registry is localhost:${REGISTRY_PORT} from this machine and ${REGISTRY}:${REGISTRY_PORT}
# from inside the cluster. Port 5000 is taken by macOS AirPlay Receiver.
REGISTRY="k3d-paved-registry"
REGISTRY_PORT="${REGISTRY_PORT:-5001}"

# Pinned on Day 1. PROGRESS.md records why these exact versions; change them together.
K3S_IMAGE="rancher/k3s:v1.36.4-k3s1"
CERT_MANAGER_VERSION="v1.21.2"
TRAEFIK_VERSION="41.5.0"
ARGO_ROLLOUTS_VERSION="2.43.1"
KUBE_PROMETHEUS_STACK_VERSION="90.1.2"
ARGO_CD_VERSION="10.9.0"  # Argo CD v3.5.2, added on Day 6
ACK_IAM_CHART_VERSION="1.9.0"   # ACK iam-controller v1.9.0, added on Day 8
ACK_S3_CHART_VERSION="1.12.1"   # ACK s3-controller v1.12.1, added on Day 8

# BOOTSTRAP_GITOPS=false stops before applying the Argo CD root app, for callers such as make demo that
# push the images the claims run first.
BOOTSTRAP_GITOPS="${BOOTSTRAP_GITOPS:-true}"

# PAVED_AWS_PROFILE names an AWS CLI profile with a short-lived session (aws login --profile <name>).
PAVED_AWS_PROFILE="${PAVED_AWS_PROFILE:-}"
ACK_NAMESPACE="ack-system"
PAVED_NAMESPACE="paved-system"
# Where each ACK controller's projected service-account token is mounted, as in claims' pods.
AWS_TOKEN_DIR="/var/run/secrets/paved.dev/aws"

log() { printf '\n==> %s\n' "$*"; }

k() { kubectl --context "$KUBE_CONTEXT" "$@"; }

aws_enabled() { [ -n "$PAVED_AWS_PROFILE" ]; }

require_tools() {
  local bin tools=(docker k3d kubectl helm)
  if aws_enabled; then
    tools+=(aws terraform curl)
  fi
  for bin in "${tools[@]}"; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      echo "error: required tool not found on PATH: $bin" >&2
      exit 1
    fi
  done
}

# link_aws applies infra/aws and reads what the cluster needs from its outputs.
link_aws() {
  log "Linking the cluster to AWS with profile '$PAVED_AWS_PROFILE' (hack/aws-up.sh)"
  AWS_PROFILE="$PAVED_AWS_PROFILE" "$REPO_ROOT/hack/aws-up.sh"

  local tf=(terraform -chdir="$REPO_ROOT/infra/aws" output -raw)
  AWS_ACCOUNT_ID="$("${tf[@]}" account_id)"
  AWS_REGION="$("${tf[@]}" region)"
  ISSUER_HOST="$("${tf[@]}" issuer_host)"
  ISSUER_BUCKET="$("${tf[@]}" issuer_bucket)"
  PERMISSIONS_BOUNDARY_ARN="$("${tf[@]}" permissions_boundary_arn)"
  ACK_IAM_ROLE_ARN="$("${tf[@]}" ack_iam_role_arn)"
  ACK_S3_ROLE_ARN="$("${tf[@]}" ack_s3_role_arn)"
}

ensure_registry() {
  if k3d registry list --no-headers 2>/dev/null | awk '{print $1}' | grep -qx "$REGISTRY"; then
    log "Registry '$REGISTRY' exists; making sure it is running"
    docker start "$REGISTRY" >/dev/null
  else
    log "Creating registry '$REGISTRY' on localhost:${REGISTRY_PORT}"
    # k3d adds the "k3d-" prefix to the name it is given.
    k3d registry create "${REGISTRY#k3d-}" --port "127.0.0.1:${REGISTRY_PORT}"
  fi
}

ensure_cluster() {
  if k3d cluster get "$CLUSTER_NAME" >/dev/null 2>&1; then
    log "Cluster '$CLUSTER_NAME' exists; making sure it is running"
    k3d cluster start "$CLUSTER_NAME" --wait
    if ! docker exec "k3d-${CLUSTER_NAME}-server-0" cat /etc/rancher/k3s/registries.yaml 2>/dev/null |
      grep -q "$REGISTRY"; then
      echo "error: cluster '$CLUSTER_NAME' is not connected to registry '$REGISTRY'." >&2
      echo "       A registry can only be attached at creation. Recreate the cluster:" >&2
      echo "       k3d cluster delete $CLUSTER_NAME && $0" >&2
      exit 1
    fi
  else
    log "Creating cluster '$CLUSTER_NAME' ($K3S_IMAGE)"
    local issuer_args=()
    if aws_enabled; then
      # The API server signs service-account tokens as the public issuer, so AWS STS can check them
      # against the keys publish_issuer puts in S3.
      issuer_args=(
        --k3s-arg "--kube-apiserver-arg=service-account-issuer=https://${ISSUER_HOST}@server:*"
        --k3s-arg "--kube-apiserver-arg=service-account-jwks-uri=https://${ISSUER_HOST}/openid/v1/jwks@server:*"
      )
    fi
    # Traefik is installed with Helm into its own namespace below, so the k3s-bundled
    # copy in kube-system is disabled: NetworkPolicy needs a dedicated ingress namespace.
    k3d cluster create "$CLUSTER_NAME" \
      --image "$K3S_IMAGE" \
      --servers 1 --agents 0 \
      --port "80:80@loadbalancer" \
      --port "443:443@loadbalancer" \
      --registry-use "${REGISTRY}:${REGISTRY_PORT}" \
      --k3s-arg "--disable=traefik@server:*" \
      ${issuer_args[@]+"${issuer_args[@]}"} \
      --wait
  fi

  k3d kubeconfig merge "$CLUSTER_NAME" --kubeconfig-merge-default --kubeconfig-switch-context >/dev/null
  k wait --for=condition=Ready node --all --timeout="$WAIT_TIMEOUT"
  # k3s creates its add-ons shortly after the node is up; metrics-server backs the HPAs.
  k -n kube-system wait --for=create --for=condition=Available \
    deployment/coredns deployment/metrics-server --timeout="$WAIT_TIMEOUT"
}

# publish_issuer puts the cluster's OIDC discovery document and token signing keys where AWS STS
# reads them. A new cluster has new keys, so every run replaces the previous cluster's: tokens from a
# deleted cluster stop working. It then reads the keys back the way AWS will, anonymously over HTTPS.
publish_issuer() {
  local dir
  dir="$(mktemp -d)"
  k get --raw /.well-known/openid-configuration >"$dir/openid-configuration"
  k get --raw /openid/v1/jwks >"$dir/jwks"
  if ! grep -q "\"issuer\":\"https://${ISSUER_HOST}\"" "$dir/openid-configuration"; then
    echo "error: cluster '$CLUSTER_NAME' was not created with the public issuer https://${ISSUER_HOST}." >&2
    echo "       The issuer can only be set at creation. Recreate the cluster:" >&2
    echo "       k3d cluster delete $CLUSTER_NAME && PAVED_AWS_PROFILE=$PAVED_AWS_PROFILE $0" >&2
    exit 1
  fi

  log "Publishing the cluster's issuer documents to https://${ISSUER_HOST}"
  local document
  for document in .well-known/openid-configuration openid/v1/jwks; do
    AWS_PROFILE="$PAVED_AWS_PROFILE" AWS_REGION="$AWS_REGION" aws s3 cp "$dir/$(basename "$document")" "s3://${ISSUER_BUCKET}/${document}" \
      --content-type application/json --cache-control max-age=60 --only-show-errors
  done
  if [ "$(curl --fail --silent --show-error "https://${ISSUER_HOST}/openid/v1/jwks")" != "$(cat "$dir/jwks")" ]; then
    echo "error: https://${ISSUER_HOST}/openid/v1/jwks does not serve this cluster's signing keys" >&2
    exit 1
  fi
  rm -rf "$dir"
}

add_repos() {
  log "Adding Helm repositories"
  helm repo add jetstack https://charts.jetstack.io --force-update >/dev/null
  helm repo add traefik https://traefik.github.io/charts --force-update >/dev/null
  helm repo add argo https://argoproj.github.io/argo-helm --force-update >/dev/null
  helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update >/dev/null
  helm repo update jetstack traefik argo prometheus-community >/dev/null
}

# install_chart RELEASE NAMESPACE CHART VERSION [extra helm args...]
install_chart() {
  local release="$1" namespace="$2" chart="$3" version="$4"
  shift 4
  log "Installing $chart $version (release '$release' in namespace '$namespace')"
  helm upgrade --install "$release" "$chart" \
    --kube-context "$KUBE_CONTEXT" \
    --namespace "$namespace" --create-namespace \
    --version "$version" "$@"
}

install_stack() {
  install_chart cert-manager cert-manager jetstack/cert-manager "$CERT_MANAGER_VERSION" \
    --values "$VALUES_DIR/cert-manager.yaml"
  k -n cert-manager wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"

  install_chart traefik traefik traefik/traefik "$TRAEFIK_VERSION"
  k -n traefik wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"

  install_chart argo-rollouts argo-rollouts argo/argo-rollouts "$ARGO_ROLLOUTS_VERSION"
  k -n argo-rollouts wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"

  install_chart kps monitoring prometheus-community/kube-prometheus-stack "$KUBE_PROMETHEUS_STACK_VERSION" \
    --values "$VALUES_DIR/kube-prometheus-stack.yaml"
  k -n monitoring wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"
  # Prometheus and Alertmanager pods are created asynchronously by the operator.
  k -n monitoring wait --for=condition=Available prometheus --all --timeout="$WAIT_TIMEOUT"
  k -n monitoring wait --for=condition=Available alertmanager --all --timeout="$WAIT_TIMEOUT"
  k -n monitoring wait --for=condition=Ready pod --all \
    --field-selector=status.phase!=Succeeded --timeout="$WAIT_TIMEOUT"

  install_chart argocd argocd argo/argo-cd "$ARGO_CD_VERSION" \
    --values "$VALUES_DIR/argo-cd.yaml"
  k -n argocd wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"
  k -n argocd rollout status statefulset --timeout="$WAIT_TIMEOUT"
}

# install_ack_controller SERVICE CHART_VERSION ROLE_ARN installs one ACK controller. It assumes its
# role (infra/aws) with a projected service-account token, the same way claims' pods do, so no AWS
# key is stored in the cluster.
install_ack_controller() {
  local service="$1" version="$2" role_arn="$3" values
  values="$(mktemp)"
  cat >"$values" <<EOF
aws:
  region: ${AWS_REGION}
deployment:
  extraEnvVars:
    - name: AWS_ROLE_ARN
      value: ${role_arn}
    - name: AWS_WEB_IDENTITY_TOKEN_FILE
      value: ${AWS_TOKEN_DIR}/token
  extraVolumes:
    - name: aws-token
      projected:
        sources:
          - serviceAccountToken:
              audience: sts.amazonaws.com
              expirationSeconds: 3600
              path: token
  extraVolumeMounts:
    - name: aws-token
      mountPath: ${AWS_TOKEN_DIR}
      readOnly: true
EOF
  install_chart "ack-${service}-controller" "$ACK_NAMESPACE" \
    "oci://public.ecr.aws/aws-controllers-k8s/${service}-chart" "$version" --values "$values"
  rm -f "$values"
}

install_ack() {
  install_ack_controller iam "$ACK_IAM_CHART_VERSION" "$ACK_IAM_ROLE_ARN"
  install_ack_controller s3 "$ACK_S3_CHART_VERSION" "$ACK_S3_ROLE_ARN"
  k -n "$ACK_NAMESPACE" wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"
}

# configure_paved writes the AWS settings paved reads at startup (internal/controller/storage.go)
# and restarts paved if it already runs, so it picks them up. They name the account, so they stay
# out of git.
configure_paved() {
  log "Writing paved's AWS settings (ConfigMap ${PAVED_NAMESPACE}/paved-aws)"
  k create namespace "$PAVED_NAMESPACE" --dry-run=client -o yaml | k apply -f - >/dev/null
  k -n "$PAVED_NAMESPACE" create configmap paved-aws \
    --from-literal=PAVED_AWS_ACCOUNT_ID="$AWS_ACCOUNT_ID" \
    --from-literal=PAVED_AWS_REGION="$AWS_REGION" \
    --from-literal=PAVED_AWS_OIDC_ISSUER="$ISSUER_HOST" \
    --from-literal=PAVED_AWS_PERMISSIONS_BOUNDARY_ARN="$PERMISSIONS_BOUNDARY_ARN" \
    --dry-run=client -o yaml | k apply -f - >/dev/null
  if k -n "$PAVED_NAMESPACE" get deployment paved-controller-manager >/dev/null 2>&1; then
    k -n "$PAVED_NAMESPACE" rollout restart deployment/paved-controller-manager
  fi
}

# bootstrap_gitops applies the root of the app-of-apps. From then on Argo CD installs and updates
# the operator and the claims from git.
bootstrap_gitops() {
  log "Applying the Argo CD root application (deploy/argocd/root.yaml)"
  k apply -f "$REPO_ROOT/deploy/argocd/root.yaml"
}

main() {
  require_tools
  if aws_enabled; then
    link_aws
  fi
  ensure_registry
  ensure_cluster
  if aws_enabled; then
    publish_issuer
  fi
  add_repos
  install_stack
  if aws_enabled; then
    install_ack
    configure_paved
  fi
  if [ "$BOOTSTRAP_GITOPS" = true ]; then
    bootstrap_gitops
  fi
  log "Cluster '$CLUSTER_NAME' is ready (kubectl context: $KUBE_CONTEXT, registry: localhost:$REGISTRY_PORT)"
}

main "$@"
