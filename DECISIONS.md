# Architecture Decision Records

Short records of the choices in paved that a reader would otherwise have to reverse-engineer.
Each one says what was decided, why, and what it costs.

---

## ADR-001: Developers cannot set limits, security context, rollout strategy, probes, or canary steps

**Status:** Accepted (Day 1)

**Context.** A `ServiceClaim` is the golden path. Every field it exposes is a field every
team has to understand, and a way to drift from the platform's standards. The fields most
often behind workload incidents are exactly these: missing resource limits (noisy
neighbours, node OOMs), containers running as root, rollout strategies that take a service
down during a deploy, and probes that restart healthy pods.

**Decision.** The API exposes only what genuinely differs per service: owner, image, port,
tier, SLI, SLO and scale bounds. Resource limits, `securityContext`, the replica strategy,
probes and the canary steps are constants in the platform code (`internal/builders`),
identical for every claim of a given tier.

**Consequences.**
- Changing a standard is one code change plus a controller rollout. Every service picks it
  up on its next reconcile, with no per-team migration.
- A service with unusual needs (a large memory footprint, a custom health endpoint) cannot
  be onboarded through `v1alpha1`. Supporting one means a reviewed API change, not a
  free-form override field.
- The builders take a small, fully validated input, so they can be tested exhaustively
  without a cluster.

---

## ADR-002: Server-side apply with one field owner for everything the controller writes

**Status:** Accepted (Day 1)

**Context.** The classic controller loop is get, mutate, update. It races with every other
writer (an HPA changing replicas, a person running `kubectl edit`), needs retries on
`resourceVersion` conflicts, and quietly takes over fields the controller never meant to own.

**Decision.** Every object the controller manages, and the claim's own status, is written
with server-side apply under the field owner `paved-controller`, with forced ownership. Each
reconcile builds the desired fields from scratch and sends only those.

**Consequences.**
- Drift correction is a single apply: if someone edits a field the controller owns, the
  next reconcile sets it back. Fields owned by other managers are left alone.
- There is no read-modify-write cycle and no conflict retry logic.
- An apply that changes nothing is not stored. On Day 1, restarting the controller
  re-reconciled an existing claim and its `resourceVersion` did not change, so writing status
  on every reconcile doesn't trigger an endless loop of reconciles.
- controller-runtime v0.25.0 deprecates the `client.Apply` patch type, so the controller
  uses `Client.Apply` and `Status().Apply` with apply configurations instead.

---

## ADR-003: Traefik instead of ingress-nginx

**Status:** Accepted (Day 1)

**Context.** The original stack named ingress-nginx. The `kubernetes/ingress-nginx`
project has been retired: the repository is archived, its last release was on 2026-03-19,
and its README states there will be no further releases or security fixes. A platform that
exposes public services should not route them through an unmaintained ingress controller.

**Decision.** Install Traefik with Helm into its own `traefik` namespace. The copy of
Traefik bundled with k3s (which runs in `kube-system`) is disabled when the cluster is created.

**Consequences.**
- Public services still get standard `networking.k8s.io/v1` Ingress objects, so the
  builders don't depend on which ingress controller is installed.
- The public tier's NetworkPolicy can allow exactly the `traefik` namespace. Using the
  bundled copy would have meant allowing all of `kube-system`.

---

## ADR-004: The SLO objective is a string checked by CEL, and 100 is rejected

**Status:** Accepted (Day 1)

**Context.** The API stores the objective as a string (`"99.5"`), because Kubernetes API
conventions discourage floats in CRDs. The first draft put `Minimum`/`Maximum` markers on
that string, but those only apply to numeric schema types, and controller-gen v0.22.0
refuses them: `must apply minimum to a numeric value, found string`.

**Decision.** Keep the string. A regex pattern checks that it is a plain decimal number, and
a CEL rule checks the range `90 <= objective < 100`. Both are part of the CRD schema.

**Consequences.**
- The API server itself rejects invalid objectives on create and update, even before the
  admission webhook exists or while it is down.
- An objective of 100 is rejected. It would leave an error budget of zero, and burn rate
  (error ratio divided by the budget) would divide by zero.

---

## ADR-005: Prometheus picks up monitoring objects from every namespace

**Status:** Accepted (Day 1)

**Context.** kube-prometheus-stack sets `serviceMonitorSelectorNilUsesHelmValues`,
`podMonitorSelectorNilUsesHelmValues` and `ruleSelectorNilUsesHelmValues` to `true` by
default. Prometheus then ignores any ServiceMonitor, PodMonitor or PrometheusRule that
lacks the chart's Helm release label. The controller creates these objects in each
claim's `svc-<name>` namespace.

