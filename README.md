# paved

An internal developer platform built as a Kubernetes operator. A developer writes one short
`ServiceClaim`; the controller turns it into a production-shaped service and keeps it that way.

```yaml
apiVersion: platform.paved.dev/v1alpha1
kind: ServiceClaim
metadata:
  name: url-shortener
  namespace: platform-claims
spec:
  owner: team-links
  image: k3d-paved-registry:5001/testsvc:0.1.1
  port: 8080
  tier: public
  sli:
    type: http-availability
  slo:
    objective: "99.5"
    window: 28d
  scale:
    min: 2
    max: 5
```

The project is being built in stages. [PROGRESS.md](PROGRESS.md) records what works so far and
the output that proves it; [DECISIONS.md](DECISIONS.md) explains the non-obvious choices.

## What a claim becomes

Each claim is reconciled into its own namespace, `svc-<name>`. The controller server-side
applies every object on each reconcile, puts back anything deleted or edited, and deletes the
namespace when the claim is deleted.

| Object | public | internal | batch |
|---|---|---|---|
| Namespace, ServiceAccount, Argo Rollout (canary), NetworkPolicy | yes | yes | yes |
| PrometheusRule: SLI recording rules and burn-rate alerts | yes | yes | yes |
| ServiceMonitor, Grafana dashboard, runbook | yes | yes | yes |
| Service, HorizontalPodAutoscaler, PodDisruptionBudget | yes | yes | no |
| Ingress (`<name>.localhost` through Traefik) | yes | no | no |

