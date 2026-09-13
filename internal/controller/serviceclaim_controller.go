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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
)

const (
	// FieldOwner is the server-side apply field manager for every write the controller makes.
	FieldOwner = client.FieldOwner("paved-controller")

	// Finalizer keeps a deleted claim until its namespace, and everything in it, is gone.
	Finalizer = "paved.dev/finalizer"

	// Condition reasons.
	ReasonApplied        = "Applied"
	ReasonApplyFailed    = "ApplyFailed"
	ReasonInvalidSpec    = "InvalidSpec"
	ReasonNotImplemented = "NotImplemented"

	claimKind = "ServiceClaim"

	// namespaceDeletionPoll is how often a deleted claim rechecks its namespace, in case the
	// namespace's own deletion event is missed.
	namespaceDeletionPoll = 5 * time.Second
)

// ServiceClaimReconciler reconciles a ServiceClaim object
type ServiceClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces;serviceaccounts;services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies;ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=rollouts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;prometheusrules,verbs=get;list;watch;create;update;patch;delete

// Reconcile applies every object a ServiceClaim's tier requires and reports the result in
// the claim's status. A deleted claim is finalized instead: its namespace is deleted, and the
// finalizer is released once the namespace is gone.
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
		return ctrl.Result{}, r.applyStatus(ctx, &claim, invalid, 0)
	}
	applied, applyErr := r.applyObjects(ctx, objects)

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

	if err := r.applyStatus(ctx, &claim, synced, applied); err != nil {
		return ctrl.Result{}, errors.Join(applyErr, err)
	}
	return ctrl.Result{}, applyErr
}

// applyObjects server-side applies objects in order and stops at the first failure, because
// every later object lives in the Namespace applied first. It returns how many were applied.
func (r *ServiceClaimReconciler) applyObjects(ctx context.Context, objects []client.Object) (int, error) {
	for i, obj := range objects {
		u, err := toApplyObject(obj)
		if err != nil {
			return i, err
		}
		if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), FieldOwner, client.ForceOwnership); err != nil {
			return i, fmt.Errorf("applying %s %s: %w", u.GetKind(), client.ObjectKeyFromObject(obj), err)
		}
	}
	return len(objects), nil
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
// those fields.
func (r *ServiceClaimReconciler) applyStatus(
	ctx context.Context, claim *platformv1alpha1.ServiceClaim, synced metav1.Condition, managed int,
) error {
	conditions := claim.Status.Conditions
	meta.SetStatusCondition(&conditions, synced)
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:               platformv1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             ReasonNotImplemented,
		Message:            "SLO evaluation is not implemented yet",
		ObservedGeneration: claim.Generation,
	})

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
