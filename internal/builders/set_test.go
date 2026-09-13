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
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

var baseKinds = []string{
	"Namespace", "ServiceAccount", "Rollout", "NetworkPolicy",
	"PrometheusRule", "ServiceMonitor", "ConfigMap", "ConfigMap",
}

// mustBuild returns Build's objects for sc, failing the test if Build returns an error.
func mustBuild(t *testing.T, sc *platformv1alpha1.ServiceClaim) []client.Object {
	t.Helper()
	objects, err := Build(sc)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return objects
}

func TestBuildResourceCountPerTier(t *testing.T) {
	tests := []struct {
		tier string
		want int
	}{
		{platformv1alpha1.TierPublic, 12},
		{platformv1alpha1.TierInternal, 11},
		{platformv1alpha1.TierBatch, 8},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			if got := len(mustBuild(t, newClaim(tt.tier))); got != tt.want {
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
			for _, obj := range mustBuild(t, newClaim(tt.tier)) {
				got = append(got, obj.GetObjectKind().GroupVersionKind().Kind)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("kinds = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildBatchHasNoService(t *testing.T) {
	for _, obj := range mustBuild(t, newClaim(platformv1alpha1.TierBatch)) {
		if _, isService := obj.(*corev1.Service); isService {
			t.Fatalf("batch claim produced a Service: %s", obj.GetName())
		}
	}
}

func TestBuildObjectsAreLabelledAndPlaced(t *testing.T) {
	for _, tier := range []string{platformv1alpha1.TierPublic, platformv1alpha1.TierInternal, platformv1alpha1.TierBatch} {
		t.Run(tier, func(t *testing.T) {
			sc := newClaim(tier)
			objects := mustBuild(t, sc)

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

func TestBuildRejectsSLIsThatCannotBecomeRules(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	sc.Spec.SLI.Type = platformv1alpha1.SLIHTTPLatency
	sc.Spec.SLI.LatencyThreshold = "soon"

	if objects, err := Build(sc); err == nil {
		t.Errorf("Build() returned %d objects and no error for latencyThreshold %q", len(objects), "soon")
	}
}

func TestManagedTypesMatchBuild(t *testing.T) {
	types := ManagedTypes()
	managed := make([]reflect.Type, 0, len(types))
	for _, obj := range types {
		managed = append(managed, reflect.TypeOf(obj))
	}

	seen := map[reflect.Type]bool{}
	for _, obj := range mustBuild(t, newClaim(platformv1alpha1.TierPublic)) {
		if !slices.Contains(managed, reflect.TypeOf(obj)) {
			t.Errorf("Build() produces %T, which ManagedTypes() does not list", obj)
		}
		seen[reflect.TypeOf(obj)] = true
	}
	for _, typ := range managed {
		if !seen[typ] {
			t.Errorf("ManagedTypes() lists %v, which a public Build() never produces", typ)
		}
	}
}
