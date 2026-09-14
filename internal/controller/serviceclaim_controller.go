/*
Copyright 2026 Arnab Singh.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
	"github.com/singha105/paved/internal/slo"
)

const (
	// FieldOwner is the server-side apply field manager for every write the controller makes.
	FieldOwner = client.FieldOwner("paved-controller")

	// Finalizer keeps a deleted claim until its namespace, and everything in it, is gone.
	Finalizer = "paved.dev/finalizer"

	// Condition reasons.
	ReasonApplied     = "Applied"
	ReasonApplyFailed = "ApplyFailed"
	ReasonInvalidSpec = "InvalidSpec"

	claimKind = "ServiceClaim"

	// namespaceDeletionPoll is how often a deleted claim rechecks its namespace, in case the
	// namespace's own deletion event is missed.
	namespaceDeletionPoll = 5 * time.Second
)

// ServiceClaimReconciler reconciles a ServiceClaim object
type ServiceClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Prometheus answers the SLO queries. When it is nil or unreachable, SLOHealthy is Unknown
	// and resources are still reconciled (DECISIONS.md, ADR-014).
	Prometheus slo.Querier
	// APIReader reads managed objects straight from the API server, to tell a drift correction
	// from an apply that changed nothing. When nil, drift isn't reported.
	APIReader client.Reader
	// Recorder records DriftCorrected Events. When nil, corrections are only logged.
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;serviceaccounts;services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies;ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=rollouts;analysistemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;prometheusrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile applies every object a ServiceClaim's tier requires, reports any it had to put
// back, reads the claim's error budget from Prometheus, decides whether its deploys are frozen,
// and records all of it in the claim's status. It requeues every minute to keep the budget current. A deleted claim is finalized
// instead: its namespace is deleted, and the finalizer is released once the namespace is gone.
func (r *ServiceClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var claim platformv1alpha1.ServiceClaim
	if err := r.Get(ctx, req.NamespacedName, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			// Deleted after the event was queued: nothing is left to reconcile.
			log.Info("ServiceClaim not found, skipping")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting ServiceClaim %s: %w", req.NamespacedName, err)
	}

	if !claim.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &claim)
	}

	if !controllerutil.ContainsFinalizer(&claim, Finalizer) {
		if err := r.applyFinalizer(ctx, &claim, true); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Added finalizer", "finalizer", Finalizer)
	}

	// The logger from controller-runtime already carries the claim's name and namespace.
	log.Info("Reconciling ServiceClaim", "tier", claim.Spec.Tier, "generation", claim.Generation)

	objects, err := builders.Build(&claim)
	if err != nil {
		// The spec passed API validation but can't become resources, for example an
		// unparseable latencyThreshold. Retrying won't help until the claim changes.
		log.Info("ServiceClaim spec cannot be built", "reason", err.Error())
		invalid := metav1.Condition{
			Type:               platformv1alpha1.ConditionResourcesSynced,
			Status:             metav1.ConditionFalse,
			Reason:             ReasonInvalidSpec,
			Message:            err.Error(),
			ObservedGeneration: claim.Generation,
		}
		report := sloReport{condition: metav1.Condition{
			Type:               platformv1alpha1.ConditionSLOHealthy,
			Status:             metav1.ConditionUnknown,
			Reason:             ReasonInvalidSpec,
			Message:            "The SLO can't be evaluated until the spec is valid",
			ObservedGeneration: claim.Generation,
		}}
		return ctrl.Result{}, r.applyStatus(ctx, &claim, 0, report,
			invalid, report.condition, freezeCondition(&claim, report), readyCondition(&claim, invalid, nil, nil))
	}

	wasInSync := inSyncAtGeneration(&claim)
	applied, corrections, applyErr := r.applyObjects(ctx, objects)
	if wasInSync {
		r.reportDrift(ctx, &claim, corrections)
	}

	synced := metav1.Condition{
		Type:               platformv1alpha1.ConditionResourcesSynced,
		Status:             metav1.ConditionTrue,
		Reason:             ReasonApplied,
		Message:            fmt.Sprintf("Applied %d of %d managed resources", applied, len(objects)),
		ObservedGeneration: claim.Generation,
	}
	if applyErr != nil {
		synced.Status = metav1.ConditionFalse
		synced.Reason = ReasonApplyFailed
		synced.Message = fmt.Sprintf("Applied %d of %d managed resources: %v", applied, len(objects), applyErr)
	}

	report := evaluateSLO(ctx, r.Prometheus, &claim)
	if report.condition.Reason == ReasonPrometheusUnavailable {
		log.Info("Could not evaluate SLO, failing open", "reason", report.condition.Message)
	}
	frozen := freezeCondition(&claim, report)
	previous := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionDeploysFrozen)
	if previous == nil || previous.Status != frozen.Status {
		log.Info("Deploy freeze changed", "frozen", frozen.Status, "reason", frozen.Reason, "message", frozen.Message)
	}

	rollout := &rolloutsv1alpha1.Rollout{}
	var rolloutErr error
	if applyErr == nil {
		// Read past the cache: the Rollout was just applied, and a cached copy from before the
		// apply would still report the previous generation as healthy.
		reader := client.Reader(r.Client)
		if r.APIReader != nil {
			reader = r.APIReader
		}
		key := types.NamespacedName{Namespace: builders.NamespaceName(&claim), Name: claim.Name}
		if err := reader.Get(ctx, key, rollout); err != nil {
			rolloutErr = fmt.Errorf("getting Rollout %s: %w", key, err)
		}
	}
	ready := readyCondition(&claim, synced, rollout, rolloutErr)
	if apierrors.IsNotFound(rolloutErr) {
		// Only a cache that hasn't caught up with the apply can miss it; its watch requeues the claim.
		rolloutErr = nil
	}

	if err := r.applyStatus(ctx, &claim, applied, report, synced, report.condition, frozen, ready); err != nil {
		return ctrl.Result{}, errors.Join(applyErr, rolloutErr, err)
	}
	if err := errors.Join(applyErr, rolloutErr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: sloRefreshInterval}, nil
}

// applyObjects server-side applies objects in order and stops at the first failure, because
// every later object lives in the Namespace applied first. It returns how many were applied
// and which objects the applies had to recreate or put back (DECISIONS.md, ADR-015).
func (r *ServiceClaimReconciler) applyObjects(ctx context.Context, objects []client.Object) (int, []correction, error) {
	var corrections []correction
	for i, obj := range objects {
		u, err := toApplyObject(obj)
		if err != nil {
			return i, corrections, err
		}
		before, existed, err := r.liveOwnership(ctx, u)
		if err != nil {
			return i, corrections, err
		}
		// The apply writes the server's response back into u.
		if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), FieldOwner, client.ForceOwnership); err != nil {
			return i, corrections, fmt.Errorf("applying %s %s: %w", u.GetKind(), client.ObjectKeyFromObject(obj), err)
		}
		if r.APIReader != nil && (!existed || !sameOwnership(before, controllerOwnership(u))) {
			corrections = append(corrections, correction{kind: u.GetKind(), ref: objectRef(u), recreated: !existed})
		}
	}
	return len(objects), corrections, nil
}

// finalize deletes the claim's namespace, which removes every managed object inside it, and
// releases the finalizer once the namespace is gone. A namespace that is not labelled as
// this claim's is never deleted.
func (r *ServiceClaimReconciler) finalize(ctx context.Context, claim *platformv1alpha1.ServiceClaim) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(claim, Finalizer) {
		return ctrl.Result{}, nil
	}

	var ns corev1.Namespace
	err := r.Get(ctx, types.NamespacedName{Name: builders.NamespaceName(claim)}, &ns)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.applyFinalizer(ctx, claim, false); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Removed finalizer after namespace was deleted", "namespace", builders.NamespaceName(claim))
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("getting namespace %s: %w", builders.NamespaceName(claim), err)
	}

	if !belongsTo(&ns, claim) {
		log.Info("Namespace belongs to another claim, leaving it in place", "namespace", ns.Name)
		return ctrl.Result{}, r.applyFinalizer(ctx, claim, false)
	}
	if ns.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, &ns); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("deleting namespace %s: %w", ns.Name, err)
		}
		log.Info("Deleted namespace", "namespace", ns.Name)
	}
	return ctrl.Result{RequeueAfter: namespaceDeletionPoll}, nil
}

// applyFinalizer adds or removes the controller's finalizer with server-side apply. The
// request carries only metadata, so the controller never takes ownership of the claim's spec.
func (r *ServiceClaimReconciler) applyFinalizer(ctx context.Context, claim *platformv1alpha1.ServiceClaim, present bool) error {
	metadata := map[string]any{"name": claim.Name, "namespace": claim.Namespace}
	if present {
		metadata["finalizers"] = []any{Finalizer}
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": platformv1alpha1.GroupVersion.String(),
		"kind":       claimKind,
		"metadata":   metadata,
	}}
	if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), FieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("setting finalizer present=%t on ServiceClaim %s: %w", present, client.ObjectKeyFromObject(claim), err)
	}
	return nil
}

// applyStatus writes the status fields the controller owns with server-side apply. The
// request is built from scratch rather than from the fetched claim, so it carries only
// those fields; a field left out, such as an unknown error budget, is cleared.
func (r *ServiceClaimReconciler) applyStatus(
	ctx context.Context, claim *platformv1alpha1.ServiceClaim, managed int, report sloReport, set ...metav1.Condition,
) error {
	conditions := slices.Clone(claim.Status.Conditions)
	for _, condition := range set {
		meta.SetStatusCondition(&conditions, condition)
	}

	conds := make([]any, 0, len(conditions))
	for i := range conditions {
		c, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&conditions[i])
		if err != nil {
			return fmt.Errorf("converting condition %q: %w", conditions[i].Type, err)
		}
		conds = append(conds, c)
	}

	status := map[string]any{
		"conditions":         conds,
		"observedGeneration": claim.Generation,
		"lastReconcileTime":  metav1.Now().UTC().Format(time.RFC3339),
	}
	if managed > 0 {
		status["managedResources"] = int64(managed)
	}
	if report.budgetRemaining != "" {
		status["errorBudgetRemaining"] = report.budgetRemaining
	}
	if report.burnRate1h != "" {
		status["burnRate1h"] = report.burnRate1h
	}

	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": platformv1alpha1.GroupVersion.String(),
		"kind":       claimKind,
		"metadata":   map[string]any{"name": claim.Name, "namespace": claim.Namespace},
		"status":     status,
	}}
	if err := r.Status().Apply(ctx, client.ApplyConfigurationFromUnstructured(u), FieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("applying status to ServiceClaim %s: %w", client.ObjectKeyFromObject(claim), err)
	}
	return nil
}

// SetupWithManager watches ServiceClaims and every kind of object they produce. Managed
// objects have no owner references (DECISIONS.md, ADR-006), so each watch maps an object back
// to its claim through the claim labels instead of using Owns().
func (r *ServiceClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ServiceClaim{}, builder.WithPredicates(claimChanged())).
		Named("serviceclaim")
	for _, obj := range builders.ManagedTypes() {
		b = b.Watches(obj, handler.EnqueueRequestsFromMapFunc(claimForObject))
	}
	return b.Complete(r)
}

// claimChanged admits the claim events that can change what should be applied: creation,
// spec changes (a new generation), label or annotation changes, and deletion. It drops
// status-only updates, including the controller's own lastReconcileTime writes, which would
// otherwise requeue the claim forever (DECISIONS.md, ADR-010).
func claimChanged() predicate.Predicate {
	return predicate.Or[client.Object](
		predicate.GenerationChangedPredicate{},
		predicate.LabelChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
		predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				return !e.ObjectNew.GetDeletionTimestamp().IsZero()
			},
		},
	)
}

// claimForObject maps a managed object to its claim through the labels every builder sets.
func claimForObject(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()[builders.LabelClaim]
	namespace := obj.GetLabels()[builders.LabelClaimNamespace]
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
}

// belongsTo reports whether obj is labelled as managed for claim.
func belongsTo(obj client.Object, claim *platformv1alpha1.ServiceClaim) bool {
	labels := obj.GetLabels()
	return labels[builders.LabelClaim] == claim.Name && labels[builders.LabelClaimNamespace] == claim.Namespace
}
