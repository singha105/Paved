# PROGRESS

| Day | Goal | Status |
|---|---|---|
| 1 | Cluster, scaffold, API, no-op reconcile | Done 2026-09-12 |
| 2 | Reconcile loop and 11 managed resources | Done 2026-09-12 |
| 3 | SLI recording rules, burn-rate alerts, dashboard | Done 2026-09-13 |
| 4 | Error budget in status, drift correction | Done 2026-09-13 |
| 5 | Admission webhook: freeze deploys on budget exhaustion | Done 2026-09-13 |
| 6 | CI/CD, GitOps, image scanning, canary auto-rollback | Done 2026-09-14 |

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
| Trivy (local scans) | 0.74.0 | `trivy version` (Day 6) |
| kubectl-argo-rollouts | v1.10.0+d90700a | GitHub release binary, SHA-256 checked against `argo-rollouts-checksums.txt`, in `~/.local/bin` (Day 6) |
| gitleaks | 8.30.1 | `gitleaks version` |

### Go libraries (`go.mod`)

| Module | Version |
|---|---|
| sigs.k8s.io/controller-runtime | v0.25.0 |
| k8s.io/api, k8s.io/apimachinery, k8s.io/client-go | v0.37.0 |
| github.com/argoproj/argo-rollouts | v1.10.0 (Day 2; Rollout types, matching the installed controller) |
| github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring | v0.93.1 (Day 2; API-only module, matching the installed operator) |
| github.com/prometheus/client_golang | v1.24.0 (Day 2; demo app metrics, already a controller-runtime dependency) |
| google.golang.org/grpc | v1.83.2 (Day 6; indirect, raised from v1.82.1 for CVE-2026-84304 and CVE-2026-84445) |

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
| argocd | argocd | argo/argo-cd | 10.9.0 | Argo CD v3.5.2 (Day 6; dex and notifications disabled) |

### GitHub Actions (pinned by commit SHA, Day 6)

| Action | Version | Commit |
|---|---|---|
| actions/checkout | v6.0.2 | `de0fac2e4500dabe0009e67214ff5f5447ce83dd` |
| actions/setup-go | v6.3.0 | `4b73464bb391d4059bd26b0524d20df3927bd417` |
| aquasecurity/trivy-action | v0.36.0, running Trivy v0.70.0 | `ed142fd0673e97e23eac54620cfb913e5ce36c25` (setup-trivy inside it pinned to `3fb12ec12f41e471780db15c232d5dd185dcb514`, v0.2.6) |

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

---

## Day 4: burn-rate reads, error budget in status, drift correction

Agreed with the user before building (2026-09-13):
- `SLOHealthy` is `False` only when the budget is used up or the 1h burn rate reaches 14.4x, and
  `Unknown` with no traffic or no Prometheus.
- The budget is computed over the claim's SLO window, with Prometheus's default 10-day
  retention stated rather than changed.
- Status formats, Event wording and the restore-time method as proposed.

### Acceptance

Run on 2026-09-13 against the running cluster with both example claims in place. Output is copied
from the terminal and shortened where marked.

