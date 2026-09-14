#!/usr/bin/env bash
#
# canary-demo.sh: ship a release that fails every request the way every release ships, as a commit
# to git, and watch Argo Rollouts abort its canary with nobody touching the cluster, because the
# claim's SLI error ratio passed 5%. Then revert the commit and watch the good version return.
#
# Expects the paved cluster with Argo CD delivering deploy/claims (hack/cluster-up.sh), the
# url-shortener claim Ready on testsvc 0.1.1, testsvc:0.2.1-bad in the local registry
# (BROKEN=true TAG=0.2.1-bad ./hack/testsvc-image.sh), and a checkout of main, level with origin, that
# can push. It commits and pushes twice: the bad image, then its revert.
#
# Runs unattended, prints the measured abort time, and exits non-zero if the canary is not aborted
# on its own. Recorded with:
#   asciinema rec --headless --window-size 120x34 --idle-time-limit 2 \
#     --command ./demo/canary-demo.sh demo/04-canary.cast
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

CLAIM=url-shortener
CLAIM_NS=platform-claims
ROLLOUT_NS=svc-$CLAIM
CLAIM_FILE=deploy/claims/$CLAIM.yaml
REGISTRY=k3d-paved-registry:5001
GOOD=$REGISTRY/testsvc:0.1.1
BAD=$REGISTRY/testsvc:0.2.1-bad
HOST=$CLAIM.localhost
INGRESS=http://127.0.0.1
SLI_RULE="sli:http_availability:error_ratio_rate5m{service=\"$CLAIM\"}"
PROMETHEUS=/api/v1/namespaces/monitoring/services/kps-kube-prometheus-stack-prometheus:9090/proxy
# Requests per batch, about one batch a second: enough for the SLI to measure, few enough that the
# canary's failures don't use up the claim's error budget, which would freeze deploys and reject
# the revert.
TRAFFIC_BATCH=10
# Appended to both demo commits when set, for example a Co-Authored-By trailer.
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

rollout() { kubectl get rollout "$CLAIM" -n "$ROLLOUT_NS" -o jsonpath="$1"; }
claim() { kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS" -o jsonpath="$1"; }
claim_condition() { claim "{.status.conditions[?(@.type==\"$1\")].$2}"; }
# sli_5m: the claim's 5m error ratio from the recording rule the analysis reads, or "none".
sli_5m() {
  kubectl get --raw "$PROMETHEUS/api/v1/query?query=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$SLI_RULE")" |
    python3 -c 'import json, sys; r = json.load(sys.stdin)["data"]["result"]; print(f"{float(r[0]["value"][1]):.3f}" if r else "none")'
}
state() {
  printf 'Rollout %-11s step %s/5  aborted=%-5s  analysis %-12s  5m error ratio %-5s  claim Ready=%s' \
    "$(rollout '{.status.phase}')" "$(rollout '{.status.currentStepIndex}')" \
    "$(rollout '{.status.abort}')" "$(rollout '{.status.canary.currentBackgroundAnalysisRunStatus.status}')" \
    "$(sli_5m)" "$(claim_condition Ready status)"
}

rollout_is_healthy_on() { [ "$(rollout '{.status.phase}')/$(rollout '{.spec.template.spec.containers[0].image}')" = "Healthy/$1" ]; }
rollout_has_bad_image() { [ "$(rollout '{.spec.template.spec.containers[0].image}')" = "$BAD" ]; }
# sli_is_clean: the rule returns a number below 0.01, not "none" (no series) or "nan" (no requests in
# 5 minutes). Errors from an earlier bad release stay in the 5m window for 5 minutes, and would fail
# the new canary's analysis before its own pods took any traffic.
sli_is_clean() { awk -v ratio="$(sli_5m)" 'BEGIN { exit !(ratio != "none" && ratio != "nan" && ratio + 0 < 0.01) }'; }
is_aborted() { [ "$(rollout '{.status.abort}')" = true ]; }
only_stable_pods_serve() {
  [ -z "$(kubectl get pods -n "$ROLLOUT_NS" -o jsonpath='{range .items[*]}{.spec.containers[0].image}{"\n"}{end}' | grep -Fx "$BAD")" ]
}
is_restored() {
  rollout_is_healthy_on "$GOOD" &&
    [ "$(claim_condition Ready status)/$(claim_condition Ready observedGeneration)" = "True/$(claim '{.metadata.generation}')" ]
}

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
    sleep 3
  done
}

# commit_claim MESSAGE: commit only the claim file, then push main.
commit_claim() {
  local message=$1
  if [ -n "$DEMO_COMMIT_TRAILER" ]; then message=$(printf '%s\n\n%s' "$message" "$DEMO_COMMIT_TRAILER"); fi
  show "git commit -m \"$1\" -- $CLAIM_FILE"
  git commit -q -m "$message" -- "$CLAIM_FILE"
  run git push -q origin main
}

# refresh_argo: ask Argo CD to compare the claims app with git now. With a GitHub webhook it would
# hear about the push by itself; this cluster can't receive one.
refresh_argo() {
  run kubectl annotate application platform-claims -n argocd argocd.argoproj.io/refresh=normal --overwrite
}