**Decision.** Set all three to `false` in `hack/values/kube-prometheus-stack.yaml`. The
matching namespace selectors already default to every namespace.

**Consequences.**
- Generated objects are scraped and evaluated without carrying a label tied to one
  Helm release name, so the builders don't depend on how Prometheus was installed.
- Any namespace can add scrape targets and rules to this Prometheus. That is acceptable for
  a cluster the platform team owns.

---

## ADR-006: Managed objects are tied to their claim by labels and a finalizer, not owner references

**Status:** Accepted (Day 2)

**Context.** A claim lives in `platform-claims`, but its objects live in `svc-<name>`, and
that Namespace is cluster-scoped. Kubernetes only honours an owner reference to an owner in
the same namespace, or to a cluster-scoped owner. controller-runtime's
`SetControllerReference` refuses the cross-namespace case outright. A hand-written owner
reference is worse: on Day 2, a ConfigMap in another namespace carrying an owner reference
to a claim was deleted by the garbage collector within one second (event
`OwnerRefInvalidNamespace`). Following the original plan (an owner reference on every object,
plus `Owns()` watches) would have had the garbage collector delete every managed object as
fast as the controller created it.

**Decision.** Managed objects carry no owner references. Each one is labelled
`paved.dev/claim` and `paved.dev/claim-namespace`, alongside `app.kubernetes.io/name`,
`app.kubernetes.io/managed-by: paved` and `paved.dev/owner`. The controller watches every
managed type and maps each event back to a claim through those two labels. A finalizer,
`paved.dev/finalizer`, deletes `svc-<name>`, which removes everything inside it, and is
removed only once the namespace is gone.

**Consequences.**
- Drift still triggers a reconcile: deleting or editing a managed object queues its claim,
  and the next apply puts the object back.
- Cleanup is the controller's job, not the garbage collector's. If the controller is down, a
  deleted claim stays in `Terminating` until it comes back.
- There is one label more than the original four, because a claim name alone doesn't say
  which namespace the claim is in.

---

## ADR-007: NetworkPolicy lets Prometheus in and leaves egress open

**Status:** Accepted (Day 2)

**Context.** Each claim's namespace denies incoming traffic by default. Public claims accept
traffic from the ingress controller; internal claims accept only the platform namespace and
their own namespace. Prometheus runs in `monitoring`, so a strict reading would block its
scrapes. k3s enforces NetworkPolicy, so the workload would produce no SLI metrics, burn
rate could never be computed, and deploys could never freeze. The original rule "egress
always allows DNS", read as "egress allows only DNS", would also break any service that
calls out, and `webhook-delivery` exists to call external URLs.

**Decision.** Each policy restricts ingress only. Every tier admits `monitoring` on the
service port. Public and internal also admit their own namespace and `platform-system`;
public additionally admits `traefik` on the service port. Egress is not restricted, so DNS
is always reachable.

**Consequences.**
- Prometheus can scrape every claimed workload, whatever its tier.
- A compromised pod can open outbound connections anywhere. Restricting egress properly needs
  a per-service list of allowed destinations, which the `v1alpha1` API does not have.

---

## ADR-008: The HPA owns the replica count, and tier floors never produce an invalid object

**Status:** Accepted (Day 2)

**Context.** Each tier sets a replica floor (a public service never runs fewer than two pods)
and the developer sets `scale.max`. The obvious implementation breaks in three ways:
- If the Rollout the controller applies includes `spec.replicas`, every reconcile resets the
  count and undoes whatever the HPA just decided.
- A public claim with `scale.max: 1` would produce an HPA with `minReplicas: 2` and
  `maxReplicas: 1`, which the API server rejects.
- A PodDisruptionBudget with `minAvailable: 1` on a single-replica service permits no
  evictions, so every node drain hangs.

**Decision.**
- For tiers with an HPA (public and internal), the Rollout leaves `spec.replicas` unset. Batch
  has no HPA and sets it to 1.
- The HPA's `minReplicas` is the tier floor and its `maxReplicas` is the larger of
  `scale.max` and that floor.
- The PodDisruptionBudget uses `maxUnavailable: 1`.
- The canary splits by replica count, without traffic routing, so it needs no separate
  canary and stable Services.

**Consequences.**
- The controller and the HPA never fight over the replica count.
- A public claim asking for at most one pod gets two. The floor is a platform guarantee, but
  the claim's status doesn't yet say that its maximum was raised.
- With only a few replicas the canary percentages are coarse, because they count pods, not
  requests.