```text
$ go test ./internal/slo/... -v
(24 top-level tests pass; the budget math ones:)
--- PASS: TestSpendBudget (0.00s)
    --- PASS: TestSpendBudget/perfect_service (0.00s)
    --- PASS: TestSpendBudget/exactly_at_the_objective (0.00s)
    --- PASS: TestSpendBudget/half_the_budget_spent (0.00s)
    --- PASS: TestSpendBudget/2x_over_the_objective (0.00s)
    --- PASS: TestSpendBudget/every_request_failing (0.00s)
    --- PASS: TestSpendBudget/a_tighter_objective_spends_faster (0.00s)
    --- PASS: TestSpendBudget/rounding_below_zero_counts_as_zero (0.00s)
--- PASS: TestSpendBudgetGuardsAgainstAZeroBudget (0.00s)
--- PASS: TestSpendBudgetRejectsNonNumbers (0.00s)
--- PASS: TestBurnRate (0.00s)
ok  	github.com/singha105/paved/internal/slo	0.461s

# First, the controller with its default in-cluster Prometheus URL, which doesn't resolve from this Mac
$ kubectl get serviceclaims -A
NAMESPACE         NAME               TIER     OWNER               BUDGET   FROZEN   READY   AGE
platform-claims   url-shortener      public   team-links                            False   11h
platform-claims   webhook-delivery   public   team-integrations                     False   11h
ResourcesSynced=True (Applied): Applied 12 of 12 managed resources
SLOHealthy=Unknown (PrometheusUnavailable): Could not read the 28d error ratio: querying Prometheus:
  Post "http://kps-kube-prometheus-stack-prometheus.monitoring.svc:9090/api/v1/query": dial tcp:
  lookup kps-kube-prometheus-stack-prometheus.monitoring.svc: no such host

# Then with --prometheus-url=http://localhost:9090 through a port-forward
$ kubectl get serviceclaims -A
NAMESPACE         NAME               TIER     OWNER               BUDGET   FROZEN   READY   AGE
platform-claims   url-shortener      public   team-links          0.0%              False   11h
platform-claims   webhook-delivery   public   team-integrations   100.0%            False   11h
url-shortener: False (BudgetExhausted) The 28d error budget is used up: 119.27x of it spent
webhook-delivery: True (WithinBudget) 100.0% of the 7d error budget left; no requests in the last hour

# Drive errors at /boom on budget-demo, a fresh claim, with healthy traffic running throughout
budget-demo, healthy traffic only:           100.0% (WithinBudget)
after 40 requests to /boom, waited 4 min:    100.0% (WithinBudget)    <- not counted; see notes
after 400 more requests to /boom:              0.0% (BudgetExhausted)
  "The 28d error budget is used up: 1.63x of it spent"

$ kubectl delete prometheusrule -n svc-url-shortener --all
prometheusrule.monitoring.coreos.com "url-shortener" deleted from svc-url-shortener namespace
$ sleep 5 && kubectl get prometheusrule -n svc-url-shortener
NAME            AGE
url-shortener   5s
$ kubectl get events -n platform-claims --field-selector reason=DriftCorrected
LAST SEEN   TYPE     REASON           OBJECT                       MESSAGE
5s          Normal   DriftCorrected   serviceclaim/url-shortener   Recreated PrometheusRule svc-url-shortener/url-shortener
```

Extra checks beyond the acceptance list:

- **Fail-open recorded no false drift.** The controller started against two claims already in
  sync from Day 3 and recorded 0 `DriftCorrected` Events.
- **Recording:** `demo/02-drift.cast` (asciinema 3.2.1, 100x30) shows the rule deleted, `kubectl wait`
  succeeding, "Restored 72 ms after the delete returned", and the `DriftCorrected` Event.
- **envtest:** it covers the 60-second requeue, the status values, fail-open with resources still in
  sync, a recreated Service, a reverted NetworkPolicy edit, and a spec change recording no Event.
- **Cleanup:** deleting `budget-demo` removed its namespace, and the claim was gone.
- **Controller log:** 0 error lines across the Day 4 runs. **`make lint`:** 0 issues.

### Measurements

| What | Value | How measured |
|---|---|---|
| Restore time for a deleted PrometheusRule | min 65 ms, median 78 ms, max 99 ms over 5 runs (72 ms in the recording) | A watch on the rule, timing from `kubectl delete` returning to the watch's `ADDED` event |
| Statement coverage | builders 90.2%, controller 86.6%, slo 93.0% | `make test` |

### Deviations from the spec

- **A temporary claim, `budget-demo`, showed the budget dropping.** `url-shortener`'s budget was
  already 0.0% from Day 3's `/boom` test, and it will stay there until those errors age out of
  Prometheus. `budget-demo` used the same test image, wasn't committed, and was deleted afterwards.

- **Drift is caught by the label-mapped watches from Day 2 (ADR-006), not `Owns()`.** Owner
  references can't cross from `svc-<claim>` to `platform-claims`.
