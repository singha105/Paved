# Postmortem: the first failures after every pod start never reached the error budget

**Found:** 2026-09-13 (Day 4) · **Fixed:** 2026-09-13 (Day 5, commit `6dcc2fa`) · **Guardrail tests:**
2026-09-14 (Day 7, commit `43c59a9`)

The worst bug this week was not the loudest one. A full disk and a crashed Docker engine cost more
hours, but they announced themselves. This one was silent, and it sat under every decision paved
makes: the error budget in status, the burn-rate alerts, the deploy freeze and, later, the canary
analysis all read the same error ratio. That ratio was too low.

## Summary

paved measures a service's availability from Prometheus's `rate()` over its `http_requests_total`
counter. The test service created a counter series the first time it answered with a given status
code. `rate()` can't see the increase that creates a series, so the failures that created each pod's
`code="500"` series were never counted. After every pod start, the first burst of errors on each pod
was missing from the budget.

## Impact

- On the day it was found, the first 40 failed requests to a fresh claim changed its budget by
  nothing: it stayed at `100.0%`.
- Every error budget was overstated by that first burst per pod, per start. A service that restarts
  often, or scales out under load (new pods, new series), would look more reliable than it was.
- The consequences reach everything built on the ratio: burn-rate alerts would page late, a deploy
  freeze would come late or not at all, and the canary analysis would see a failing pod as healthier.
- Nothing had shipped to anyone. It was caught on a local cluster, during the Day 4 acceptance run.

## Timeline (2026-09-13)

- **Day 4 acceptance.** To show the budget moving, a fresh claim, `budget-demo`, got steady
  successful traffic. Its budget read `100.0%`.
- **Symptom.** 40 requests to `/boom`, which always fails, then 4 minutes, more than enough for a
  30-second scrape and a 1-minute reconcile. The budget still read `100.0%` and `SLOHealthy` stayed
  `WithinBudget`.
- **Not the controller.** 400 more failures took the budget to `0.0%` (`BudgetExhausted`, 1.63x
  spent). The pipeline worked; only the first failures were lost.
- **Found it.** A range query on `http_requests_total{code="500"}` showed each pod's series first
  scraped already at 20. No sample of it at 0 existed, so the first 20 failures on each of the two
  pods had no earlier value for `rate()` to subtract from.
- **Decision.** Fixing it meant changing the service contract every service follows, not just the
  test service, so it was raised with the user. It was recorded under "Notes for later days" in
  PROGRESS.md rather than patched quietly.
- **Day 5.** The user agreed, and it was fixed as the day's first commit.

## Root cause

Two facts that are each reasonable on their own:

1. **`promhttp` creates label series lazily.** `InstrumentHandlerCounter` creates
   `http_requests_total{code="500",method="get"}` when a handler first returns 500. Until then the
   series doesn't exist at all, rather than existing at 0.
2. **`rate()` and `increase()` work from samples.** They extrapolate from the first sample in the
   range, and that first sample is the counter's value when Prometheus first saw the series. A series
   born at 20 contributes nothing until it rises past 20.

Put together, the increase that created the series happened before its first sample, and was lost.
`code="200"` had the same gap, but successful traffic was steady, so it was lost in the noise. The
failures that matter most, a new release's first errors, hit it worst.

The controller's tests didn't catch it: they give the reconciler a fake Prometheus that returns
chosen ratios. The rules and the budget maths were right. The input was wrong.

## Fix

- **Commit `6dcc2fa`.** testsvc creates its `code="200"` and `code="500"` series, for both the counter
  and the latency histogram, at zero before it serves, with `GetMetricWithLabelValues`. That was
  released as testsvc 0.1.1, and the example claims moved to it.
- **Verified in the cluster.** Before any traffic, Prometheus already had
  `http_requests_total{code="200"} 0` and `{code="500"} 0` for both url-shortener pods.
- **The service contract changed.** The README asks every service to create the series for every
  status code it returns at zero when it starts, and says why (ADR-020). The fix belongs in the
  service, because the rules can't recover an increase that never reached Prometheus.

## Guardrails

- **Tests on both services** fail if a series is missing before the first request:
  - `examples/testsvc`: `TestRequestSeriesExistBeforeAnyRequest`.
  - `services/shortlink`: `TestEveryStatusSeriesExistsBeforeAnyRequest`, which covers every code it
    can answer, for both the counter and the histogram.
- **CI runs them** on every push; shortlink is a separate module, so it has its own step in `ci.yaml`.
- **The contract is written down** where a team onboarding a service reads it, in the README's service
  contract section and ADR-020, not only in code.
- **The second service followed it.** shortlink was written from the contract alone, and its first
  scrape already had every series at 0.

## What would have caught it sooner

- **An end-to-end check that budgets respond to the first failures after a pod start.** Unit tests
  with a fake Prometheus prove the maths, not the measurement. The demo scripts exercise the real
  pipeline, and this bug surfaced in one; a test doing the same on purpose would have found it on
  Day 3.
- **Distrusting a number that didn't move.** Waiting 4 minutes was the right instinct. Querying the
  raw series, instead of assuming the controller was stale, was what found it.
