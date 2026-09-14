# Architecture Decision Records

Short records of the choices in paved that a reader would otherwise have to reverse-engineer.
Each one says what was decided, why, and what it costs.

---


## Start here

paved rests on a handful of decisions. The rest of this file explains them one at a time:

| Decision | Where |
|---|---|
| An operator, not a Helm chart | [ADR-024](#adr-024-paved-is-an-operator-not-a-helm-chart) |
| Server-side apply with one field owner, never get-then-update loops | [ADR-002](#adr-002-server-side-apply-with-one-field-owner-for-everything-the-controller-writes) |
| A finalizer and labels for the cluster-scoped namespace, not owner references | [ADR-006](#adr-006-managed-objects-are-tied-to-their-claim-by-labels-and-a-finalizer-not-owner-references) |
| Fail open when metrics are missing | [ADR-014](#adr-014-when-prometheus-cant-answer-the-controller-fails-open) |
| Freeze at an empty budget, unfreeze only at 5% | [ADR-017](#adr-017-deploys-freeze-when-the-budget-is-gone-and-unfreeze-only-at-5) |
| Break-glass instead of a hard block | [ADR-018](#adr-018-the-webhook-rejects-only-image-changes-and-break-glass-must-be-set-in-the-same-update) |
| One SLI for alerts, the freeze and the canary | [ADR-022](#adr-022-the-canary-is-gated-on-the-same-sli-recording-rule-as-the-alerts-and-the-freeze) |
| Storage through the cluster's own identity, with no AWS key anywhere | [ADR-025](#adr-025-claims-get-storage-through-the-clusters-own-identity-with-no-aws-key-anywhere) |

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

---

## ADR-015: Drift is detected from the controller's own field ownership

**Status:** Accepted (Day 4)

**Context.** The controller re-applies every managed object on every reconcile, which already
puts back anything deleted or edited. Reporting that as a `DriftCorrected` Event means telling a
real correction apart from everything else that changes those objects: the HPA rewrites the
Rollout's replica count, Argo Rollouts and the HPA update status constantly, and a new claim or
a spec edit changes objects on purpose. Comparing `resourceVersion`s before and after an apply
would report all of those as drift.

**Decision.** Before applying an object, the controller reads it straight from the API server,
bypassing the cache, and keeps the server-side apply entry for `paved-controller`: the fields it
owns and when they last changed. After the apply it compares that with the entry in the
server's response. The object had drifted if it didn't exist, or if the entry changed, meaning
the apply had to take fields back from another manager or reset their values. Events are only
recorded when the claim was already in sync at its current generation, so creating a claim's
resources and applying a spec edit never count.

**Consequences.**
- Other controllers' writes never touch `paved-controller`'s entry, and an apply that changes
  nothing leaves it byte-for-byte identical, so routine activity produces no Events.
- A change to a field the controller doesn't own, such as an extra label, isn't drift. It is
  left alone and not reported.
- Each correction is one `Normal` Event on the ServiceClaim, for example
  `Recreated PrometheusRule svc-url-shortener/url-shortener`.
- Every reconcile makes one uncached GET per managed object: 12 per public claim, at least once
  a minute.

---

## ADR-016: The controller runs in the cluster, and `make docker-build` loads it into k3d

**Status:** Accepted (Day 5)

**Context.** The API server calls the admission webhook over TLS, at the webhook Service, with a
certificate it trusts. cert-manager issues that certificate for the Service inside the cluster,
so the webhook, and the controller in the same binary, have to run there. A controller started on
the Mac can reconcile claims but can't answer admission requests. The scaffold's default image,
`controller:latest`, can't work on k3d either: nothing pushes it anywhere, and a `latest` tag makes
the kubelet try to pull it every time.

**Decision.** `IMG` defaults to `paved-controller:dev`. `make docker-build` builds it and, when a k3d
cluster named `paved` exists, imports it with `k3d image import`. The tag isn't `latest`, so the
kubelet uses the imported image instead of pulling. `make deploy` applies `config/default` with its
`[WEBHOOK]` and `[CERTMANAGER]` sections enabled. `make run` sets `ENABLE_WEBHOOKS=false`, because
there is no certificate outside the cluster.

**Consequences.**
- `make docker-build deploy` works as written. On machines without k3d, such as CI's Kind cluster,
  the import step is skipped.
- Rebuilding under the same tag doesn't restart the running pod:
  `kubectl rollout restart deployment/paved-controller-manager -n paved-system` picks up the new image.
- A controller run from the Mac doesn't enforce deploy freezes.
- The scaffold's metrics certificate is left out of `config/certmanager`, since the
  `[METRICS-WITH-CERTS]` patch that would use it is not enabled.

---

## ADR-017: Deploys freeze when the budget is gone and unfreeze only at 5%

**Status:** Accepted (Day 5)

**Context.** The controller re-reads each claim's error budget every minute. A single threshold at
zero would flap: a service near zero crosses it with every good or bad minute, each unfrozen minute
lets a deploy through, and whether `kubectl apply` succeeds would depend on which minute it ran in.
The budget also can't always be read: Prometheus can be down, or a service can have no traffic.

**Decision.** `DeploysFrozen` becomes `True` (`BudgetExhausted`) when no budget is left, and stays
`True` (`BudgetRecovering`) until at least 5% is back; then it is `False` (`WithinBudget`). When the
budget can't be measured, the condition keeps its status, with reason `BudgetUnknown`. A fast burn
alone does not freeze deploys: only a used-up budget does.

**Consequences.**
- A service must earn 5% of its budget back before it can ship again, so the freeze can't lift and
  drop again within minutes.
- A Prometheus outage never freezes a healthy service (ADR-014) and never unfreezes an exhausted one.
  If a freeze outlives the data behind it, break-glass (ADR-018) is the way out.
- Freeze decisions see only Prometheus's 10 days of retention, as budgets do (ADR-014).

---

## ADR-018: The webhook rejects only image changes, and break-glass must be set in the same update

**Status:** Accepted (Day 5)

**Context.** The freeze exists to stop shipping new code to a service that is out of budget, not to
lock its claim. An emergency change still has to be possible, and it has to leave a record. But an
override annotation left on a claim mustn't switch the freeze off for every later deploy.

**Decision.**
- The webhook rejects an update that changes `spec.image` while the stored claim's `DeploysFrozen`
  is `True`. Every other change is admitted. A rollback is an image change too, so it needs
  break-glass during a freeze.
- `paved.dev/break-glass: "<reason>"` lets the change through only when the same update adds the
  annotation or changes its value. A blank reason counts as none.
- A break-glass change is admitted with a warning and recorded as a `Warning` `BreakGlassUsed`
  Event on the claim, reported by `paved-webhook`, carrying the requesting user and the reason. Dry
  runs record nothing, which is what `sideEffects: NoneOnDryRun` promises the API server.
- The rejection is one line: `deploys frozen: url-shortener has 0% error budget remaining in a 28d
  window (burn rate 14.2x). Override with annotation paved.dev/break-glass="<reason>" — this is
  audited.` The numbers come from the claim's status, and read `unknown` when it has none.
- `failurePolicy: Fail`.

**Why break-glass rather than a hard block.** A freeze with no way through turns a bad week into an
outage. The release that fixes the errors is itself an image change, and so is rolling back a bad
one: a hard block would reject both, while the budget kept burning. The people on call often know a
change is safe when the numbers can't. So the webhook doesn't try to prevent every override. It
makes each one deliberate and visible instead: the reason must be set in the same update, so it
can't be left on by accident, and every use records who and why in a `BreakGlassUsed` Event. On
Day 5 that is how the freeze demo shipped its hotfix, while the release it was waiting for stayed
rejected.

**Consequences.**
- While the webhook can't answer, no ServiceClaim can be created or updated, including the
  finalizer the controller adds to a new claim. Status writes are not affected. With the controller
  scaled to zero, `kubectl annotate` on a claim failed with `failed calling webhook`.
- The webhook trusts `DeploysFrozen` as the controller last wrote it. If the controller is down, a
  freeze stays in force until it comes back, or someone breaks glass.
- The Event is recorded when the webhook admits the change. If something later in the request fails
  it, such as another webhook or a conflict, the Event describes a change that didn't land.
- Each override needs its own reason. Removing the annotation afterwards is not an image change, so it
  is always allowed.
- Every use is its own Event. client-go's events recorder folds Events together only when they are
  about the same object at the same `resourceVersion`, and every admitted update changes it.

---

## ADR-019: Ready means every resource is applied and the Rollout is healthy

**Status:** Accepted (Day 5)

**Context.** Until Day 5, `Ready` was a placeholder that was always `False`. The claim already has
separate conditions for its resources (`ResourcesSynced`), its SLO (`SLOHealthy`) and its deploy
policy (`DeploysFrozen`). `Ready` should answer a different question: is the service running as
claimed?

**Decision.** `Ready` is `True` when `ResourcesSynced` is `True` and Argo Rollouts reports the
Rollout `Healthy` at its current generation (`status.observedGeneration` equals
`metadata.generation`). Otherwise it is `False`, with reason `RolloutProgressing` (including canary
pauses), `RolloutDegraded`, `RolloutNotFound`, or the reason from `ResourcesSynced`. The controller
reads the Rollout without the cache, right after applying it, because a cached copy from before the
apply would still report the previous generation as healthy.

**Consequences.**
- `Ready` is `False` for the whole of every canary rollout.
- A service can be `Ready` while it is out of budget or frozen: those are separate conditions.
- Each reconcile makes one more uncached GET.

---

## ADR-020: Services create their request series at zero when they start

**Status:** Accepted (Day 5)

**Context.** On Day 4, a claim's first 40 errors never reached its budget. Each pod created its
`code="500"` series with the first failure, and Prometheus's `rate()` counts increases between
samples, not the value a series first appears with. Every pod start has that gap, and a busy
service that restarts often would look more reliable than it is.

**Decision.** The service contract in the README asks every service to create its request series,
for every status code it returns, at zero before serving. `examples/testsvc` does this for
`code="200"` and `code="500"` in both `http_requests_total` and `http_request_duration_seconds`,
from image 0.1.1.

**Consequences.**
- The first failure after a pod start counts. Measured on Day 5: before any traffic, Prometheus
  already had `http_requests_total{code="500"} 0` for each url-shortener pod.
- Each pod exposes a few more series.
- A service that returns other codes must create those series too, or the first of each will be
  missed. The rules can't fix this: the missing increase never reaches Prometheus.

---

## ADR-021: CI scans the operator image before anything can publish it

**Status:** Accepted (Day 6)

**Context.** Argo CD deploys whatever image tag git names, so the pipeline that builds that image is
the platform's front door. A vulnerable dependency is easy to miss: on Day 6 a local scan found two
HIGH CVEs in `google.golang.org/grpc` 1.82.1 inside the controller binary, and nothing had flagged
them. The scanner itself is a risk too: in March 2026 most of `aquasecurity/trivy-action`'s version
tags were force-pushed to code that stole CI secrets (CVE-2026-33634).

**Decision.**
- `ci.yaml` replaces `test.yml` and runs, in order: `go vet`, `go test` for the packages that need no
  API server, envtest (`make test`), `docker build`, a Trivy scan, and the push.
- Trivy fails the run on any HIGH or CRITICAL vulnerability that has a fix. Unfixed ones are shown in
  the report but don't fail it, because nothing in this repository could resolve them.
- Only a push to main publishes, as `ghcr.io/singha105/paved:sha-<commit>` and `:main`. The pushed
  image is the one that was scanned.
- Every action is pinned to a full commit SHA; trivy-action is v0.36.0, released after the
  compromise, with its own setup-trivy dependency also pinned by SHA.
- The GHCR token reaches `docker login` through the environment and stdin, never interpolated into
  a command.
- `google.golang.org/grpc` moves to 1.83.2, which fixes both CVEs.
- The branch `demo/trivy-catch` keeps a commit that builds on `alpine:3.14.0`, so the scan's failure
  can be seen. It is never merged.

**Consequences.**
- A new CVE with a fix breaks main's next run until the dependency or base image is updated. That is
  intended.
- Branches and pull requests are scanned but publish nothing.
- Updating a pinned action means looking up and checking its commit SHA by hand.

---

## ADR-022: The canary is gated on the same SLI recording rule as the alerts and the freeze

**Status:** Accepted (Day 6)

**Context.** Before Day 6 a Rollout moved through its canary steps on a timer. A release that failed
every request would reach 100% as long as its pods passed their probes. The platform already
measures each service's error ratio with recording rules that the burn-rate alerts (ADR-013) and the
deploy freeze (ADR-017) read.

**Decision.**
- Every public and internal claim gets an AnalysisTemplate, `<name>-canary`, that queries the claim's
  own 5m recording rule, `sli:http_availability:error_ratio_rate5m{service="<name>"}` (or the
  `http_latency` one), every 30s.
- A measurement fails when `len(result) > 0 && result[0] > 0.05`, and the first failure fails the
  analysis. Argo Rollouts hands a Prometheus vector to the condition as a list, and an empty result,
  from a service with no traffic, passes, as in ADR-014.
- The Rollout runs it as background analysis from step 1, the first pause, once 20% of the replicas
  run the new version, until the rollout completes. A failed analysis aborts the rollout: the canary
  scales down and the stable version keeps serving.
- The canary pauses grow from 30s to 2m. A bad version needs a scrape (30s), a rule evaluation (30s)
  and a measurement (30s) before the analysis can see it, and a 30s pause would promote it first.
- Batch claims get no analysis, since they have no traffic to measure.

**Consequences.**
- One SLI now has three jobs: it pages people (the burn-rate alerts), it stops shipping when the
  budget is gone (the freeze), and it stops a bad release mid-rollout (this analysis). A change to how
  a service is measured changes all three together.
- Every canary takes at least 4 minutes.
- Tiers are 13, 12 and 8 objects.
- A Prometheus outage behaves differently here than in ADR-014. Query errors count against Argo
  Rollouts' default `consecutiveErrorLimit` of 4, so an outage of about two minutes during a canary
  aborts it. That is left at the default for now.
- Changing the builders changes every claim's managed objects when a new controller version starts,
  and drift detection (ADR-015) records those updates as `DriftCorrected` Events.

---

## ADR-023: Argo CD delivers the operator and the claims from git, as an app-of-apps

**Status:** Accepted (Day 6)

**Context.** Until Day 6 the operator reached the cluster through `make deploy` and the claims through
`kubectl apply`, so what ran was whatever someone last applied from their machine. The platform
should be delivered the way it delivers services: declared in git, applied by a controller, with
drift put back.

**Decision.**
- `hack/cluster-up.sh` installs Argo CD (chart 10.9.0, v3.5.2, with dex and notifications off) and
  applies one Application by hand: `deploy/argocd/root.yaml`. It syncs `deploy/argocd/apps`.
- `paved-operator` renders `deploy/operator`: `config/default` with the image pinned to the GHCR tag
  CI pushed. A new operator version is a commit that changes that tag. It runs in sync wave 0.
- `platform-claims` applies `deploy/claims`: the namespace and every ServiceClaim. It runs in sync
  wave 1.
- Both use automated sync with prune and self-heal, server-side apply, and retries with backoff.
  cert-manager's CA bundle in the webhook configuration is ignored.
- The operator image lives on GHCR and is public, so the cluster needs no pull credentials.
- The cluster can't receive GitHub webhooks, so the demos annotate the claims app with
  `argocd.argoproj.io/refresh` to make it look at git immediately. Otherwise Argo CD polls.

**Consequences.**
- A claim changes by commit. A `kubectl edit` is undone by self-heal, which is why `freeze-demo.sh`
  pauses automated sync while it patches the claim, and restores it on exit.
- The freeze gates GitOps too. An image change synced while a claim is frozen is rejected by the
  webhook, and the sync retries until the budget recovers or the commit adds a break-glass reason.
- Rolling back a bad release means reverting its commit. Argo Rollouts already stopped the canary
  (ADR-022), but git keeps asking for the bad image until the revert.
- `make deploy` still works, but Argo CD replaces anything it applies with what git says.

---

## ADR-024: paved is an operator, not a Helm chart

**Status:** Accepted (Day 7, recording a choice made on Day 1)

**Context.** A Helm chart could render the same objects from a values file: a Rollout, an HPA, a
NetworkPolicy, alert rules, a dashboard. Charts are familiar, and they cost far less code than a
controller. But the platform has to do things a chart can't do: keep the objects as they were
declared after they are created, report how each service is doing, and let live SLO data decide
whether a release may ship.

**Decision.** paved is a controller behind its own API, `ServiceClaim`:
- **It keeps objects the way they were declared.** A chart renders once, at install or upgrade.
  The controller sees a deleted or edited object and puts it back: on Day 7 a deleted
  PrometheusRule was back 100 to 257 ms after `kubectl delete` returned (README).
- **It reports.** The claim's status carries the error budget, the burn rate, whether deploys are
  frozen and whether the service is ready. A chart has no runtime state to report.
- **Its policy runs on live data.** The freeze needs the budget from Prometheus, re-read every
  minute, and an admission webhook that reads it when someone changes an image. A chart only knows
  its values at render time.
- **The API is the boundary.** A developer can set only what the CRD has fields for. Limits, probes,
  security context and canary steps don't exist in the schema, rather than being values teams are
  asked not to override (ADR-001).
- **Derived values are code with tests.** Tier floors, exact decimal alert thresholds and budget maths
  live in Go with unit tests, not in template arithmetic.

**Consequences.**
- There is more to own: Go code, a CRD to version, an envtest suite, and the operator's own delivery
  (ADR-023).
- A bug in the controller reaches every service at once. The tests, fail-open (ADR-014) and the canary
  gate on the operator's own consumers limit the damage, but don't remove it.
- Helm is still used where rendering once is right: `hack/cluster-up.sh` installs cert-manager,
  Traefik, Argo Rollouts, kube-prometheus-stack and Argo CD with it.

## ADR-025: Claims get storage through the cluster's own identity, with no AWS key anywhere

**Status:** Accepted (Day 8, optional)

**Context.** Day 8 asked for an S3 bucket and a scoped IAM role for each claim with
`spec.storage: true`, with no long-lived credentials anywhere. The spec proposed moving the cluster
to an Oracle Cloud always-free ARM instance with a public OIDC issuer, federated to AWS IAM. There
was no Oracle Cloud account to use, so the cluster stays on k3d. AWS doesn't need a public cluster
to trust one, only a public issuer: the discovery document and the keys that verify the cluster's
tokens.

**Decision.**
- **Federation, not keys.**
  - The k3s API server issues service-account tokens as
    `https://<issuer bucket>.s3.<region>.amazonaws.com`.
  - The discovery document and the signing keys (JWKS) are the only public objects in that bucket.
  - An IAM OIDC provider trusts that issuer. IAM reads the discovery document when it creates the
    provider, before any cluster exists, so Terraform first uploads placeholders that name the issuer
    with an empty key set. cluster-up overwrites both.
  - A pod hands AWS STS a projected token with the audience `sts.amazonaws.com`
    (`AssumeRoleWithWebIdentity`) and gets a session that expires.
- **The cluster stays private.** Only the two documents are public, and the API server isn't
  reachable from the internet. This deviates from the spec: without the Oracle move, the setup is a
  local cluster federated to AWS, not a cross-cloud one.
- **ACK makes the AWS calls.**
  - paved writes an ACK `Role` and `Bucket` for each claim with server-side apply, like every other
    object it manages (ADR-002).
  - ACK's IAM and S3 controllers create them in AWS and correct them there.
  - The ACK controllers use the same federation, and paved itself holds no AWS permissions.
- **Scoped at every layer.**
  - A claim's role trusts only `system:serviceaccount:svc-<name>:<name>` with the STS audience, and
    its one inline policy names only its own bucket.
  - A permissions boundary caps every such role to objects in `paved-*` buckets, and denies the
    issuer bucket.
  - The IAM controller can create roles only under `/paved/workloads/`, only with that boundary,
    and can't remove the boundary.
  - The S3 controller can change the settings of `paved-*` buckets. It is denied their object data
    and the issuer bucket.
- **Data outlives YAML.**
  - Deleting a claim deletes its role but keeps its bucket (`services.k8s.aws/deletion-policy:
    retain`).
  - Both objects are `adopt-or-create`, so a rebuilt cluster takes back what an earlier one made.
  - `spec.storage` can't change after creation, because paved never deletes objects it stops
    building. Turning it off would leave a live role and bucket behind with no warning.
- **Keys rotate with the cluster.** Every cluster-up publishes the new cluster's keys over the old
  ones, so tokens signed by a deleted cluster stop verifying.
- **Short-lived on the laptop too.** `hack/aws-up.sh` refuses a profile that stores an access key,
  and runs on an `aws login` session instead.

**Consequences.**
- There are two more controllers to run and keep current (pinned in PROGRESS.md). A storage claim
  is Ready only once ACK reports both objects synced with AWS.
- The IAM controller can write any trust policy on the roles it manages. The boundary limits what
  such a role can do (objects in paved buckets), not who can assume it.
- Deleting the cluster without deleting storage claims first leaves their roles in AWS, until a
  rebuilt cluster adopts them or someone deletes them.
- Kept buckets cost storage until their owner deletes them. `make aws-down` doesn't touch them.
- Storage needs the AWS link. Without it, `make demo` works as before, and a storage claim reports
  `StorageUnavailable`.
