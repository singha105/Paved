# PROGRESS

| Day | Goal | Status |
|---|---|---|
| 1 | Cluster, scaffold, API, no-op reconcile | Done 2026-09-12 |

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
| golangci-lint | v2.13.1 | `GOLANGCI_LINT_VERSION` in `Makefile` (not downloaded yet) |

### Go libraries (`go.mod`)

| Module | Version |
|---|---|
| sigs.k8s.io/controller-runtime | v0.25.0 |
| k8s.io/api, k8s.io/apimachinery, k8s.io/client-go | v0.37.0 |

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
