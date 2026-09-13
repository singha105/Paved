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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

func ownershipEntry(manager string, at time.Time, fields string) metav1.ManagedFieldsEntry {
	return metav1.ManagedFieldsEntry{
		Manager:   manager,
		Operation: metav1.ManagedFieldsOperationApply,
		Time:      &metav1.Time{Time: at},
		FieldsV1:  metav1.NewFieldsV1(fields),
	}
}

func TestControllerOwnership(t *testing.T) {
	now := time.Date(2026, 9, 13, 5, 23, 8, 0, time.UTC)
	obj := &corev1.Service{}
	obj.SetManagedFields([]metav1.ManagedFieldsEntry{
		ownershipEntry("kubectl-edit", now, `{"f:spec":{}}`),
		{Manager: string(FieldOwner), Operation: metav1.ManagedFieldsOperationApply, Subresource: "status"},
		ownershipEntry(string(FieldOwner), now, `{"f:metadata":{}}`),
	})

	entry := controllerOwnership(obj)
	if entry == nil || entry.Subresource != "" || entry.FieldsV1.GetRawString() != `{"f:metadata":{}}` {
		t.Errorf("controllerOwnership = %+v, want the controller's main-resource apply entry", entry)
	}
	if got := controllerOwnership(&corev1.Service{}); got != nil {
		t.Errorf("controllerOwnership of an object the controller never applied = %+v, want nil", got)
	}
}

func TestSameOwnership(t *testing.T) {
	now := time.Date(2026, 9, 13, 5, 23, 8, 0, time.UTC)
	original := ownershipEntry(string(FieldOwner), now, `{"f:spec":{"f:policyTypes":{}}}`)
	identical := ownershipEntry(string(FieldOwner), now, `{"f:spec":{"f:policyTypes":{}}}`)
	retaken := ownershipEntry(string(FieldOwner), now, `{"f:spec":{"f:ingress":{},"f:policyTypes":{}}}`)
	rewritten := ownershipEntry(string(FieldOwner), now.Add(time.Second), `{"f:spec":{"f:policyTypes":{}}}`)

	tests := []struct {
		name          string
		before, after *metav1.ManagedFieldsEntry
		want          bool
	}{
		{"an apply that changed nothing", &original, &identical, true},
		{"fields taken back from another manager in the same second", &original, &retaken, false},
		{"values rewritten", &original, &rewritten, false},
		{"ownership lost, then regained", nil, &identical, false},
		{"never owned by the controller", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameOwnership(tt.before, tt.after); got != tt.want {
				t.Errorf("sameOwnership = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestInSyncAtGeneration(t *testing.T) {
	synced := func(status metav1.ConditionStatus, generation int64) *platformv1alpha1.ServiceClaim {
		claim := unitTestClaim()
		claim.Generation = 2
		claim.Status.Conditions = []metav1.Condition{{
			Type: platformv1alpha1.ConditionResourcesSynced, Status: status, ObservedGeneration: generation,
		}}
		return claim
	}

	if !inSyncAtGeneration(synced(metav1.ConditionTrue, 2)) {
		t.Error("a claim synced at its current generation is not reported as in sync")
	}
	if inSyncAtGeneration(synced(metav1.ConditionTrue, 1)) {
		t.Error("a claim whose spec changed since the last sync is reported as in sync")
	}
	if inSyncAtGeneration(synced(metav1.ConditionFalse, 2)) {
		t.Error("a claim whose last apply failed is reported as in sync")
	}
	if inSyncAtGeneration(unitTestClaim()) {
		t.Error("a claim that was never reconciled is reported as in sync")
	}
}

func TestObjectRef(t *testing.T) {
	const managedNamespace = "svc-url-shortener"

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: unitClaimName, Namespace: managedNamespace}}
	if got := objectRef(svc); got != managedNamespace+"/"+unitClaimName {
		t.Errorf("objectRef(Service) = %q", got)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managedNamespace}}
	if got := objectRef(ns); got != managedNamespace {
		t.Errorf("objectRef(Namespace) = %q", got)
	}
}
