#!/usr/bin/env bash
#
# cluster-up.sh: create the local "paved" k3d cluster and install the platform stack
# (cert-manager, Traefik, Argo Rollouts, kube-prometheus-stack).
#
# Idempotent: re-running reuses the existing cluster and upgrades each Helm release in
# place. Readiness is checked with `kubectl wait`, never `sleep`.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-paved}"
KUBE_CONTEXT="k3d-${CLUSTER_NAME}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-600s}"
VALUES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/values"

# Pinned on Day 1. PROGRESS.md records why these exact versions; change them together.
K3S_IMAGE="rancher/k3s:v1.36.4-k3s1"
CERT_MANAGER_VERSION="v1.21.2"
TRAEFIK_VERSION="41.5.0"
ARGO_ROLLOUTS_VERSION="2.43.1"
KUBE_PROMETHEUS_STACK_VERSION="90.1.2"

log() { printf '\n==> %s\n' "$*"; }

k() { kubectl --context "$KUBE_CONTEXT" "$@"; }

require_tools() {
  local bin
  for bin in docker k3d kubectl helm; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      echo "error: required tool not found on PATH: $bin" >&2
      exit 1
    fi
  done
}

ensure_cluster() {
  if k3d cluster get "$CLUSTER_NAME" >/dev/null 2>&1; then
    log "Cluster '$CLUSTER_NAME' exists; making sure it is running"
    k3d cluster start "$CLUSTER_NAME" --wait
  else
    log "Creating cluster '$CLUSTER_NAME' ($K3S_IMAGE)"
    # Traefik is installed with Helm into its own namespace below, so the k3s-bundled
    # copy in kube-system is disabled: NetworkPolicy needs a dedicated ingress namespace.
    k3d cluster create "$CLUSTER_NAME" \
      --image "$K3S_IMAGE" \
      --servers 1 --agents 0 \
      --port "80:80@loadbalancer" \
      --port "443:443@loadbalancer" \
      --k3s-arg "--disable=traefik@server:*" \
      --wait
  fi

  k3d kubeconfig merge "$CLUSTER_NAME" --kubeconfig-merge-default --kubeconfig-switch-context >/dev/null
  k wait --for=condition=Ready node --all --timeout="$WAIT_TIMEOUT"
  # k3s creates its add-ons shortly after the node is up; metrics-server backs the HPAs.
  k -n kube-system wait --for=create --for=condition=Available \
    deployment/coredns deployment/metrics-server --timeout="$WAIT_TIMEOUT"
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
}

main() {
  require_tools
  ensure_cluster
  add_repos
  install_stack
  log "Cluster '$CLUSTER_NAME' is ready (kubectl context: $KUBE_CONTEXT)"
}

main "$@"
