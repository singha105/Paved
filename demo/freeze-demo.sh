#!/usr/bin/env bash
#
# freeze-demo.sh: break url-shortener until paved freezes its deploys, watch an image bump get
# rejected, ship an audited break-glass hotfix, stop the errors, wait for the error budget to
# recover, and ship the release that was refused.
#
# Expects paved running in the cluster with its webhook (make docker-build deploy), the
# url-shortener claim, and testsvc 0.1.1, 0.1.2 and 0.2.0 in the local registry
# (TAG=<tag> ./hack/testsvc-image.sh). All three tags are the same test service: its errors are
# the requests this script sends to /boom, and they stop when the script stops sending them.
#
# Runs unattended and exits non-zero as soon as a step doesn't behave as described. Recorded with:
#   asciinema rec --headless --window-size 120x34 --idle-time-limit 2 \
#     --command ./demo/freeze-demo.sh demo/03-freeze.cast
set -euo pipefail

CLAIM=url-shortener
CLAIM_NS=platform-claims
ROLLOUT_NS=svc-$CLAIM
REGISTRY=k3d-paved-registry:5001
CURRENT=$REGISTRY/testsvc:0.1.1
HOTFIX=$REGISTRY/testsvc:0.1.2
RELEASE=$REGISTRY/testsvc:0.2.0
HOST=$CLAIM.localhost
INGRESS=http://127.0.0.1
BREAK_GLASS_REASON="INC-1234: hotfix for the 500s"

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

field() { kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS" -o jsonpath="$1"; }
condition() { field "{.status.conditions[?(@.type==\"$1\")].$2}"; }
budget() { field '{.status.errorBudgetRemaining}'; }
burn_rate() { field '{.status.burnRate1h}'; }
state() {
  printf 'budget %-6s  1h burn rate %-5s  DeploysFrozen=%s (%s)  Ready=%s' \
    "$(budget)" "$(burn_rate)" "$(condition DeploysFrozen status)" \
    "$(condition DeploysFrozen reason)" "$(condition Ready status)"
}

has_budget() { [ -n "$(budget)" ]; }
is_frozen() { [ "$(condition DeploysFrozen status)" = True ]; }
is_unfrozen() { [ "$(condition DeploysFrozen status)" = False ]; }
is_burning() { is_frozen && awk -v rate="$(burn_rate)" 'BEGIN { exit !(rate != "" && rate + 0 >= 1) }'; }
is_ready() { [ "$(condition Ready status)/$(condition Ready observedGeneration)" = "True/$(field '{.metadata.generation}')" ]; }
is_released() { [ "$(field '{.spec.image}')" = "$RELEASE" ] && is_ready; }
break_glass_events() {
  kubectl get events -n "$CLAIM_NS" --field-selector reason=BreakGlassUsed -o name | wc -l | tr -d ' '
}
# has_new_break_glass_event: more BreakGlassUsed Events than events_before, counted before the change.
has_new_break_glass_event() { [ "$(break_glass_events)" -gt "$events_before" ]; }

# wait_for DESCRIPTION TIMEOUT CHECK...: print the claim's state each time it changes, until
# CHECK succeeds. Fails the demo if TIMEOUT seconds pass first.
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

# patch_image IMAGE [ANNOTATIONS_JSON]: change the claim's image, and its annotations if given.
patch_image() {
  local metadata=""
  if [ $# -gt 1 ]; then metadata="\"metadata\":{\"annotations\":$2},"; fi
  kubectl patch serviceclaim "$CLAIM" -n "$CLAIM_NS" --type merge -p "{$metadata\"spec\":{\"image\":\"$1\"}}"
}

# load FLAG PATH REQUESTS PAUSE CONCURRENCY: send REQUESTS requests to PATH, then pause PAUSE
# seconds, over and over until the file FLAG is removed.
load() {
  while [ -f "$1" ]; do
    ab -q -k -n "$3" -c "$5" -H "Host: $HOST" "$INGRESS$2" >/dev/null 2>&1 || true
    sleep "$4"
  done
}
TRAFFIC=$(mktemp -t paved-demo-traffic)
ERRORS=$(mktemp -t paved-demo-errors)
# Argo CD applies the claims from git and self-heals them, which would undo this demo's kubectl
# patches. While the demo runs, automated sync is off for the root app, which would otherwise put
# the claims app's policy back, and for the claims app. Cleanup turns it back on, and Argo CD
# then returns the claim to what git says.
GITOPS_APPS=(paved platform-claims)
GITOPS_PAUSED=false
gitops_manages_claims() { kubectl get application platform-claims -n argocd >/dev/null 2>&1; }
# set_automated_sync POLICY_JSON: set spec.syncPolicy.automated on every GitOps app.
set_automated_sync() {
  local app
  for app in "${GITOPS_APPS[@]}"; do
    kubectl patch application "$app" -n argocd --type merge -p "{\"spec\":{\"syncPolicy\":{\"automated\":$1}}}" >/dev/null
  done
}
cleanup() {
  rm -f "$TRAFFIC" "$ERRORS"
  wait
  if [ "$GITOPS_PAUSED" = true ]; then
    set_automated_sync '{"prune":true,"selfHeal":true}'
  fi
}
trap cleanup EXIT

command -v ab >/dev/null || fail "ab (ApacheBench) is required to send traffic"
kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS" >/dev/null
if is_frozen; then
  fail "$CLAIM's deploys are still frozen from an earlier run; let its budget recover, then run again"
fi
if gitops_manages_claims; then
  say "Argo CD manages this claim from git: pausing its automated sync until the demo ends"
  set_automated_sync null
  GITOPS_PAUSED=true
fi
if [ "$(field '{.spec.image}')" != "$CURRENT" ] || [ -n "$(field '{.metadata.annotations.paved\.dev/break-glass}')" ]; then
  say "Setup: put $CLAIM back on $CURRENT"
  run patch_image "$CURRENT" '{"paved.dev/break-glass":null}'
fi

say "url-shortener: a public service with a 99.5% availability SLO over 28 days"
wait_for "the claim is Ready" 600 is_ready
run kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS"

say "Traffic arrives: a steady stream of successful requests, for the whole demo"
load "$TRAFFIC" / 100 1 2 &
wait_for "the controller has measured the error budget" 300 has_budget

say "A bad release: some requests now fail (the demo sends them to /boom)"
load "$ERRORS" /boom 10 1 2 &
errors_pid=$!
wait_for "the error budget is gone and deploys freeze" 900 is_frozen
wait_for "the last hour is spending the budget faster than the SLO allows (burn rate 1x or more)" 300 is_burning
run kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS"
condition DeploysFrozen message
echo

say "The next release, 0.2.0, is ready. Try to ship it"
show "kubectl patch serviceclaim $CLAIM -n $CLAIM_NS --type merge -p '{\"spec\":{\"image\":\"$RELEASE\"}}'"
if output=$(patch_image "$RELEASE" 2>&1); then
  printf '%s\n' "$output"
  fail "the image change was admitted while deploys were frozen"
fi
printf '%s\n' "$output"
[[ $output == *"deploys frozen: $CLAIM has "* ]] || fail "the image change was refused, but not by the deploy freeze"

say "An urgent hotfix can still go out, on the record: break-glass with a reason"
show "kubectl patch serviceclaim $CLAIM -n $CLAIM_NS --type merge -p '{\"metadata\":{\"annotations\":{\"paved.dev/break-glass\":\"$BREAK_GLASS_REASON\"}},\"spec\":{\"image\":\"$HOTFIX\"}}'"
events_before=$(break_glass_events)
patch_image "$HOTFIX" "{\"paved.dev/break-glass\":\"$BREAK_GLASS_REASON\"}"
wait_for "the BreakGlassUsed Event is recorded" 30 has_new_break_glass_event
run kubectl get events -n "$CLAIM_NS" --field-selector reason=BreakGlassUsed

say "The override covered that one change. Take the annotation off"
run kubectl annotate serviceclaim "$CLAIM" -n "$CLAIM_NS" paved.dev/break-glass-

say "The hotfix is out and the errors stop"
rm -f "$ERRORS"
wait "$errors_pid"
say "Good requests win the budget back. To keep the demo short, it now sends much more traffic"
say "Deploys stay frozen until 5% of the budget is back, so they can't flap on and off around zero"
load "$TRAFFIC" / 2000 0 8 &
wait_for "deploys unfreeze" 1500 is_unfrozen

say "Deploys are unfrozen. Retry the same release"
show "kubectl patch serviceclaim $CLAIM -n $CLAIM_NS --type merge -p '{\"spec\":{\"image\":\"$RELEASE\"}}'"
patch_image "$RELEASE"

say "Argo Rollouts ships 0.2.0 as a canary. The claim is Ready again when it finishes"
wait_for "0.2.0 is fully rolled out" 600 is_released
run kubectl get serviceclaim "$CLAIM" -n "$CLAIM_NS"
run kubectl get rollout "$CLAIM" -n "$ROLLOUT_NS" \
  -o custom-columns='ROLLOUT:.metadata.name,IMAGE:.spec.template.spec.containers[0].image,PHASE:.status.phase'

say "Done: nothing shipped while the budget was gone, except one change that is on the record"