Developers cannot set resource limits, security context, rollout strategy, probes or canary
steps. Those belong to the platform ([ADR-001](DECISIONS.md#adr-001-developers-cannot-set-limits-security-context-rollout-strategy-probes-or-canary-steps)).

## Service contract

The platform measures every service the same way, so every service must expose the same
metrics. **This is the one thing a service must do to be onboarded.**

Serve Prometheus metrics at `/metrics` on the claim's `port`, including:

- **`http_requests_total`**: a counter of HTTP requests, with the status code in a label named
  `code` (for example `code="500"`). Every claim needs it. For `http-availability`, a request
  is bad when its code is not in the claim's `sli.goodStatuses`.
- **`http_request_duration_seconds`**: a histogram of request latency in seconds. Claims using
  `http-latency` need it, with a bucket at exactly their `sli.latencyThreshold` (the default
  250ms needs a bucket at 0.25). A request is bad when it takes longer than the threshold.

Create the series for every status code the service returns, successes and failures, at zero when
it starts. Prometheus's `rate()` can't see the increase that creates a series, so without this the
first failures after each start would not count against the SLO
([ADR-020](DECISIONS.md#adr-020-services-create-their-request-series-at-zero-when-they-start)).

Don't count health checks or scrapes in these metrics; they would make the service look
healthier than it is. The platform also expects `/healthz` and `/readyz` on the same port, and
runs the container as UID 65532 with a read-only root filesystem.

Go services get all of this from `prometheus/client_golang`'s `promhttp.InstrumentHandlerCounter`
and `promhttp.InstrumentHandlerDuration`. [`examples/testsvc`](examples/testsvc/main.go) is a
complete example.

## How SLOs are enforced

From the claim's SLI and objective, the controller generates:

- **Recording rules** for the SLI error ratio over 5m, 30m, 1h, 2h, 6h, 1d and 3d, named
  `sli:http_availability:error_ratio_rate5m` (or `sli:http_latency:...`) and labelled
  `service`, `owner` and `tier`.
- **Four burn-rate alerts** (`SLOErrorBudgetBurn`) from the Google SRE Workbook. Each fires only
  when both of its windows are burning the error budget faster than its threshold:

  | Severity | Burn rate | Long window | Short window |
  |---|---|---|---|
  | page | 14.4 | 1h | 5m |
  | page | 6 | 6h | 30m |
  | ticket | 3 | 1d | 2h |
  | ticket | 1 | 3d | 6h |

- **A Grafana dashboard** with request rate, error ratio against the objective, latency
  percentiles and error budget remaining, plus a **runbook** ConfigMap. The alerts link to
  [docs/runbooks/slo-burn-rate.md](docs/runbooks/slo-burn-rate.md).

## What the controller reports

Every minute, the controller reads each claim's error ratio from Prometheus and records it in
the claim's status:

- **`errorBudgetRemaining`** (the BUDGET column): the share of the error budget left over the
  SLO window, `clamp(1 - errorRatio / (1 - objective), 0, 1)`.
- **`burnRate1h`**: how fast the last hour spent it. At 1, the whole budget would last exactly
  the SLO window.
- **`SLOHealthy`**: `True` while budget remains; `False` when it is used up
  (`BudgetExhausted`) or the last hour burned at 14.4x or faster (`FastBurn`); `Unknown` when
  there is no traffic or Prometheus can't be reached.

```text
$ kubectl get serviceclaims -A
NAMESPACE         NAME               TIER     OWNER               BUDGET   FROZEN   READY   AGE
platform-claims   url-shortener      public   team-links          39.8%    False    True    16h
platform-claims   webhook-delivery   public   team-integrations   100.0%   False    True    16h
```

Here `url-shortener` has spent 60% of its budget on requests the freeze demo sent to `/boom`, which
always fails.

If Prometheus is unreachable, the controller fails open: `SLOHealthy` becomes `Unknown`, the
budget is left blank rather than guessed, and resources are still reconciled
([ADR-014](DECISIONS.md#adr-014-when-prometheus-cant-answer-the-controller-fails-open)).

The controller also heals drift. Delete or edit an object it manages and the next reconcile puts
it back, recording a `DriftCorrected` Event on the claim
([ADR-015](DECISIONS.md#adr-015-drift-is-detected-from-the-controllers-own-field-ownership)).
[`demo/02-drift.cast`](demo/02-drift.cast) records a deleted PrometheusRule coming back. In five
measured runs on a local k3d cluster, the rule was back 65 to 99 ms after `kubectl delete`
returned (median 78 ms).

## How deploys freeze

The error budget decides whether a service may ship. Every minute the controller sets the claim's
`DeploysFrozen` condition, the FROZEN column:

- **`True` (`BudgetExhausted`)** as soon as no error budget is left.
- **Still `True` (`BudgetRecovering`)** until 5% of the budget is back, so a service hovering at
  zero can't flip between frozen and unfrozen
  ([ADR-017](DECISIONS.md#adr-017-deploys-freeze-when-the-budget-is-gone-and-unfreeze-only-at-5)).
- **`False`** otherwise. When the budget can't be measured, the last decision stands.

A validating admission webhook enforces it. While `DeploysFrozen` is `True`, an update that changes
`spec.image` is rejected. Every other change, such as scaling, is admitted
([ADR-018](DECISIONS.md#adr-018-the-webhook-rejects-only-image-changes-and-break-glass-must-be-set-in-the-same-update)).
From [`demo/03-freeze.cast`](demo/03-freeze.cast), with the command wrapped:

```text
$ kubectl patch serviceclaim url-shortener -n platform-claims --type merge \
    -p '{"spec":{"image":"k3d-paved-registry:5001/testsvc:0.2.0"}}'
Error from server (Forbidden): admission webhook "vserviceclaim-v1alpha1.paved.dev" denied the request: deploys frozen: url-shortener has 0% error budget remaining in a 28d window (burn rate 1.0x). Override with annotation paved.dev/break-glass="<reason>" — this is audited.
```

In an emergency, set a reason in the same update that changes the image. The change goes through
with a warning, and a `BreakGlassUsed` Event records who made it and why. From the recording, with
the Events of earlier demo runs left out:

```text
$ kubectl patch serviceclaim url-shortener -n platform-claims --type merge \
    -p '{"metadata":{"annotations":{"paved.dev/break-glass":"INC-1234: hotfix for the 500s"}},"spec":{"image":"k3d-paved-registry:5001/testsvc:0.1.2"}}'
Warning: deploys are frozen: this image change was let through by paved.dev/break-glass and recorded in a BreakGlassUsed Event
serviceclaim.platform.paved.dev/url-shortener patched

$ kubectl get events -n platform-claims --field-selector reason=BreakGlassUsed
LAST SEEN   TYPE      REASON           OBJECT                       MESSAGE
0s          Warning   BreakGlassUsed   serviceclaim/url-shortener   system:admin changed the image from k3d-paved-registry:5001/testsvc:0.1.1 to k3d-paved-registry:5001/testsvc:0.1.2 during a deploy freeze: INC-1234: hotfix for the 500s
```

A reason left on the claim won't carry a later image change through, so every override needs its
own. The webhook's failure policy is `Fail`: if it can't be reached, ServiceClaim writes are
rejected rather than let through.

[`demo/03-freeze.cast`](demo/03-freeze.cast) records the whole arc. Errors spend url-shortener's
budget and deploys freeze. A release is rejected, and a hotfix ships with break-glass. Then the
errors stop, the budget recovers, and the same release ships. Replay it with
`asciinema play demo/03-freeze.cast`, or run it yourself with [`demo/freeze-demo.sh`](demo/freeze-demo.sh).

`Ready` is `True` when every managed resource is applied and Argo Rollouts reports the Rollout
healthy at its current generation, so it is `False` during a canary
([ADR-019](DECISIONS.md#adr-019-ready-means-every-resource-is-applied-and-the-rollout-is-healthy)).

## Run it locally

Requires Docker, k3d, kubectl, Helm and Go, plus ApacheBench (`ab`) for the freeze demo. Versions
used are pinned in [PROGRESS.md](PROGRESS.md).

```bash
./hack/cluster-up.sh        # k3d cluster, registry on localhost:5001, platform stack
./hack/testsvc-image.sh     # build and push the test service
make docker-build deploy    # build the controller, load it into k3d, deploy it with its webhook
```

The controller runs in the cluster: the API server can only reach its admission webhook there, with
the certificate cert-manager issues for it
([ADR-016](DECISIONS.md#adr-016-the-controller-runs-in-the-cluster-and-make-docker-build-loads-it-into-k3d)).
After rebuilding, restart it to pick up the new image:

```bash
kubectl rollout restart deployment/paved-controller-manager -n paved-system
```

Then create a claim:

```bash
kubectl apply -f examples/platform-claims-namespace.yaml
kubectl apply -f examples/url-shortener.yaml
kubectl get serviceclaims -n platform-claims
curl http://url-shortener.localhost/boom    # returns 500; counts against the SLO
```

The freeze demo ships two more tags of the test service. Push them, then run it:

```bash
TAG=0.1.2 ./hack/testsvc-image.sh
TAG=0.2.0 ./hack/testsvc-image.sh
./demo/freeze-demo.sh
```

`make run` starts the controller on your machine instead, without the webhook, so nothing enforces
deploy freezes. It also can't reach Prometheus's in-cluster address; forward the port and point
the controller at it:

```bash
kubectl port-forward -n monitoring svc/kps-kube-prometheus-stack-prometheus 9090:9090
```

```bash
ENABLE_WEBHOOKS=false go run ./cmd/main.go --prometheus-url=http://localhost:9090
```

## Repository layout

```
api/v1alpha1/        ServiceClaim types
cmd/main.go          controller manager
internal/builders/   one pure function per managed object
internal/slo/        error budgets, recording rules, burn-rate alerts
internal/controller/ reconciler: resources, drift, error budget, deploy freeze, Ready
internal/webhook/    admission webhook: rejects image changes during a freeze, audits break-glass
examples/            example claims and the test service
demo/                demo scripts and their asciinema recordings
docs/runbooks/       runbooks the alerts link to
hack/                cluster bootstrap and image scripts
test/                e2e suite and third-party CRDs for tests
```

## License

Apache License 2.0.