- A public claim stays at 11 managed resources.

---

## ADR-009: A small demo app stands in for the example services

**Status:** Superseded by ADR-012 (Day 3)

**Context.** The example claims pointed at images that don't exist yet. Pods that can't pull
their image never become ready, and Argo Rollouts may keep updating the Rollout's status
while it waits. That can make a check like "reconciling again leaves the Rollout's
`resourceVersion` unchanged" fail for reasons that have nothing to do with the controller.
The platform's pod contract is also strict enough that an arbitrary public image rarely
meets all of it: `/healthz` and `/readyz` on the claim's port, `/metrics` for Prometheus, UID
65532 and a read-only root filesystem.

**Decision.** `demo/app` is a small Go HTTP server that meets exactly that contract. It
exposes `http_requests_total` and `http_request_duration_seconds` (with a 0.25s bucket, to
match the default latency threshold) so the SLO work has something to measure, and it
excludes probe and scrape requests from both. `hack/demo-app.sh` builds it and loads it into
k3d with `k3d image import`, so no registry is involved. Both example claims run it.

**Consequences.**
- The Day 2 checks run against pods that genuinely become ready.
- The examples no longer show onboarding a real, unrelated service. That still has to be
  done with the real images.
- The image exists only inside the local cluster; recreating the cluster means running
  `hack/demo-app.sh` again.

---

## ADR-010: The controller's own writes never retrigger it, and it caches only platform objects

**Status:** Accepted (Day 2)

**Context.** Three details of the reconcile loop would each have caused trouble:
- Every reconcile writes `status.lastReconcileTime`. With a plain watch on ServiceClaims, that
  write is an update event, which queues the claim again, which writes a new time: a loop
  that never goes idle.
- Watching Namespaces, ConfigMaps and Services means caching them, and by default the cache
  holds every object of those kinds in the cluster, including kube-prometheus-stack's large
  dashboard ConfigMaps.
- The builders return typed Go objects. Converted for server-side apply, they also carry a
  null `creationTimestamp` and an empty `status`, which the controller would then own.

**Decision.**
- The ServiceClaim watch admits creation, deletion, and updates that change the generation,
  labels or annotations. Status-only updates are dropped. Managed objects are watched without
  a filter, so any change to them still queues their claim.
- For every managed kind, the manager caches only objects labelled
  `app.kubernetes.io/managed-by: paved`.
- Before applying, the controller removes `metadata.creationTimestamp`, the pod template's
  `creationTimestamp`, and `status` from the request.

**Consequences.**
- A reconcile that finds nothing to change leaves every managed object's `resourceVersion`
  as it was. The envtest suite asserts this for all 10 internal-tier objects.
- `kubectl annotate` on a claim triggers a reconcile; a status-only edit to a claim does not.
- The controller cannot see an object of a managed kind that lacks the label. A pre-existing,
  unlabelled namespace called `svc-<name>` therefore looks absent to the finalizer, which
  releases the claim without deleting it.

---

## ADR-011: Every claim gets its own runbook, which grows the tiers to 12, 11 and 8 objects

**Status:** Accepted (Day 3)

**Context.** An engineer paged at night needs to learn quickly what the service is, who owns
it, what this alert means for this service, and what to check first. Most of that is specific
to the claim: the owner, the objective, the thresholds, how long the budget lasts. A single
shared document can't hold it. Alert annotations conventionally carry a runbook URL, but a
Kubernetes object has no URL.

**Decision.** Each claim gets a ConfigMap `<claim>-runbook` holding a generated `runbook.md`:
the service and its SLI and SLO, its owner, a table of the four alerts with this claim's
thresholds and how long the budget lasts at each, and three first debugging steps. Every alert
carries two annotations: `runbook_url`, which links to the general guidance in
`docs/runbooks/slo-burn-rate.md` on GitHub, and `runbook`, which names the claim's ConfigMap.

**Consequences.**
- The runbook can't fall out of date with the alerts: both are generated from the same claim on
  every reconcile, using the same threshold arithmetic.
- The tier sets become 12 objects for public, 11 for internal and 8 for batch, one more than the
  original table. The builder tests and the envtest suite assert the new counts.
- Reading a service's own runbook takes a `kubectl` command; only the general guidance is a
  clickable link.

---

## ADR-012: The test service is pushed to a registry the cluster pulls from

**Status:** Accepted (Day 3). Supersedes ADR-009.

**Context.** The SLO rules and burn-rate alerts can only be proven against a workload whose
errors can be produced on demand. The Day 2 demo app met the platform's pod contract, but it
was loaded with `k3d image import`, which copies an image straight into the node. A real deploy
pushes an image to a registry and the cluster pulls it, and k3d can only connect a registry
to a cluster when the cluster is created.