- **The objective-100 guard has two layers.** The CRD and `ErrorBudget` already reject 100, and
  `SpendBudget` also returns an error for a zero budget rather than dividing by it.
- **The controller runs through `go run ./cmd/main.go --prometheus-url=http://localhost:9090`.**
  `make run` passes no flags, and the in-cluster default address doesn't resolve from this Mac.
- **Events use the `events.k8s.io/v1` API**, because controller-runtime deprecates
  `GetEventRecorderFor` in favour of `GetEventRecorder`.

### Notes for later days

- With 10 days of retention, a 28d or 30d budget counts only the last 10 days (ADR-014 and the
  agreed decision above).
- Drift detection makes one uncached GET per managed object per reconcile (ADR-015).
- `SLOHealthy` doesn't feed `Ready` or a deploy freeze yet; that is the admission webhook's job.
- **The first failures on a new series aren't counted.** `budget-demo`'s first 40 requests to `/boom`
  created each pod's `code="500"` series. A range query shows the series first scraped already at
  20 per pod, and `rate()` treats a series' first sample as its starting point, so those 40 errors
  never reached the budget; the next 400 did. A real service has the same gap after every pod
  start. The usual fix is for a service to create its error series at zero on startup (for example
  `requests.WithLabelValues("500", "get")`). That would change `examples/testsvc` and the README's
  service contract, so it is raised with the user rather than done here.
- `kubectl get --watch-only --output-watch-events -o name` prints no event type, which is why the
  first restore-time script never saw `ADDED`. The measurement uses the default table output.

---

## Day 5: admission webhook, deploy freeze on budget exhaustion

Agreed with the user before building (2026-09-13):
- While the budget can't be measured, `DeploysFrozen` keeps its last decision.
- `paved.dev/break-glass` counts only when the update that changes the image also sets or changes
  it, and a blank reason counts as none.
- The demo ships a break-glass hotfix, and shows its Event, between the rejection and the recovery.
- The proposed defaults:
  - `Ready` means synced plus a healthy Rollout.
  - testsvc creates its request series at zero (the open item from Day 4), in its own commit.
  - `make docker-build` imports the image into k3d, and `make run` runs without webhooks.
  - `failurePolicy: Fail`, and a rollback needs break-glass during a freeze.
  - The rejection message is one line.
  - `BreakGlassUsed` is a `Warning` Event from `paved-webhook`, never recorded for dry runs.
  - The webhook trusts `DeploysFrozen` as last written, and a fast burn alone doesn't freeze.

### Acceptance

Run on 2026-09-13 against the running cluster, with both example claims in place and the controller
deployed in `paved-system`. Output is copied from the terminal and shortened where marked.

