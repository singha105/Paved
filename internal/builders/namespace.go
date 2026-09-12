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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// BuildNamespace returns the namespace every other managed object lives in. It is
// cluster-scoped, so nothing garbage-collects it: the claim's finalizer deletes it
// (DECISIONS.md, ADR-006).
func BuildNamespace(sc *platformv1alpha1.ServiceClaim) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   NamespaceName(sc),
			Labels: Labels(sc),
		},
	}
}
