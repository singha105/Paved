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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// FieldOwner is the server-side apply field manager for every write the controller makes.
const FieldOwner = client.FieldOwner("paved-controller")

// ReasonNotImplemented marks the Ready condition until managed resources are reconciled.
const ReasonNotImplemented = "NotImplemented"

// ServiceClaimReconciler reconciles a ServiceClaim object
type ServiceClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.paved.dev,resources=serviceclaims/finalizers,verbs=update

// Reconcile moves the cluster toward the state a ServiceClaim declares. For now it only
// records that the claim was seen, with Ready=False and reason NotImplemented.
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

	// The logger from controller-runtime already carries the claim's name and namespace.
	log.Info("Reconciling ServiceClaim", "tier", claim.Spec.Tier, "generation", claim.Generation)

	conditions := claim.Status.Conditions
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:               platformv1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             ReasonNotImplemented,
		Message:            "Managed resources are not reconciled yet",
		ObservedGeneration: claim.Generation,
	})
	if err := r.applyStatus(ctx, &claim, conditions); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// applyStatus writes the status fields the controller owns with server-side apply.
// The request is built from scratch, not from the fetched object, so it carries only
// those fields. An apply that changes nothing is not persisted, so the status write
// does not retrigger reconciles once the condition is stable.
func (r *ServiceClaimReconciler) applyStatus(ctx context.Context, claim *platformv1alpha1.ServiceClaim, conditions []metav1.Condition) error {
	conds := make([]any, 0, len(conditions))
	for i := range conditions {
		c, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&conditions[i])
		if err != nil {
			return fmt.Errorf("converting condition %q: %w", conditions[i].Type, err)
		}
		conds = append(conds, c)
	}

	status := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": platformv1alpha1.GroupVersion.String(),
		"kind":       "ServiceClaim",
		"metadata": map[string]any{
			"name":      claim.Name,
			"namespace": claim.Namespace,
		},
		"status": map[string]any{
			"conditions": conds,
		},
	}}

	if err := r.Status().Apply(ctx, client.ApplyConfigurationFromUnstructured(status), FieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("applying status to ServiceClaim %s/%s: %w", claim.Namespace, claim.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ServiceClaim{}).
		Named("serviceclaim").
		Complete(r)
}