```text
$ make docker-build deploy
make exit code: 0
#15 naming to docker.io/library/paved-controller:dev done
INFO[0003] Importing images from tarball '/k3d/images/k3d-paved-images-20260913181131.tar' into node 'k3d-paved-server-0'...
(kubectl apply output; every other object "unchanged")
certificate.cert-manager.io/paved-serving-cert unchanged
issuer.cert-manager.io/paved-selfsigned-issuer unchanged
validatingwebhookconfiguration.admissionregistration.k8s.io/paved-validating-webhook-configuration configured

# cert-manager issued the serving certificate before anything relied on it
$ kubectl wait --for=condition=Ready certificate/paved-serving-cert -n paved-system --timeout=120s
certificate.cert-manager.io/paved-serving-cert condition met
$ kubectl get certificate -n paved-system
NAME                 READY   SECRET                AGE
paved-serving-cert   True    webhook-server-cert   52m
$ kubectl get secret webhook-server-cert -n paved-system
NAME                  TYPE                DATA   AGE
webhook-server-cert   kubernetes.io/tls   3      52m
$ kubectl get pods -n paved-system
NAME                                        READY   STATUS    RESTARTS   AGE
paved-controller-manager-6c94dc8c7f-r4dk5   1/1     Running   0          20m

$ kubectl get validatingwebhookconfiguration | grep paved
paved-validating-webhook-configuration   1          52m

$ ./demo/freeze-demo.sh
freeze-demo.sh exit code: 0 after 487 s
(key lines from its output:)
# Setup: put url-shortener back on k3d-paved-registry:5001/testsvc:0.1.1
# A bad release: some requests now fail (the demo sends them to /boom)
  budget 0.0%    1h burn rate 0.89   DeploysFrozen=True (BudgetExhausted)  Ready=True
  budget 0.0%    1h burn rate 1.03   DeploysFrozen=True (BudgetExhausted)  Ready=True
No 28d error budget left; image changes are rejected until 5.0% of it is back
# The next release, 0.2.0, is ready. Try to ship it
Error from server (Forbidden): admission webhook "vserviceclaim-v1alpha1.paved.dev" denied the request: deploys frozen: url-shortener has 0% error budget remaining in a 28d window (burn rate 1.0x). Override with annotation paved.dev/break-glass="<reason>" — this is audited.
# An urgent hotfix can still go out, on the record: break-glass with a reason
Warning: deploys are frozen: this image change was let through by paved.dev/break-glass and recorded in a BreakGlassUsed Event
serviceclaim.platform.paved.dev/url-shortener patched
# The hotfix is out and the errors stop
  budget 0.0%    1h burn rate 1.15   DeploysFrozen=True (BudgetExhausted)  Ready=False
  budget 14.7%   1h burn rate 1.15   DeploysFrozen=False (WithinBudget)  Ready=False
# Deploys are unfrozen. Retry the same release
serviceclaim.platform.paved.dev/url-shortener patched
url-shortener   k3d-paved-registry:5001/testsvc:0.2.0   Healthy
# Done: nothing shipped while the budget was gone, except one change that is on the record

$ kubectl get events -A --field-selector reason=BreakGlassUsed
NAMESPACE         LAST SEEN   TYPE      REASON           OBJECT                       MESSAGE
platform-claims   27m         Warning   BreakGlassUsed   serviceclaim/url-shortener   system:admin changed the image from k3d-paved-registry:5001/testsvc:0.1.1 to k3d-paved-registry:5001/testsvc:0.1.2 during a deploy freeze: INC-1234: hotfix for the 500s
platform-claims   19m         Warning   BreakGlassUsed   serviceclaim/url-shortener   system:admin changed the image from k3d-paved-registry:5001/testsvc:0.1.1 to k3d-paved-registry:5001/testsvc:0.1.2 during a deploy freeze: INC-1234: hotfix for the 500s
platform-claims   11m         Warning   BreakGlassUsed   serviceclaim/url-shortener   system:admin changed the image from k3d-paved-registry:5001/testsvc:0.1.1 to k3d-paved-registry:5001/testsvc:0.1.2 during a deploy freeze: INC-1234: hotfix for the 500s
platform-claims   2m48s       Warning   BreakGlassUsed   serviceclaim/url-shortener   system:admin changed the image from k3d-paved-registry:5001/testsvc:0.1.1 to k3d-paved-registry:5001/testsvc:0.1.2 during a deploy freeze: INC-1234: hotfix for the 500s

$ kubectl get serviceclaims -A
NAMESPACE         NAME               TIER     OWNER               BUDGET   FROZEN   READY   AGE
platform-claims   url-shortener      public   team-links          39.8%    False    True    16h
platform-claims   webhook-delivery   public   team-integrations   100.0%   False    True    16h

controller ERROR log lines: 0
```

The four `BreakGlassUsed` Events are the four demo runs of the day, each recorded separately: the
first unattended run, two recordings, and this acceptance run. Each run's Event shows up in the
next run's output, so the audit trail keeps every use.

Extra checks beyond the acceptance list:

- **cert-manager issued the certificate before anything relied on it.** Certificate
  `paved-serving-cert` was `Ready=True` with Secret `webhook-server-cert` (`kubernetes.io/tls`) for
  `paved-webhook-service.paved-system.svc` and `.svc.cluster.local`. Its CA was injected into
  `paved-validating-webhook-configuration`, valid until 2026-12-12, and was still there after
  redeploys.
