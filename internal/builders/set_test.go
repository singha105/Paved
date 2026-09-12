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

package builders

import (
	"reflect"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

var baseKinds = []string{
	"Namespace", "ServiceAccount", "Rollout", "NetworkPolicy",
	"PrometheusRule", "ServiceMonitor", "ConfigMap",
}

func TestBuildResourceCountPerTier(t *testing.T) {
	tests := []struct {
		tier string
		want int
	}{
		{platformv1alpha1.TierPublic, 11},
		{platformv1alpha1.TierInternal, 10},
		{platformv1alpha1.TierBatch, 7},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			if got := len(Build(newClaim(tt.tier))); got != tt.want {
				t.Errorf("Build() returned %d objects, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildKindsPerTier(t *testing.T) {
	tests := []struct {
		tier string
		want []string
	}{
		{platformv1alpha1.TierPublic, append(slices.Clone(baseKinds),
			"Service", "HorizontalPodAutoscaler", "PodDisruptionBudget", "Ingress")},
		{platformv1alpha1.TierInternal, append(slices.Clone(baseKinds),
			"Service", "HorizontalPodAutoscaler", "PodDisruptionBudget")},
		{platformv1alpha1.TierBatch, slices.Clone(baseKinds)},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			var got []string
			for _, obj := range Build(newClaim(tt.tier)) {
				got = append(got, obj.GetObjectKind().GroupVersionKind().Kind)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("kinds = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildBatchHasNoService(t *testing.T) {
	for _, obj := range Build(newClaim(platformv1alpha1.TierBatch)) {
		if _, isService := obj.(*corev1.Service); isService {
			t.Fatalf("batch claim produced a Service: %s", obj.GetName())
		}
	}
}

func TestBuildObjectsAreLabelledAndPlaced(t *testing.T) {
	for _, tier := range []string{platformv1alpha1.TierPublic, platformv1alpha1.TierInternal, platformv1alpha1.TierBatch} {
		t.Run(tier, func(t *testing.T) {
			sc := newClaim(tier)
			objects := Build(sc)

			if _, isNamespace := objects[0].(*corev1.Namespace); !isNamespace {
				t.Errorf("first object is %T, want the Namespace", objects[0])
			}
			for _, obj := range objects {
				gvk := obj.GetObjectKind().GroupVersionKind()
				if gvk.Kind == "" || gvk.Version == "" {
					t.Errorf("%T has no apiVersion/kind set: %v", obj, gvk)
				}

				wantNamespace := testNamespace
				if gvk.Kind == "Namespace" {
					wantNamespace = ""
				}
				if obj.GetNamespace() != wantNamespace {
					t.Errorf("%s namespace = %q, want %q", gvk.Kind, obj.GetNamespace(), wantNamespace)
				}
				for key, value := range Labels(sc) {
					if obj.GetLabels()[key] != value {
						t.Errorf("%s label %s = %q, want %q", gvk.Kind, key, obj.GetLabels()[key], value)
					}
				}
				if len(obj.GetOwnerReferences()) != 0 {
					t.Errorf("%s has owner references %v, want none", gvk.Kind, obj.GetOwnerReferences())
				}
			}
		})
	}
}

func TestManagedTypesMatchBuild(t *testing.T) {
	types := ManagedTypes()
	managed := make([]reflect.Type, 0, len(types))
	for _, obj := range types {
		managed = append(managed, reflect.TypeOf(obj))
	}

	built := Build(newClaim(platformv1alpha1.TierPublic))
	if len(built) != len(managed) {
		t.Errorf("public Build() has %d objects but ManagedTypes() has %d types", len(built), len(managed))
	}
	for _, obj := range built {
		if !slices.Contains(managed, reflect.TypeOf(obj)) {
			t.Errorf("Build() produces %T, which ManagedTypes() does not list", obj)
		}
	}
}
