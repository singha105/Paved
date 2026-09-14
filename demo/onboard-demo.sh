#!/usr/bin/env bash
#
# onboard-demo.sh: onboard shortlink, a second service that shares nothing with the first, the way a
# team would. Write one ServiceClaim, commit it, and wait for the platform to report it Ready. It
# times the whole thing, from creating the claim file to Ready=True, and counts the lines of YAML
# written.
#
# Expects the paved cluster with Argo CD delivering deploy/claims (hack/cluster-up.sh), the shortlink
# image in the local registry (hack/shortlink-image.sh), no deploy/claims/shortlink.yaml yet, and a
# checkout of main, level with origin, that can push. It commits and pushes the claim.
#
# Recorded with:
#   asciinema rec --headless --window-size 120x34 --idle-time-limit 2 \
#     --command ./demo/onboard-demo.sh demo/01-onboard.cast
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

CLAIM=shortlink
CLAIM_NS=platform-claims
SERVICE_NS=svc-$CLAIM
CLAIM_FILE=deploy/claims/$CLAIM.yaml
IMAGE=k3d-paved-registry:5001/shortlink:0.1.0
LOCAL_PORT=18080
# Appended to the onboarding commit when set, for example a Co-Authored-By trailer.
DEMO_COMMIT_TRAILER=${DEMO_COMMIT_TRAILER:-}

say() { printf '\n\033[1;36m# %s\033[0m\n' "$*"; }
show() { printf '\033[1;32m$\033[0m %s\n' "$*"; }
run() {
  show "$*"
  "$@"
}
fail() {
  printf '\n\033[1;31mdemo failed: %s\033[0m\n' "$*" >&2
  exit 1
}