- **`failurePolicy: Fail` holds.** With the controller scaled to zero, `kubectl annotate` on a claim
  failed with `failed calling webhook "vserviceclaim-v1alpha1.paved.dev" ... no endpoints available`.
  Once it was back, the same write was admitted.
- **The request series exist before any traffic.** Prometheus had `http_requests_total{code="200"} 0`
  and `{code="500"} 0` for both url-shortener pods on testsvc 0.1.1.
- **`Ready` follows the Rollout.** After the controller was redeployed, both claims were
  `Ready=True (RolloutHealthy)`. During the demo's canary it went `False` and came back `True`.
- **Recording:** `demo/03-freeze.cast` (asciinema, 120x34, idle time limit 2 s) is a full run of
  `demo/freeze-demo.sh`. It shows the budget draining from 91.7% to 0.0% and the rejection with
  `burn rate 1.0x`. Then the break-glass Event, the recovery to 11.2% that unfreezes deploys, and 0.2.0
  rolled out with `Ready=True`.
- **Redeploys leave the CA bundle alone.** `make deploy` prints `configured` for the
  ValidatingWebhookConfiguration each time. Its managedFields show cert-manager's cainjector last
  wrote at 21:19:03 and the generation is 3 (create, CA injection, the `sideEffects` change), so no
  redeploy removed the injected CA.
- **envtest and unit tests** cover the freeze through the real reconcile: exhausted, recovering at 3%,
  held while Prometheus is down, unfrozen at 6%, still unfrozen back at 3%. They also cover every `Ready`
  reason. Through the API server they cover a rejected image change (`403 Forbidden`, with the
  message), an admitted scale change, and a break-glass change whose Event lands. Unit tests cover the
  exact message, `unknown` values, stale and blank reasons, dry runs, and a long reason kept within
  the Event size limit.

### Measurements

| What | Value | How measured |
|---|---|---|
| Controller memory | 41 MiB of its 128 MiB limit, with two claims, shortly after start | `kubectl top pod -n paved-system` |
| Unattended `freeze-demo.sh` runs | 4 min 35 s (first run, error bursts); 487 s (acceptance run, including the setup canary) | Wall clock from start to exit 0 |
| `demo/03-freeze.cast` | 346 s recorded, about 50 s of playback | Sum of the cast's event intervals, uncapped and capped at the 2 s idle limit |
| Statement coverage | builders 90.2%, controller 89.1%, slo 93.0%, webhook 95.1% | `make test` |

### Deviations from the spec

- **Six commits.** The user asked for at least five, and the test service fix is its own commit.
- **The hotfix tag is 0.1.2.** 0.1.1 carries the request-series fix and became the example claims'
  image, so the demo starts on 0.1.1, hotfixes to 0.1.2 and releases 0.2.0.
- **The webhook is named `vserviceclaim-v1alpha1.paved.dev`.** The scaffold's `.kb.io` is kubebuilder's
  placeholder domain.
- **The scaffold's metrics certificate is left out**, since nothing uses it (ADR-016).
- **The rejection message is one line**, which the spec wraps only for display. `0%` and `14.2x`
  are the status values `0.0%` and `14.20`, shortened for the message.
- **The demo's hotfix and release are the same test service under new tags.** The errors stop because
  the script stops sending requests to `/boom`, and it sends much more traffic to speed the recovery.
  The narration says both.
- **The controller commit was amended before the push** to read the Rollout without the cache. The
  demo's final wait showed that a cached Rollout from before an apply could report `Ready` for the
  previous generation (ADR-019).

### Notes for later days

- **The first demo run sent errors in bursts, which the recording can't use.** Its rejection read
  `burn rate 0.0x`, because the 1h recording rule, evaluated every 30 s, lagged the raw window query.
  And 50 errors against a minute of traffic spent the budget only 1.32x, so the freeze could lift
  within a minute. The demo now sends a steady trickle of errors until the hotfix ships, and waits
  for a non-zero burn rate before trying to ship.
