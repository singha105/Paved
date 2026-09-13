# PROGRESS

| Day | Goal | Status |
|---|---|---|
| 1 | Cluster, scaffold, API, no-op reconcile | Done 2026-09-12 |
| 2 | Reconcile loop and 11 managed resources | Done 2026-09-12 |
| 3 | SLI recording rules, burn-rate alerts, dashboard | Done 2026-09-13 |

## BLOCKED

Nothing blocked.

---

## Pinned versions (resolved 2026-09-12)

Every version below was read from a command or file on this machine, not assumed. Use these
for the rest of the build; change one only on purpose, and record why here.

### Local tools

| Tool | Version | Source |
|---|---|---|
| Go | 1.27.0 darwin/arm64 | `go version` (scaffold's `go.mod` declares `go 1.26.0`) |
| kubebuilder | v4.16.0 | GitHub release binary, SHA-256 checked against `checksums.txt`, in `~/.local/bin` |
| k3d | v5.9.0 | `k3d version` |
| kubectl | v1.37.0 (client) | `kubectl version` |
| Helm | v4.2.4 | `helm version` |
| Docker Desktop | 29.7.2 (VM: 8 CPUs, 4.80 GiB) | `docker version`, `docker info` |
| controller-gen | v0.22.0 | `CONTROLLER_TOOLS_VERSION` in `Makefile`, installed to `bin/` |
| kustomize | v5.8.1 | `KUSTOMIZE_VERSION` in `Makefile` |
| setup-envtest | v0.25.0, serving Kubernetes 1.37.0 binaries | `make test` output |
| golangci-lint | v2.13.1 | `GOLANGCI_LINT_VERSION` in `Makefile`; built with the logcheck plugin into `bin/` on Day 2 |

### Go libraries (`go.mod`)

| Module | Version |
|---|---|
| sigs.k8s.io/controller-runtime | v0.25.0 |
| k8s.io/api, k8s.io/apimachinery, k8s.io/client-go | v0.37.0 |
| github.com/argoproj/argo-rollouts | v1.10.0 (Day 2; Rollout types, matching the installed controller) |
| github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring | v0.93.1 (Day 2; API-only module, matching the installed operator) |
| github.com/prometheus/client_golang | v1.24.0 (Day 2; demo app metrics, already a controller-runtime dependency) |

Adding argo-rollouts raised the `go` directive in `go.mod` from 1.26.0 to 1.26.1, the minimum it
declares. It pins `k8s.io/*` v0.34.5 in its own `go.mod`, but that doesn't apply to this module:
the build resolves v0.37.0 and pulls in no `k8s.io/kubernetes` packages.

### Cluster

| Component | Version | Note |
|---|---|---|
| k3s image | `rancher/k3s:v1.36.4-k3s1` | Newest stable k3s release on 2026-09-12. Server reports `v1.36.4+k3s1`; kubectl 1.37 is within one minor version. |
| CoreDNS / metrics-server / local-path-provisioner | 1.14.6 / v0.9.0 / v0.0.37 | Bundled with k3s |

### Helm charts

| Release | Namespace | Chart | Chart version | Images running |
|---|---|---|---|---|
| cert-manager | cert-manager | jetstack/cert-manager | v1.21.2 | cert-manager v1.21.2 |
| traefik | traefik | traefik/traefik | 41.5.0 | traefik v3.7.13 |
| argo-rollouts | argo-rollouts | argo/argo-rollouts | 2.43.1 | argo-rollouts v1.10.0 |
| kps | monitoring | prometheus-community/kube-prometheus-stack | 90.1.2 | prometheus-operator v0.93.1, Prometheus v3.14.0, Alertmanager v0.34.0, Grafana 13.2.1, kube-state-metrics v2.20.0, node-exporter v1.12.1 |

---

## Day 1: cluster, scaffold, API, no-op reconcile

### Acceptance

All seven lines were run on 2026-09-12 against the live cluster. The pod list is summarised;
everything else is copied from the terminal.

```text
$ kubectl get nodes
NAME                 STATUS   ROLES           AGE   VERSION
k3d-paved-server-0   Ready    control-plane   10m   v1.36.4+k3s1

$ kubectl get pods -A | grep -E 'prometheus|grafana|argo-rollouts|traefik|cert-manager'
12 pods, all Running: argo-rollouts (2), cert-manager (3), svclb-traefik, traefik,
alertmanager, grafana, prometheus-operator, node-exporter, prometheus

$ make manifests generate fmt vet && make install
customresourcedefinition.apiextensions.k8s.io/serviceclaims.platform.paved.dev unchanged

$ kubectl apply -f examples/url-shortener.yaml
serviceclaim.platform.paved.dev/url-shortener created

$ make run   # background
INFO  Reconciling ServiceClaim  {"ServiceClaim": {"name":"url-shortener","namespace":"platform-claims"}, ..., "tier": "public", "generation": 1}

$ kubectl get serviceclaims -n platform-claims
NAME            TIER     OWNER        BUDGET   FROZEN   READY   AGE
url-shortener   public   team-links                     False   2m12s

$ kubectl describe serviceclaim url-shortener -n platform-claims | grep -A5 Conditions
  Conditions:
    Last Transition Time:  2026-09-12T23:18:01Z
    Message:               Managed resources are not reconciled yet
    Observed Generation:   1
    Reason:                NotImplemented
    Status:                False
```

Extra checks beyond the acceptance list:

- **CRD validation, via server-side dry run:** objectives `99.5` and `90` accepted; `100`, `89.9`
  and `abc`, port `0`, and tier `premium` all rejected. The server filled in the defaults for
  `goodStatuses`, `latencyThreshold` (`250ms`) and `window` (`28d`).
- **`make test`:** envtest (Kubernetes 1.37.0) passes with 84.8% statement coverage of
  `internal/controller`. It asserts `Ready=False/NotImplemented` and that a missing claim
  reconciles without error.
- **Server-side apply:** `managedFields` on the claim lists `paved-controller Apply status`.
- **No reconcile loop:** creating the claim caused two reconciles (the create, then its own status
  change), then none. After a controller restart there was one reconcile, and `resourceVersion`
  stayed at `2347` with the same `lastTransitionTime`: an identical apply is not stored.

### Measurements

All taken on 2026-09-12 with the full stack installed and no ServiceClaims yet.

| What | Value | How measured |
|---|---|---|
| k3d server container memory | 2.717 GiB of 4.801 GiB | `docker stats --no-stream` |
| Sum of pod working sets | 1227 MiB (Grafana 429 MiB, Prometheus 321 MiB) | `kubectl top pods -A` |
| `hack/cluster-up.sh` re-run against an existing cluster | 31.9 s wall clock | `time ./hack/cluster-up.sh` |
| Free host disk | 15.6 GB, after clearing 15.42 GB of Docker build cache | `diskutil info /` |

### `hack/cluster-up.sh` is idempotent

- **Run 1** (fresh): exit 0; cluster created, all four charts installed, every `kubectl wait` passed.
- **Run 2**: exit 0; cluster reused, each release upgraded to revision 2. Grafana restarted
  once: the chart renders its generated admin-password Secret differently on upgrade (unquoted
  vs quoted), which changes the pod's `checksum/secret` annotation.
- **Run 3**: exit 0; `helm get manifest` is byte-identical between revisions 2 and 3 for all four
  releases, and no pod restarted. From the second run on, re-running changes nothing.

### Deviations from the spec

- **Traefik instead of ingress-nginx** (agreed; ADR-003). The pod acceptance grep matches `traefik`
  in place of `ingress-nginx`.
- **Objective validated by pattern + CEL, not Minimum/Maximum** (agreed; ADR-004). `100` is rejected.
- **`+listType=map` / `+listMapKey=type` kept on `status.conditions`** from the scaffold, so
  server-side apply merges conditions by type instead of replacing the whole list.
- **Day 1 split into seven commits** at the user's request; the day's commit message is on the last.

### Notes for later days

- The Argo Rollouts Go module has no API-only package: v1.10.0 pins `k8s.io/*` v0.34.5 (with a
  `k8s.io/kubernetes` requirement) while this project uses v0.37.0. Not yet tested together;
  check before writing `BuildRollout`.
- The scaffold keeps its envtest suite in `internal/controller/`; section 4 puts envtest suites in `test/`.
- The scaffold generated `.github/workflows/` (lint, test, test-e2e with Kind) and `.devcontainer/`.
  They are unreviewed and will run on the first push.
- `config/samples/platform_v1alpha1_serviceclaim.yaml` is still the scaffold's empty sample; the real
  examples live in `examples/`, with placeholder owner, image and SLO values.
- `shellcheck` is not installed; `hack/cluster-up.sh` was checked only with `bash -n`.
- Open spec questions from the section 3 review (still unanswered, none block Day 1): Prometheus
  scrape access through NetworkPolicy, ServiceMonitor for `batch` (no Service), the app metrics
  contract, freeze/unfreeze thresholds, webhook rollback and failure policy, `scale.min > scale.max`,
  PDB shape, canary style, claim namespace and name rules, Ingress host and TLS, dashboard as a
  ConfigMap, `platform-system` naming and egress scope.

---

## Day 2: reconcile loop and 11 managed resources

Agreed with the user before building: ownership through labels and a finalizer instead of owner
references (ADR-006), a demo app image for the examples (ADR-009), a NetworkPolicy that admits
Prometheus and leaves egress open (ADR-007), and the platform defaults listed in ADR-008.

### Acceptance

Run on 2026-09-12 against the live cluster, with the controller started by `make run`. Output is
copied from the terminal; long lists are shortened where marked.

```text
$ kubectl apply -f examples/url-shortener.yaml
serviceclaim.platform.paved.dev/url-shortener configured

$ kubectl get all,networkpolicy,pdb,servicemonitor,prometheusrule,cm -n svc-url-shortener
(pods and ReplicaSets created by the Rollout, plus:)
service/url-shortener                               ClusterIP   10.43.70.20   80/TCP
horizontalpodautoscaler.autoscaling/url-shortener   Rollout/url-shortener   MINPODS 2  MAXPODS 5
networkpolicy.networking.k8s.io/url-shortener
poddisruptionbudget.policy/url-shortener            MAX UNAVAILABLE 1
servicemonitor.monitoring.coreos.com/url-shortener
prometheusrule.monitoring.coreos.com/url-shortener
configmap/kube-root-ca.crt                          (created by Kubernetes in every namespace)
configmap/url-shortener-dashboard

# Rollout, Ingress, ServiceAccount and the Namespace aren't in that listing, so select by label:
$ kubectl get <all 11 kinds> -A -l paved.dev/claim=url-shortener
Namespace                 <none>              svc-url-shortener
ServiceAccount            svc-url-shortener   url-shortener
Rollout                   svc-url-shortener   url-shortener
NetworkPolicy             svc-url-shortener   url-shortener
PrometheusRule            svc-url-shortener   url-shortener
ServiceMonitor            svc-url-shortener   url-shortener
ConfigMap                 svc-url-shortener   url-shortener-dashboard
Service                   svc-url-shortener   url-shortener
HorizontalPodAutoscaler   svc-url-shortener   url-shortener
PodDisruptionBudget       svc-url-shortener   url-shortener
Ingress                   svc-url-shortener   url-shortener
COUNT=11

# claim status once the Rollout was Healthy with 2/2 pods ready
finalizers=["paved.dev/finalizer"] managedResources=11 observedGeneration=2
ResourcesSynced=True (Applied): Applied 11 of 11 managed resources
Ready=False (NotImplemented): SLO evaluation is not implemented yet

$ kubectl get rollout -n svc-url-shortener -o jsonpath='{.items[0].metadata.resourceVersion}'
5750
$ kubectl annotate serviceclaim url-shortener -n platform-claims kick=1 --overwrite
serviceclaim.platform.paved.dev/url-shortener annotated
$ sleep 5
$ kubectl get rollout -n svc-url-shortener -o jsonpath='{.items[0].metadata.resourceVersion}'
5750
reconciles logged during the check: 1
RESULT: Rollout resourceVersion identical (5750)

$ kubectl delete serviceclaim url-shortener -n platform-claims --wait=false
serviceclaim.platform.paved.dev "url-shortener" deleted from platform-claims namespace
$ kubectl get ns svc-url-shortener
svc-url-shortener   Terminating   2m27s
$ kubectl wait --for=delete serviceclaim/url-shortener -n platform-claims --timeout=180s
serviceclaim.platform.paved.dev/url-shortener condition met
$ kubectl get ns svc-url-shortener
Error from server (NotFound): namespaces "svc-url-shortener" not found

$ go test ./internal/builders/... -v
21 tests and 26 subtests, all PASS
ok  	github.com/singha105/paved/internal/builders	0.631s
```

Extra checks beyond the acceptance list:

- **Nothing rewritten on the extra reconcile.** For all 11 objects, both `resourceVersion` and the
  time `paved-controller` last wrote them were identical before and after the annotation.
- **Drift repair:** `kubectl delete service url-shortener -n svc-url-shortener` was followed by
  the controller recreating it (`kubectl wait --for=create` condition met).
- **Ingress:** `GET http://url-shortener.localhost/` returned 200 through Traefik on port 80.
- **Prometheus scrapes through the NetworkPolicy:** `up{namespace="svc-url-shortener"}` returned 2
  targets, both `up=1`.
- **Grafana:** the dashboard sidecar logged `Writing /tmp/dashboards/url-shortener.json`.
- **Finalizer order:** the controller logged `Deleted namespace` at 20:09:00 and
  `Removed finalizer after namespace was deleted` at 20:09:25, once the pods had terminated.
- **Controller log:** zero errors for the whole run.
- **envtest (`make test`):** create (finalizer, 10 internal-tier objects, status), a second reconcile
  with every `resourceVersion` unchanged, repair of a deleted Service, and deletion that keeps the
  finalizer until the namespace is gone. All pass.
- **Demo image smoke test:** `docker run --read-only --user 65532:65532 --cap-drop ALL`; `/healthz`,
  `/readyz` and `/` return 200, and `/metrics` counts only application requests.
- **`make lint`:** 0 issues.

### Measurements

| What | Value | How measured |
|---|---|---|
| k3d node memory, stack plus `url-shortener` (2 pods) | 2.966 GiB of 4.801 GiB | `docker stats --no-stream` |
| Demo app image | 24.7 MB (7.09 MB as stored in the k3d node) | `docker image ls`, `crictl images` |
| Builder statement coverage | 100.0% | `make test` |
| Controller statement coverage (envtest) | 82.0% | `make test` |

### Deviations from the spec

- **No owner references and no `Owns()`** (agreed; ADR-006). Every managed object carries the claim
  labels, one watch per managed kind maps events back to the claim, and the finalizer deletes
  `svc-<name>`. That adds one label beyond the spec's four: `paved.dev/claim-namespace`.
- **The NetworkPolicy admits Prometheus and restricts ingress only** (agreed; ADR-007).
- **The examples run `paved-demo-app:0.1.0`** (agreed; ADR-009).
- **Platform defaults that section 3 left open** (agreed; ADR-008): 50m/64Mi requests, 250m/128Mi
  limits, HPA on 70% CPU, PDB `maxUnavailable: 1`, Ingress host `<claim>.localhost` with no TLS,
  dashboard as a ConfigMap, and a PrometheusRule with one empty group.
- **`sleep 5` ran inside a background script**, because this shell tool blocks a foreground `sleep`.
  The check around it is stricter than the spec's: it also compares every managed object's
  `resourceVersion` and `paved-controller` write time.
- **`cmd/main.go` no longer calls `utilruntime.Must`**. Scheme registration returns an error, which is
  logged before exiting, per rule 5.
- **The scaffold's e2e suite now installs `test/crds`** (Rollout, ServiceMonitor, PrometheusRule) before
  deploying. Without them the manager can't start its watches in a fresh Kind cluster. It passed
  in CI (E2E Tests) on the Day 2 push, commit `c52210a`.

### Notes for later days

- When the new controller first started, it reconciled the stored Day 1 claim, whose image didn't
  exist, before the updated example was applied. That left a failed first ReplicaSet, scaled to 0
  once the demo image rolled out. The controller acts on whatever spec is stored, as it should.
- Nothing validates yet that `svc-<claim>` fits the 63-character namespace limit (so claim names of
  at most 59 characters), or that `spec.owner` is a valid label value. Either mistake would surface
  as `ResourcesSynced=False (ApplyFailed)`.
- Server-side apply with force adopts a pre-existing namespace called `svc-<name>`; the finalizer
  only deletes it if its labels name this claim (ADR-010).
- A batch claim's ServiceMonitor matches nothing, because batch has no Service.
- Still open: freeze and unfreeze thresholds; the webhook's rollback handling and failure policy;
  the app metrics contract (the demo app's `http_requests_total` and
  `http_request_duration_seconds` are a proposal only); rules for claim namespaces and names; Ingress
  TLS; and the `platform-system` namespace name.

---

## Day 3: SLI recording rules, burn-rate alerts, dashboard

Agreed with the user before building (2026-09-13): implement the `http-latency` SLI as well as
`http-availability`; move the demo app to `examples/testsvc`, add `/boom`, and serve it from a
k3d registry (ADR-012); give every claim a runbook ConfigMap, with alerts linking to
`docs/runbooks/slo-burn-rate.md` (ADR-011); and the rule and dashboard defaults in ADR-013.

### Acceptance

Run on 2026-09-13 against the recreated cluster, with the controller started by `make run` and
both example claims applied. Output is copied from the terminal; shortened where marked.

```text
$ kubectl apply -f examples/url-shortener.yaml -f examples/webhook-delivery.yaml
serviceclaim.platform.paved.dev/url-shortener created
serviceclaim.platform.paved.dev/webhook-delivery created

$ kubectl get prometheusrule -n svc-url-shortener -o yaml | head -60
(shortened; the operator's admission webhook added prometheus-operator-validated: "true")
  spec:
    groups:
    - name: url-shortener.sli
      rules:
      - expr: |-
          (sum(rate(http_requests_total{namespace="svc-url-shortener",code!~"200|201|204|301|302|304|400|404"}[5m])) or vector(0))
          /
          sum(rate(http_requests_total{namespace="svc-url-shortener"}[5m]))
        labels:
          owner: team-links
          service: url-shortener
          tier: public
        record: sli:http_availability:error_ratio_rate5m
      (the 30m, 1h and 2h rules follow in the same shape)

$ kubectl port-forward -n monitoring svc/kps-kube-prometheus-stack-prometheus 9090:9090 &
$ curl -s localhost:9090/api/v1/rules | jq '.data.groups[].name'
(35 kube-prometheus-stack groups, then:)
"url-shortener.sli"
"url-shortener.slo-alerts"
"webhook-delivery.sli"
"webhook-delivery.slo-alerts"

# after their first evaluation
url-shortener.sli: 7 rules, health=ok, errors=0
url-shortener.slo-alerts: 4 rules, health=ok, errors=0
webhook-delivery.sli: 7 rules, health=ok, errors=0
webhook-delivery.slo-alerts: 4 rules, health=ok, errors=0

$ curl -s 'localhost:9090/api/v1/query?query=sli:http_availability:error_ratio_rate5m' | jq
# before any traffic
"result": []
# 65 s after healthy traffic started
"result": [
  {
    "metric": {
      "__name__": "sli:http_availability:error_ratio_rate5m",
      "owner": "team-links",
      "service": "url-shortener",
      "tier": "public"
    },
    "value": [1789277146.331, "0"]
  }
]

# then 3 requests to /boom for every request to /, sampled every 15 s
elapsed  ratio_5m            ratio_1h            page alerts firing
0s       0                   0                   0
45s      0                   0                   0
60s      0.5884512893608808  0.5884512893608809  0
75s      0.5884512893608808  0.5884512893608809  2

$ curl -s 'localhost:9090/api/v1/alerts' | jq '.data.alerts[].labels.alertname'
"Watchdog"
"KubeControllerManagerDown"
"SLOErrorBudgetBurn"
"SLOErrorBudgetBurn"
"SLOErrorBudgetBurn"
"SLOErrorBudgetBurn"
"KubeSchedulerDown"
"NodeClockNotSynchronising"
"KubeProxyDown"

firing  service=url-shortener severity=page windows=1h/5m
firing  service=url-shortener severity=page windows=6h/30m
firing  service=url-shortener severity=ticket windows=1d/2h
firing  service=url-shortener severity=ticket windows=3d/6h

# annotations of the 1h/5m page alert
"description": "The error ratio is above 0.072 over both the last 1h and the last 5m. At 14.4x, the 28d error budget of 0.005 lasts about 47 hours.",
"owner": "team-links",
"runbook": "svc-url-shortener/url-shortener-runbook",
"runbook_url": "https://github.com/singha105/paved/blob/main/docs/runbooks/slo-burn-rate.md",
"summary": "url-shortener is spending its error budget 14.4x faster than it can sustain"
```

The other alert names are kube-prometheus-stack's own default alerts; paved doesn't generate them,
and they weren't investigated.

Extra checks beyond the acceptance list:

- **The latency SLI returns data.** `webhook-delivery` recorded `sli:http_latency:error_ratio_rate5m`
  = `0` and `...rate1h` = `0` (every test request finished within 250ms), its `le="0.25"` bucket
  series exist, and none of its alerts fired.
- **Metric names match.** `up` was `1` for all four pods, and `http_requests_total` arrived with the
  `code` label.
- **Bucket label format.** Prometheus's `le` values included `"0.25"`, `"1.0"` and `"10.0"`, confirming
  that whole seconds need the `.0` the latency rules add.
- **Still idempotent.** After `kubectl annotate ... kick=day3`, none of `url-shortener`'s 12 objects had
  a new `paved-controller` write time; all still read `05:23:08Z`.
- **Grafana:** the dashboard sidecar wrote `url-shortener.json` and `webhook-delivery.json`, and Grafana
  logged 0 dashboard errors over 30 minutes.
- **Runbook:** `url-shortener-runbook` renders the owner, the SLO and a table with thresholds 0.072,
  0.03, 0.015 and 0.005 and budget lifetimes of 47 hours, 4.7, 9.3 and 28 days.
- **Registry:** `localhost:5001/v2/_catalog` lists `testsvc` with tag `0.1.0`. The pushed image served
  200 on `/healthz`, `/readyz` and `/`, and 500 on `/boom`, running read-only as UID 65532 with no
  capabilities; `/metrics` counted exactly those requests.
- **Controller log:** 43 reconciles and 0 error lines.
- **`make test`:** passes, with the new `InvalidSpec` envtest case. **`make lint`:** 0 issues.

### Measurements

| What | Value | How measured |
|---|---|---|
| Healthy traffic starting to a recorded 5m error ratio | 65 s | acceptance script, polling every 5 s |
| `/boom` traffic starting to both page alerts firing | at most 75 s (not firing at 60 s, firing at 75 s) | acceptance script, sampling every 15 s |
| Generated rules loaded by Prometheus | 22 (11 per claim, 2 claims), all `health=ok` | `/api/v1/rules` |
| k3d node memory, stack plus 2 claims (4 pods) | 2.944 GiB of 4.801 GiB | `docker stats --no-stream` |
| `hack/cluster-up.sh` re-run with the registry | 29.6 s | `time ./hack/cluster-up.sh` |
| Statement coverage | builders 90.2%, controller 82.1%, slo 92.1% | `make test` |
| Registry image | `docker.io/library/registry:2` | `docker inspect k3d-paved-registry` |

### Deviations from the spec

- **Latency SLIs are implemented too** (agreed). The service contract therefore names two metrics:
  `http_requests_total{code}` for every claim, plus `http_request_duration_seconds` for latency
  claims (README, "Service contract").
- **`examples/testsvc` replaces Day 2's `demo/app`, and a k3d registry replaces `k3d image import`**
  (agreed; ADR-012). k3d can only attach a registry when it creates a cluster, so the cluster was
  deleted and recreated once by `hack/cluster-up.sh`. The registry uses port 5001, because macOS
  AirPlay Receiver holds 5000.
- **Every claim gets a runbook ConfigMap**, which makes the tier sets 12, 11 and 8 objects instead of
  section 3's 11, 10 and 7 (agreed; ADR-011). `runbook_url` links to the general runbook on GitHub,
  and a `runbook` annotation names the claim's ConfigMap.
- **The dashboard ConfigMap stays in `svc-<claim>`** rather than the monitoring namespace: the Grafana
  sidecar watches every namespace, and keeping it there means the finalizer cleans it up.
- **Each claim's rules are in two groups**, `<claim>.sli` and `<claim>.slo-alerts`.
- **The acceptance port-forward targets `svc/kps-kube-prometheus-stack-prometheus`**, because the Helm
  release is named `kps`.
- **README.md was rewritten** from the kubebuilder boilerplate to hold the service contract the spec
  asks for, plus what paved is and how to run it.

### Notes for later days

- With no traffic, a service records no SLI value and its alerts can't fire (ADR-013).
- A batch claim has no Service, so its ServiceMonitor scrapes nothing and it gets no SLO data.
- The CRD doesn't validate `latencyThreshold`; a bad value surfaces as
  `ResourcesSynced=False (InvalidSpec)`.
- The error budget panel can only see Prometheus's 10 days of retention, so a 28d or 30d window is
  undercounted. Budget and freeze decisions will need either more retention or a recording rule.
- Grafana loading the dashboards is evidenced by the sidecar writing the files and Grafana logging no
  dashboard errors; the Grafana API was not queried, because that needs the admin login.
- Still open for the freeze work: unfreeze threshold (hysteresis), whether rollbacks are allowed
  during a freeze, and the webhook's failure policy.
