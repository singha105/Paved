#!/usr/bin/env bash
#
# cluster-up.sh: create the local "paved" k3d cluster with its image registry, and install
# the platform stack (cert-manager, Traefik, Argo Rollouts, kube-prometheus-stack, Argo CD). Argo CD
# then delivers the operator and the claims from git (deploy/argocd).
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

# BOOTSTRAP_GITOPS=false stops before applying the Argo CD root app, for callers such as make demo that
# push the images the claims run first.
BOOTSTRAP_GITOPS="${BOOTSTRAP_GITOPS:-true}"

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
    # Traefik is installed with Helm into its own namespace below, so the k3s-bundled
    # copy in kube-system is disabled: NetworkPolicy needs a dedicated ingress namespace.
    k3d cluster create "$CLUSTER_NAME" \
      --image "$K3S_IMAGE" \
      --servers 1 --agents 0 \
      --port "80:80@loadbalancer" \
      --port "443:443@loadbalancer" \
      --registry-use "${REGISTRY}:${REGISTRY_PORT}" \
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

  install_chart argocd argocd argo/argo-cd "$ARGO_CD_VERSION" \
    --values "$VALUES_DIR/argo-cd.yaml"
  k -n argocd wait --for=condition=Available deployment --all --timeout="$WAIT_TIMEOUT"
  k -n argocd rollout status statefulset --timeout="$WAIT_TIMEOUT"
}

# bootstrap_gitops applies the root of the app-of-apps. From then on Argo CD installs and updates
# the operator and the claims from git.
bootstrap_gitops() {
  log "Applying the Argo CD root application (deploy/argocd/root.yaml)"
  k apply -f "$REPO_ROOT/deploy/argocd/root.yaml"
}

main() {
  require_tools
  ensure_registry
  ensure_cluster
  add_repos
  install_stack
  if [ "$BOOTSTRAP_GITOPS" = true ]; then
    bootstrap_gitops
  fi
  log "Cluster '$CLUSTER_NAME' is ready (kubectl context: $KUBE_CONTEXT, registry: localhost:$REGISTRY_PORT)"
}

main "$@"