- **`BudgetRecovering` rarely shows in the demo.** With only minutes of history, the budget can pass
  from 0% to over 5% between two reconciles (run 1 went from 0.0% straight to 16.2%). envtest and the
  unit tests cover that state.
- **Restarting the k3d cluster recreated the Prometheus pod**, and its `emptyDir` storage lost every
  sample. After a cluster restart, all budgets start again from no data, so Day 4's 0.0% budget for
  url-shortener is gone.
- **BreakGlassUsed Events expire** after the API server's default `--event-ttl` of one hour. The
  Events check has to run within an hour of the demo.
- url-shortener still carries a `kick: day3` annotation from Day 3 testing.
- **Resolved spec questions:** the unfreeze threshold (5%), rollbacks during a freeze (they need
  break-glass) and the webhook failure policy (`Fail`).
- **Still open:** claim name and namespace validation, ingress TLS, the batch tier's ServiceMonitor,
  and the `platform-system` namespace that managed NetworkPolicies admit. The controller runs in
  `paved-system`, but it never calls the services, so nothing is blocked.

---

## Day 6: CI/CD, GitOps, image scanning, canary auto-rollback

Agreed with the user before building (2026-09-14):
- The cluster pulls the operator image from GHCR, public.
- Demo 4 ships the bad image by committing it to main, and a second commit reverts it. Argo CD
  self-heal is on, and `freeze-demo.sh` pauses the claims app's automated sync while it runs.
- The vulnerable base image lives only on the branch `demo/trivy-catch`; main never carries it.
- The canary pauses grow to 2 minutes, so the analysis can see a bad version before it is promoted.
- The proposed defaults:
  - grpc 1.83.2; `ci.yaml` replaces `test.yml`; every action pinned by SHA.
  - Trivy fails on fixable HIGH and CRITICAL vulnerabilities; images publish only from main.
  - Argo CD 10.9.0 without dex or notifications, an app-of-apps under `deploy/argocd`, and the claims
    in `deploy/claims`.
  - One AnalysisTemplate per public or internal claim, reading its 5m rule every 30 seconds and
    failing on `len(result) > 0 && result[0] > 0.05`, run in the background from the first pause.
  - The bad image is `testsvc:0.2.1-bad`; the rollouts plugin is v1.10.0.
  - The abort time is measured in the recording and two more runs.

### Acceptance

Run on 2026-09-14 against the running cluster, with Argo CD delivering the operator and both
claims. Output is copied from the terminal and shortened where marked.

