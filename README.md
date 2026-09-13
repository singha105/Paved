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
  image: k3d-paved-registry:5001/testsvc:0.1.0
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

## Run it locally

Requires Docker, k3d, kubectl, Helm and Go. Versions used are pinned in [PROGRESS.md](PROGRESS.md).

```bash
./hack/cluster-up.sh        # k3d cluster, registry on localhost:5001, platform stack
./hack/testsvc-image.sh     # build and push the test service
make install                # install the ServiceClaim CRD
make run                    # run the controller against the cluster
```

In another terminal:

```bash
kubectl apply -f examples/platform-claims-namespace.yaml
kubectl apply -f examples/url-shortener.yaml
kubectl get serviceclaims -n platform-claims
curl http://url-shortener.localhost/boom    # returns 500; counts against the SLO
```

## Repository layout

```
api/v1alpha1/        ServiceClaim types
cmd/main.go          controller manager
internal/builders/   one pure function per managed object
internal/slo/        error budgets, recording rules, burn-rate alerts
internal/controller/ reconciler
examples/            example claims and the test service
docs/runbooks/       runbooks the alerts link to
hack/                cluster bootstrap and image scripts
test/                e2e suite and third-party CRDs for tests
```

## License

Apache License 2.0.