claim() { kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS" -o jsonpath="$1" 2>/dev/null || true; }
condition() { claim "{.status.conditions[?(@.type==\"$1\")].$2}"; }
state() {
  local exists=absent
  if kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS" >/dev/null 2>&1; then exists=created; fi
  printf 'claim %-7s  ResourcesSynced=%-5s  managed resources %-2s  Ready=%-5s %s' \
    "$exists" "$(condition ResourcesSynced status)" "$(claim '{.status.managedResources}')" \
    "$(condition Ready status)" "$(condition Ready reason)"
}
is_ready() { [ "$(condition Ready status)" = True ]; }

# wait_for DESCRIPTION TIMEOUT CHECK...: print the state each time it changes, until CHECK
# succeeds. Fails the demo if TIMEOUT seconds pass first.
wait_for() {
  local description=$1 timeout=$2 start=$SECONDS last="" now
  shift 2
  printf '  waiting until %s\n' "$description"
  while true; do
    now=$(state)
    if [ "$now" != "$last" ]; then
      printf '  %s\n' "$now"
      last=$now
    fi
    if "$@"; then return 0; fi
    if ((SECONDS - start > timeout)); then fail "timed out after ${timeout}s waiting until $description"; fi
    sleep 2
  done
}

PORT_FORWARD_PID=""
cleanup() {
  if [ -n "$PORT_FORWARD_PID" ]; then kill "$PORT_FORWARD_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT

for tool in curl git kubectl python3; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
kubectl get application platform-claims -n argocd >/dev/null || fail "Argo CD doesn't deliver the claims; run hack/cluster-up.sh"
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || fail "check out main first"
git diff --cached --quiet || fail "the index has staged changes"
git fetch -q origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main is not level with origin/main"
[ ! -e "$CLAIM_FILE" ] || fail "$CLAIM_FILE already exists: shortlink is already onboarded"
if kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS" >/dev/null 2>&1; then fail "the $CLAIM claim already exists"; fi
curl -sf "http://localhost:5001/v2/shortlink/manifests/0.1.0" -H 'Accept: application/vnd.docker.distribution.manifest.v2+json' >/dev/null ||
  fail "$IMAGE is not in the registry; run hack/shortlink-image.sh"

say "shortlink is a URL shortener with its own code, module and image. Nothing runs it yet"
run ls services/shortlink
run kubectl get serviceclaims -n "$CLAIM_NS"

say "Onboarding it is one file. The stopwatch starts when the file is created"
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
cat >"$CLAIM_FILE" <<EOF
apiVersion: platform.paved.dev/v1alpha1
kind: ServiceClaim
metadata:
  name: $CLAIM
  namespace: $CLAIM_NS
spec:
  owner: team-growth
  image: $IMAGE
  port: 8080
  tier: internal
  sli:
    type: http-availability
  slo:
    objective: "99.9"
    window: 28d
  scale:
    min: 2
    max: 4
EOF
run cat "$CLAIM_FILE"
yaml_lines=$(grep -cvE '^[[:space:]]*(#|$)' "$CLAIM_FILE")

say "$yaml_lines lines of YAML. Commit them: Argo CD applies what is on main"
message="feat(claims): onboard shortlink, a second service"
if [ -n "$DEMO_COMMIT_TRAILER" ]; then message=$(printf '%s\n\n%s' "$message" "$DEMO_COMMIT_TRAILER"); fi
run git add "$CLAIM_FILE"
show "git commit -m \"feat(claims): onboard shortlink, a second service\""
git commit -q -m "$message"
run git push -q origin main
# Argo CD would hear about the push from a GitHub webhook; this cluster can't receive one.
run kubectl annotate application platform-claims -n argocd argocd.argoproj.io/refresh=normal --overwrite

wait_for "Argo CD creates the claim, and paved builds the service and reports it Ready" 900 is_ready
ready_at=$(condition Ready lastTransitionTime)
onboard_seconds=$(python3 -c 'import sys; from datetime import datetime as d; p = lambda s: d.fromisoformat(s.replace("Z", "+00:00")); print(int((p(sys.argv[2]) - p(sys.argv[1])).total_seconds()))' "$started" "$ready_at")
say "Ready=True ${onboard_seconds}s after the claim file was created (file $started, Ready $ready_at)"
run kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS"

say "What those $yaml_lines lines became: the namespace, and everything in it that paved manages"
run kubectl get namespace "$SERVICE_NS"
run kubectl get serviceaccount,rollout,analysistemplate,service,horizontalpodautoscaler,poddisruptionbudget,networkpolicy,servicemonitor,prometheusrule,configmap \
  -n "$SERVICE_NS" -l app.kubernetes.io/managed-by=paved

say "It serves. An internal service has no Ingress, so reach it through a port-forward"
show "kubectl port-forward -n $SERVICE_NS svc/$CLAIM $LOCAL_PORT:80 &"
kubectl port-forward -n "$SERVICE_NS" "svc/$CLAIM" "$LOCAL_PORT:80" >/dev/null 2>&1 &
PORT_FORWARD_PID=$!
for _ in $(seq 1 30); do
  if curl -sf "http://localhost:$LOCAL_PORT/readyz" >/dev/null; then break; fi
  sleep 1
done
show "curl -s -X POST localhost:$LOCAL_PORT/links -d '{\"url\": \"https://github.com/singha105/paved\"}'"
created=$(curl -sf -X POST "http://localhost:$LOCAL_PORT/links" -d '{"url": "https://github.com/singha105/paved"}') ||
  fail "creating a link failed"
echo "$created"
short=$(printf '%s' "$created" | python3 -c 'import json, sys; print(json.load(sys.stdin)["short"])')
show "curl -si localhost:$LOCAL_PORT$short"
curl -si "http://localhost:$LOCAL_PORT$short" | grep -E '^(HTTP|Location)' || fail "following the link failed"

say "Done: $yaml_lines lines of YAML, Ready=True in ${onboard_seconds}s, with no ticket and no kubectl apply"
echo "onboard_seconds=$onboard_seconds yaml_lines=$yaml_lines"