```text
$ gh run list --limit 5
completed	success	demo: revert url-shortener to 0.1.1 after its canary aborted	Lint	main	push	34811499912	1m16s	2026-09-14T05:57:23Z
completed	success	demo: revert url-shortener to 0.1.1 after its canary aborted	CI	main	push	34811499907	3m44s	2026-09-14T05:57:23Z
completed	success	demo: revert url-shortener to 0.1.1 after its canary aborted	E2E Tests	main	push	34811499891	4m31s	2026-09-14T05:57:23Z
completed	success	demo: ship url-shortener 0.2.1-bad, a release that fails every request	E2E Tests	main	push	34811435274	4m44s	2026-09-14T05:56:15Z
completed	success	demo: ship url-shortener 0.2.1-bad, a release that fails every request	CI	main	push	34811435272	3m40s	2026-09-14T05:56:15Z

$ gh run list --branch demo/trivy-catch --limit 3
completed	success	demo: build the operator on a vulnerable base image so Trivy fails CI	Lint	demo/trivy-catch	push	34807296381	3m51s	2026-09-14T04:46:56Z
completed	failure	demo: build the operator on a vulnerable base image so Trivy fails CI	CI	demo/trivy-catch	push	34807296384	4m51s	2026-09-14T04:46:56Z
completed	success	demo: build the operator on a vulnerable base image so Trivy fails CI	E2E Tests	demo/trivy-catch	push	34807296426	6m31s	2026-09-14T04:46:56Z
(none of the last 40 runs on main has a conclusion other than success)

$ kubectl get applications -n argocd
NAME              SYNC STATUS   HEALTH STATUS
paved             Synced        Healthy
paved-operator    Synced        Healthy
platform-claims   Synced        Healthy

$ kubectl argo rollouts get rollout url-shortener -n svc-url-shortener --watch
(captured through the third measurement run of canary-demo.sh; each status change, in order)
Status:          ✔ Healthy
Status:          ◌ Progressing
Message:         more replicas need to be updated
Status:          ॥ Paused
Message:         CanaryPauseStep
Status:          ✖ Degraded
Message:         RolloutAborted: Rollout aborted update to revision 23: Background analysis phase error/failed: Metric "sli-error-ratio" assessed Failed due to failed (1) > failureLimit (0)
Status:          ◌ Progressing
Message:         waiting for rollout spec update to be observed
Status:          ✔ Healthy

# The same run, from canary-demo.sh: the analysis readings and the abort time
  2026-09-14T05:56:18Z  Successful  error ratio [0]
  2026-09-14T05:56:48Z  Successful  error ratio [0]
  2026-09-14T05:57:18Z  Failed  error ratio [0.05110782034319856]
# Aborted 63s after the bad image reached the Rollout (applied 2026-09-14T05:56:15Z, aborted 2026-09-14T05:57:18Z)

$ kubectl get serviceclaims -A
NAMESPACE         NAME               TIER     OWNER               BUDGET   FROZEN   READY   AGE
platform-claims   url-shortener      public   team-links          28.1%    False    True    24h
platform-claims   webhook-delivery   public   team-integrations   100.0%   False    True    24h
```

The rollback to stable was automatic. At the abort, Argo Rollouts scaled the canary to zero, and in the
recording `kubectl get pods` then listed only the two 0.1.1 pods, before anyone reverted anything. The
last `Progressing` and `Healthy` came after the demo's revert commit made git agree with the cluster
again.

Extra checks beyond the acceptance list:

- **main would have failed its own scan.** Trivy on the Day 5 controller image found 2 HIGH
  vulnerabilities in `google.golang.org/grpc` 1.82.1 (CVE-2026-84304 and CVE-2026-84445). After the
  bump to 1.83.2 it found none, in the Debian packages or the Go binary.
- **The catch fails exactly at the scan.** On `demo/trivy-catch`, CI run 34807296384 passed every
  step up to "Scan the image with Trivy", which failed with `Total: 38 (HIGH: 34, CRITICAL: 4)` for
  `alpine 3.14.0`; "Push to GHCR" was skipped. Its parent on main, run 34807295309, passed every step
  including the push.
- **The published image is public.** With an anonymous GHCR token, the manifests of `sha-0d60c3c`
  and `sha-8d12dc8` returned HTTP 200. Nothing was changed in the package settings.
- **The analysis objects are what the builder says.** `kubectl get analysistemplates -A` listed
  `url-shortener-canary` and `webhook-delivery-canary`, with failure condition
  `len(result) > 0 && result[0] > 0.05` and query
  `sli:http_availability:error_ratio_rate5m{service="url-shortener"}`. The Rollout carried
  `analysis={"startingStep":1,"templates":[{"templateName":"url-shortener-canary"}]}` and pauses of `2m`.
- **The analysis lets a good release through.** When Argo CD first set url-shortener back to 0.1.1,
  the canary finished `Healthy` and AnalysisRun `url-shortener-95d6ffc94-14` was `Successful` after 9
  measurements. All 9 were `NaN`, because nothing was sending traffic, and no data passes by design.
- **Argo CD took over the running operator.** About 32 seconds after `kubectl apply -f
  deploy/argocd/root.yaml`, all three Applications were `Synced` and `Healthy`, and the controller
  Deployment ran `ghcr.io/singha105/paved:sha-8d12dc8`.
- **The bad image deploys cleanly and then fails.** Run locally, `testsvc:0.2.1-bad` answered HTTP 500
  on `/` and HTTP 200 on `/healthz`, `/readyz` and `/metrics`, and logged `broken=true`.