TRAFFIC=$(mktemp -t paved-canary-demo)
cleanup() {
  rm -f "$TRAFFIC"
  wait
}
trap cleanup EXIT

for tool in ab git kubectl kubectl-argo-rollouts python3; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
kubectl get application platform-claims -n argocd >/dev/null || fail "Argo CD doesn't deliver the claims; run hack/cluster-up.sh"
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || fail "check out main first"
git diff --quiet -- "$CLAIM_FILE" && git diff --cached --quiet || fail "$CLAIM_FILE or the index has uncommitted changes"
git fetch -q origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main is not level with origin/main"
grep -qF "image: $GOOD" "$CLAIM_FILE" || fail "$CLAIM_FILE doesn't run $GOOD"
[ "$(claim_condition DeploysFrozen status)" != True ] || fail "$CLAIM's deploys are frozen, so the revert would be rejected"

say "url-shortener runs 0.1.1. Argo CD applies its claim from git, and nobody runs kubectl apply"
run kubectl get applications -n argocd
wait_for "the Rollout is healthy on 0.1.1" 600 rollout_is_healthy_on "$GOOD"

say "Traffic: a steady trickle of requests for the whole demo"
(
  while [ -f "$TRAFFIC" ]; do
    ab -q -k -n "$TRAFFIC_BATCH" -c 1 -H "Host: $HOST" "$INGRESS/" >/dev/null 2>&1 || true
    sleep 1
  done
) &
wait_for "the 5m SLI, the rule the canary analysis reads, has data and no recent errors" 900 sli_is_clean

say "A bad release, 0.2.1-bad, fails every request. It ships like any release: as a commit to git"
sed -i '' "s#image: $GOOD#image: $BAD#" "$CLAIM_FILE"
run git --no-pager diff -- "$CLAIM_FILE"
commit_claim "demo: ship url-shortener 0.2.1-bad, a release that fails every request"
refresh_argo
wait_for "Argo CD applies the claim and paved passes the new image to the Rollout" 300 rollout_has_bad_image

say "The canary starts. From its first pause, the analysis reads the SLI every 30 seconds and fails above 0.05"
wait_for "Argo Rollouts aborts the canary on its own" 600 is_aborted

# The abort time comes from the cluster's own timestamps, not from this script's polling: paved's
# server-side apply entry on the Rollout records when it applied the bad image, and Argo Rollouts
# records when it aborted.
applied=$(kubectl get rollout "$CLAIM" -n "$ROLLOUT_NS" -o json --show-managed-fields | python3 -c '
import json, sys
fields = json.load(sys.stdin)["metadata"]["managedFields"]
print(next(f["time"] for f in fields if f["manager"] == "paved-controller" and f["operation"] == "Apply" and not f.get("subresource")))')
aborted=$(rollout '{.status.abortedAt}')
abort_seconds=$(python3 -c 'import sys; from datetime import datetime as d; p = lambda s: d.fromisoformat(s.replace("Z", "+00:00")); print(int((p(sys.argv[2]) - p(sys.argv[1])).total_seconds()))' "$applied" "$aborted")
say "Aborted ${abort_seconds}s after the bad image reached the Rollout (applied $applied, aborted $aborted)"

run kubectl argo rollouts get rollout "$CLAIM" -n "$ROLLOUT_NS"
# An aborted rollout no longer names its analysis run, so take the newest one.
analysis_run=$(kubectl get analysisrun -n "$ROLLOUT_NS" --sort-by=.metadata.creationTimestamp -o name | tail -1)
[ -n "$analysis_run" ] || fail "no AnalysisRun found in $ROLLOUT_NS"
show "kubectl get $analysis_run -n $ROLLOUT_NS -o jsonpath='{.status.metricResults[*].measurements[*]}'"
kubectl get "$analysis_run" -n "$ROLLOUT_NS" \
  -o jsonpath='{range .status.metricResults[*].measurements[*]}  {.finishedAt}  {.phase}  error ratio {.value}{"\n"}{end}'

wait_for "the canary is scaled down and only 0.1.1 pods serve" 180 only_stable_pods_serve
run kubectl get pods -n "$ROLLOUT_NS" \
  -o custom-columns='POD:.metadata.name,IMAGE:.spec.containers[0].image,READY:.status.containerStatuses[0].ready'
run kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS"
claim_condition Ready message
echo

say "Git still asks for 0.2.1-bad. Revert the commit, so git and the cluster agree on 0.1.1 again"
show "git revert --no-commit HEAD"
git revert --no-commit HEAD
commit_claim "demo: revert url-shortener to 0.1.1 after its canary aborted"
refresh_argo
wait_for "the Rollout is healthy on 0.1.1 and the claim is Ready" 600 is_restored
run kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS"

say "Done: the SLI that pages and freezes deploys also stopped this one, ${abort_seconds}s in, with no human action"
echo "abort_seconds=$abort_seconds"
