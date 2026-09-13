#!/usr/bin/env bash
#
# 02-drift.sh: delete a PrometheusRule that paved manages and watch the controller put it back.
#
# Expects the controller to be running and the url-shortener claim to exist. Recorded with:
#   asciinema rec --headless --window-size 100x30 --command ./demo/02-drift.sh demo/02-drift.cast
set -euo pipefail

NAMESPACE=svc-url-shortener

say() { printf '\n\033[1;36m# %s\033[0m\n' "$*"; }
run() {
  printf '\033[1;32m$\033[0m %s\n' "$*"
  "$@"
}
now_ms() { python3 -c 'import time; print(int(time.time() * 1000))'; }

say "A claim, and the PrometheusRule the controller generated for it"
run kubectl get serviceclaim url-shortener -n platform-claims
run kubectl get prometheusrule -n "$NAMESPACE"

say "Someone deletes the rules by hand"
run kubectl delete prometheusrule -n "$NAMESPACE" --all
deleted=$(now_ms)

say "Wait for the controller to notice and put them back"
run kubectl wait --for=create prometheusrule/url-shortener -n "$NAMESPACE" --timeout=60s
restored=$(now_ms)
run kubectl get prometheusrule -n "$NAMESPACE"
say "Restored $((restored - deleted)) ms after the delete returned"

say "The controller records what it fixed as an Event on the claim"
run kubectl get events -n platform-claims --field-selector reason=DriftCorrected
