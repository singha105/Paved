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
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	// ReasonDriftCorrected is the reason of the Event recorded for each object put back.
	ReasonDriftCorrected = "DriftCorrected"

	eventActionReconcile = "Reconcile"
)

// correction is a managed object the controller had to put back.
type correction struct {
	kind      string
	ref       string
	recreated bool
}

// liveOwnership reads obj as the API server has it now, bypassing the cache, and returns the
// controller's server-side apply ownership entry for it. existed is false when there is no such
// object. With no APIReader configured, drift isn't tracked and it reports the object as existing.
func (r *ServiceClaimReconciler) liveOwnership(
	ctx context.Context, obj *unstructured.Unstructured,
) (entry *metav1.ManagedFieldsEntry, existed bool, err error) {
	if r.APIReader == nil {
		return nil, true, nil
	}
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(obj.GroupVersionKind())
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), live); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading %s %s: %w", obj.GetKind(), objectRef(obj), err)
	}
	return controllerOwnership(live), true, nil
}

// controllerOwnership returns the entry that records which fields the controller's
// server-side applies own on obj, and when they last changed, or nil if there is none.
func controllerOwnership(obj client.Object) *metav1.ManagedFieldsEntry {
	for _, entry := range obj.GetManagedFields() {
		if entry.Manager == string(FieldOwner) &&
			entry.Operation == metav1.ManagedFieldsOperationApply &&
			entry.Subresource == "" {
			return &entry
		}
	}
	return nil
}

// sameOwnership reports whether two ownership entries are identical. An apply that changes
// nothing leaves the entry untouched; an apply that has to take fields back from another
// manager, or reset their values, changes its fields or its timestamp (DECISIONS.md, ADR-015).
func sameOwnership(before, after *metav1.ManagedFieldsEntry) bool {
	if before == nil || after == nil {
		return before == after
	}
	var beforeFields, afterFields []byte
	if before.FieldsV1 != nil {
		beforeFields = before.FieldsV1.GetRawBytes()
	}
	if after.FieldsV1 != nil {
		afterFields = after.FieldsV1.GetRawBytes()
	}
	return before.Time.Equal(after.Time) && bytes.Equal(beforeFields, afterFields)
}

// inSyncAtGeneration reports whether the claim's resources were last applied successfully for
// its current generation. Only then does a change to a managed object count as drift, rather
// than the result of a new claim or a spec edit.
func inSyncAtGeneration(claim *platformv1alpha1.ServiceClaim) bool {
	synced := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionResourcesSynced)
	return synced != nil && synced.Status == metav1.ConditionTrue && synced.ObservedGeneration == claim.Generation
}

// reportDrift logs every correction and records a DriftCorrected Event on the claim naming the
// object that was put back.
func (r *ServiceClaimReconciler) reportDrift(ctx context.Context, claim *platformv1alpha1.ServiceClaim, corrections []correction) {
	log := logf.FromContext(ctx)
	for _, c := range corrections {
		action := "Reverted changes to"
		if c.recreated {
			action = "Recreated"
		}
		log.Info("Corrected drift in managed object", "action", action, "kind", c.kind, "object", c.ref)
		if r.Recorder != nil {
			r.Recorder.Eventf(claim, nil, corev1.EventTypeNormal, ReasonDriftCorrected, eventActionReconcile,
				"%s %s %s", action, c.kind, c.ref)
		}
	}
}

// objectRef names an object as namespace/name, or just name when it is cluster-scoped.
func objectRef(obj client.Object) string {
	if obj.GetNamespace() == "" {
		return obj.GetName()
	}
	return obj.GetNamespace() + "/" + obj.GetName()
}
