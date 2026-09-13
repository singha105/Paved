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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
)

const (
	unitClaimName      = "url-shortener"
	unitClaimNamespace = "platform-claims"
)

func unitTestClaim() *platformv1alpha1.ServiceClaim {
	return &platformv1alpha1.ServiceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: unitClaimName, Namespace: unitClaimNamespace, Generation: 1},
		Spec: platformv1alpha1.ServiceClaimSpec{
			Owner: "team-links",
			Image: "paved-demo-app:0.1.0",
			Port:  8080,
			Tier:  platformv1alpha1.TierPublic,
			SLI:   platformv1alpha1.SLISpec{Type: "http-availability"},
			SLO:   platformv1alpha1.SLOSpec{Objective: "99.5", Window: "28d"},
			Scale: platformv1alpha1.ScaleSpec{Min: 2, Max: 5},
		},
	}
}

func TestToApplyObjectSendsOnlyBuilderFields(t *testing.T) {
	for _, obj := range builders.Build(unitTestClaim()) {
		u, err := toApplyObject(obj)
		if err != nil {
			t.Fatalf("toApplyObject(%T): %v", obj, err)
		}
		kind := obj.GetObjectKind().GroupVersionKind().Kind
		if u.GetAPIVersion() == "" || u.GetKind() != kind {
			t.Errorf("%s: apiVersion %q kind %q, want both set", kind, u.GetAPIVersion(), u.GetKind())
		}
		for _, path := range [][]string{
			{"metadata", "creationTimestamp"},
			{"spec", "template", "metadata", "creationTimestamp"},
			{"status"},
		} {
			if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, path...); found {
				t.Errorf("%s: %v is still set; the controller would own it", kind, path)
			}
		}
	}
}

func TestClaimForObject(t *testing.T) {
	svc := builders.BuildService(unitTestClaim())

	got := claimForObject(t.Context(), svc)
	want := types.NamespacedName{Namespace: unitClaimNamespace, Name: unitClaimName}
	if len(got) != 1 || got[0].NamespacedName != want {
		t.Errorf("claimForObject(labelled Service) = %v, want [%v]", got, want)
	}

	svc.Labels = map[string]string{builders.LabelClaim: unitClaimName}
	if got := claimForObject(t.Context(), svc); len(got) != 0 {
		t.Errorf("claimForObject(object without claim namespace label) = %v, want none", got)
	}
}

func TestClaimChanged(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*platformv1alpha1.ServiceClaim)
		want   bool
	}{
		{"status-only update is ignored", func(sc *platformv1alpha1.ServiceClaim) {
			sc.Status.LastReconcileTime = new(metav1.Now())
		}, false},
		{"new generation", func(sc *platformv1alpha1.ServiceClaim) { sc.Generation++ }, true},
		{"annotation change", func(sc *platformv1alpha1.ServiceClaim) {
			sc.Annotations = map[string]string{"kick": "1"}
		}, true},
		{"label change", func(sc *platformv1alpha1.ServiceClaim) {
			sc.Labels = map[string]string{"team": "links"}
		}, true},
		{"deletion started", func(sc *platformv1alpha1.ServiceClaim) {
			sc.DeletionTimestamp = new(metav1.Now())
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClaim := unitTestClaim()
			newClaim := unitTestClaim()
			tt.mutate(newClaim)

			got := claimChanged().Update(event.UpdateEvent{ObjectOld: oldClaim, ObjectNew: newClaim})
			if got != tt.want {
				t.Errorf("claimChanged().Update() = %t, want %t", got, tt.want)
			}
		})
	}
}