### Measurements

| What | Value | How measured |
|---|---|---|
| Canary abort time | 62 s in the recording, 63 s and 63 s in two more runs (median 63 s). A first attempt, whose revert step failed, measured 70 s. One run that aborted after 5 s, on errors left from an earlier run, is excluded | From the time paved's server-side apply put the bad image on the Rollout (its `managedFields` entry) to the Rollout's `status.abortedAt` |
| Argo CD memory | 126 MiB across its five pods, just after install | `kubectl top pods -n argocd` |
| `demo/04-canary.cast` | 217 s recorded, about 23 s of playback | Sum of the cast's event intervals, uncapped and capped at the 2 s idle limit |
| url-shortener error budget spent by five bad canaries | 38.5% remaining before, 28.1% after | `kubectl get serviceclaims` |
| Statement coverage | builders 90.7%, controller 89.1%, slo 93.0%, webhook 95.1% | `make test` |

### Deviations from the spec

- **The claims moved to `deploy/claims` in the analysis commit (8d12dc8), not the GitOps commit.** The
  rename was already staged when that commit was made, and it had been pushed before this was
  noticed, so history was not rewritten.
- **The failure condition is `len(result) > 0 && result[0] > 0.05`, not `result > 0.05`.** Argo
  Rollouts hands a Prometheus vector to the condition as a list, and an empty result has to pass
  rather than error.
- **`ci.yaml` runs envtest through `make test`**, which also reruns `go vet` and the unit tests.
- **main has no revert commit for the Trivy catch**, because main never carried the vulnerable base
  image (agreed). The branch stays pushed so its failed run can be linked.
- **Each `canary-demo.sh` run adds two commits to main**, the bad image and its revert (agreed).
- **The first two recording attempts left extra demo commits on main.** The first stopped after the
  abort, when it looked up the AnalysisRun by the name an aborted Rollout no longer reports, so its
  revert (aac8186) was committed by hand. The second aborted 5 seconds in, because errors from the
  first attempt were still inside the 5-minute window when its canary began. That is not a
  measurement of the new release, so it is excluded, and the script now finds the newest AnalysisRun
  and waits for the 5m error ratio to fall below 0.01 before shipping.

### Notes for later days

- **Disk and Docker failed mid-day.** The Mac's disk filled to 128 MiB free, Docker's VM went
  read-only, and k3s answered writes with `attempt to write a readonly database`. It was fixed by
  clearing the Go build cache (with the user's OK) and the Trivy cache, and restarting Docker Desktop.
  Later, during the first demo 4 attempt, Docker Desktop's engine stopped answering: every API call
  returned HTTP 500. After the user's interruption Docker was started again, and the cluster came back
  with its data, Prometheus's history included. The cause of the second failure was not established.
- **The `gvenzl/oracle-xe` images (14.6 GB) are still there.** Deleting them was blocked by the
  permission check.
- **A controller upgrade that changes the builders is reported as drift.** When the analysis builder
  was deployed, both claims got `DriftCorrected` Events for their Rollouts, because paved's own field
  set changed (ADR-015). Telling an upgrade from an edit needs more design.
- **A Prometheus outage can abort a canary.** Query errors count against Argo Rollouts' default
  `consecutiveErrorLimit` of 4, the opposite of the fail-open rule in ADR-014 (ADR-022).
- **Canary failures spend the claim's error budget.** With about 1.48 million requests of history,
  url-shortener had room for roughly 2,800 more failures, which is why `canary-demo.sh` sends only
  about 10 requests a second. A claim with little history could freeze itself during its own bad
  canary, and then the revert commit is rejected until the budget recovers or it adds a break-glass
  reason.
- **The cluster can't receive GitHub webhooks.** Argo CD polls git, and the demos annotate the claims
  app to make it check immediately.
- Still open from earlier days: claim name and namespace validation, ingress TLS, the batch tier's
  ServiceMonitor, and the `platform-system` namespace in managed NetworkPolicies.
