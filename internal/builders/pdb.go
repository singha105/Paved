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
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// BuildPodDisruptionBudget lets voluntary disruptions such as node drains take down one of
// the claim's pods at a time. maxUnavailable is used rather than minAvailable, which would
// block every drain for a claim running a single replica (DECISIONS.md, ADR-008).
func BuildPodDisruptionBudget(sc *platformv1alpha1.ServiceClaim) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		TypeMeta:   metav1.TypeMeta{APIVersion: policyv1.SchemeGroupVersion.String(), Kind: "PodDisruptionBudget"},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: new(intstr.FromInt32(1)),
			Selector:       &metav1.LabelSelector{MatchLabels: SelectorLabels(sc)},
		},
	}
}
