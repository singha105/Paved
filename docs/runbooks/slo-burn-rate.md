# Runbook: SLOErrorBudgetBurn

`SLOErrorBudgetBurn` fires when a service claimed through Paved is using up its error budget
faster than its SLO allows. Every claim gets four of these alerts, generated from its
`ServiceClaim`; they differ only in how fast the budget is burning and over which windows.

Each claim also has its own runbook, with the service's owner and objective filled in, in the
ConfigMap named by the alert's `runbook` annotation.

## Reading the alert

| Label | Meaning |
|---|---|
| `service` | The claim's name. Its workload runs in the namespace `svc-<service>`. |
| `owner` | The team that owns the service (also in the `owner` annotation). |
| `tier` | `public`, `internal` or `batch`. |
| `severity` | `page`: act now. `ticket`: act within working hours. |
| `long_window`, `short_window` | The two windows whose error ratios are both above the threshold. |

The `description` annotation states the threshold and how long the budget lasts if the
current rate continues.

## What each alert means

A **burn rate** of 1 spends exactly the whole error budget over the SLO window. A burn rate of
14.4 spends it 14.4 times faster. For a 30-day window:

| Severity | Burn rate | Long window | Short window | A full budget lasts |
|---|---|---|---|---|
| page | 14.4x | 1h | 5m | about 2 days |
| page | 6x | 6h | 30m | 5 days |
| ticket | 3x | 1d | 2h | 10 days |
| ticket | 1x | 3d | 6h | 30 days |

The alert needs **both** windows above the threshold. The long window shows the burn is large
enough to matter. The short window shows it is still happening, so the alert resolves shortly
after the problem stops.

## First three steps

1. **Read the service's own runbook** for its owner, objective and SLI:

   ```bash
   kubectl get configmap <service>-runbook -n svc-<service> -o jsonpath='{.data.runbook\.md}'
   ```

2. **Check whether a deploy caused it.** Compare the claim's image with what the Rollout is
   running, and look for a rollout in progress:

   ```bash
   kubectl get serviceclaim <service> -n platform-claims -o jsonpath='{.spec.image}{"\n"}'
   kubectl get rollout <service> -n svc-<service>
   ```

3. **Find when the burn started** on the service's Grafana dashboard (tagged `paved`): the error
   ratio panel against the objective, and request rate by status code. If the rise lines up with
   an image change, set `spec.image` in the ServiceClaim back to the previous version. The
   controller owns the Rollout, so edit the claim, not the Rollout.
