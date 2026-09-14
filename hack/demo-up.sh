#!/usr/bin/env bash
#
# demo-up.sh: what `make demo` runs. From a clean clone, it creates the paved k3d cluster, its
# registry and the platform stack, pushes the images the example claims run, hands the platform to
# Argo CD, and waits until the operator is deployed and every claim is Ready. Safe to re-run.
#
# Argo CD deploys from github.com/singha105/paved, so the cluster runs what is on main: the operator
# image CI published to GHCR and the claims in deploy/claims.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WAIT_SECONDS="${WAIT_SECONDS:-900}"

log() { printf '\n==> %s\n' "$*"; }
fail() {
  printf '\nerror: %s\n' "$*" >&2
  exit 1
}

# wait_until DESCRIPTION CHECK...: run CHECK every 5 seconds until it succeeds, for up to WAIT_SECONDS.
wait_until() {
  local description=$1 start=$SECONDS
  shift
  printf '    waiting until %s\n' "$description"
  until "$@"; do
    ((SECONDS - start < WAIT_SECONDS)) || fail "timed out after ${WAIT_SECONDS}s waiting until $description"
    sleep 5
  done
}

apps_synced_and_healthy() {
  local states
  states=$(kubectl get applications -n argocd \
    -o jsonpath='{range .items[*]}{.status.sync.status}/{.status.health.status}{"\n"}{end}' 2>/dev/null) || return 1
  [ "$(printf '%s\n' "$states" | grep -c .)" -ge 3 ] && ! printf '%s\n' "$states" | grep -qv '^Synced/Healthy$'
}

# claims_ready: every claim file in deploy/claims has become a claim, and all of them report Ready.
claims_ready() {
  local want ready
  want=$(grep -l '^kind: ServiceClaim' "$ROOT"/deploy/claims/*.yaml | wc -l | tr -d ' ')
  ready=$(kubectl get serviceclaims -n platform-claims \
    -o jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' 2>/dev/null | grep -c '^True$') || return 1
  [ "$ready" -ge "$want" ]
}

start=$SECONDS
for tool in docker k3d kubectl helm curl; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool not found on PATH: $tool"
done

log "Creating the cluster, its registry and the platform stack (hack/cluster-up.sh)"
BOOTSTRAP_GITOPS=false "$ROOT/hack/cluster-up.sh"

log "Pushing the images the example claims and the demos run"
for tag in 0.1.1 0.1.2 0.2.0; do
  TAG="$tag" "$ROOT/hack/testsvc-image.sh"
done
BROKEN=true TAG=0.2.1-bad "$ROOT/hack/testsvc-image.sh"
"$ROOT/hack/shortlink-image.sh"

log "Handing the platform to Argo CD (deploy/argocd/root.yaml)"
kubectl apply -f "$ROOT/deploy/argocd/root.yaml"
wait_until "Argo CD has synced the operator and the claims from git" apps_synced_and_healthy
wait_until "every claim in deploy/claims is Ready" claims_ready

log "paved is up, in $((SECONDS - start))s"
kubectl get applications -n argocd
kubectl get serviceclaims -n platform-claims

cat <<'EOF'

Next:
  ./demo/02-drift.sh         delete a managed PrometheusRule and watch it come back
  ./demo/freeze-demo.sh      spend url-shortener's error budget and watch deploys freeze
  ./demo/canary-demo.sh      ship a failing release through git and watch its canary abort (pushes to main)
  ./demo/onboard-demo.sh     onboard a service with one claim (pushes to main; needs deploy/claims/shortlink.yaml removed)

The demos need ApacheBench (ab) and the kubectl argo rollouts plugin.
EOF