**Decision.** The demo app moves to `examples/testsvc` and gains `GET /boom`, which answers 500
and is counted like any other request. `hack/cluster-up.sh` creates a k3d registry,
`k3d-paved-registry`, on `localhost:5001` (port 5000 is taken by macOS AirPlay Receiver) and
creates the cluster connected to it. If an existing cluster isn't connected, the script stops
and prints how to recreate it rather than deleting anything itself. `hack/testsvc-image.sh`
builds the image and pushes it to `localhost:5001/testsvc:<tag>`; claims reference it as
`k3d-paved-registry:5001/testsvc:<tag>`, which the node's registry mirror resolves.

**Consequences.**
- Changing a claim's image is a real push and pull, the same path a production deploy takes.
- The cluster had to be recreated once, from the script, to attach the registry.
- One registry has two names: `localhost:5001` from the laptop, `k3d-paved-registry:5001` from pods.
- Pushed images survive a cluster rebuild, as long as the registry container isn't deleted.

---

## ADR-013: Generated SLO rules must return data, not merely load

**Status:** Accepted (Day 3)

**Context.** A Prometheus rule that loads without errors but never returns a value looks
healthy and protects nothing. The generated rules have to be right for every claim: a service
that has never failed, a service with no traffic, and objectives whose arithmetic doesn't fit
neatly in a float.

**Decision.**
- **Recording rules do the expensive work once.** For each window they compute the error ratio
  from the raw counters, scoped by `namespace="svc-<claim>"`, and record it with only the
  `service`, `owner` and `tier` labels. The four alerts read those recorded series.
- **Availability uses `(bad requests or vector(0)) / all requests`.** Prometheus has no series
  for failed requests until the first failure. Without `or vector(0)`, a service that has never
  failed would record nothing instead of 0.
- **Latency uses `1 - (requests in the threshold bucket / all requests)`**, with the bucket
  label written the way Prometheus 3 stores it: a 1s threshold is `le="1.0"`, not `le="1"`
  (checked against the running Prometheus).
- **Thresholds are exact decimals.** Burn rate × budget is computed with rational arithmetic and
  written as a literal: 14.4 × 0.005 is `0.072`, where float64 would give `0.07200000000000001`.
- **Each alert needs both of its windows above the threshold, with no `for` clause.** All four
  share the name `SLOErrorBudgetBurn`; the `severity`, `long_window` and `short_window` labels
  tell them apart.

**Consequences.**
- A service with no traffic records no value, so its burn-rate alerts can't fire. Detecting a
  service that should have traffic and doesn't would need a different kind of alert.
- Two claims with the same name in different namespaces would record series with the same
  `service` label. Claims are meant to live only in `platform-claims`, but nothing enforces that yet.
- The error budget panel can look back only as far as Prometheus keeps data, 10 days by default.

---

## ADR-014: When Prometheus can't answer, the controller fails open

**Status:** Accepted (Day 4)

**Context.** Every minute the controller reads each claim's error budget from Prometheus, and
the deploy freeze will use that budget to reject image changes. Prometheus can be restarting,
overloaded, unreachable or slow, and a new service may have no data yet. If missing data
counted as an exhausted budget, a monitoring outage would freeze deploys across the whole
platform, at exactly the moment a team might need to ship a fix.

**Decision.** Each query times out after 5 seconds. When Prometheus can't be queried, the claim's
`SLOHealthy` condition is `Unknown` with reason `PrometheusUnavailable`, and
`errorBudgetRemaining` and `burnRate1h` are cleared rather than left at their last values. A
service with no requests in its window is `Unknown` with reason `NoTraffic`. Only a measured value
can make `SLOHealthy` `False`: `BudgetExhausted` when nothing is left, or `FastBurn` when the 1h
burn rate reaches 14.4, the fastest page alert's rate. Applying the claim's resources never waits
on Prometheus: they are reconciled whether or not the SLO can be read.

**Consequences.**
- A Prometheus outage can't block anyone's deploys. It shows up as `Unknown` on every claim,
  which is itself visible.
- During an outage, a service that really is out of budget can still deploy. That is the accepted
  cost: the burn-rate alerts are evaluated inside Prometheus and page once it recovers.
- `kubectl get serviceclaims` shows an empty BUDGET column during an outage, not a stale number
  that looks current.
- Each reconcile makes at most two queries, and stops after the first if Prometheus fails, so an
  outage costs one 5-second timeout per claim per minute.
