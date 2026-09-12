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
