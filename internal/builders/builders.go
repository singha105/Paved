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

// Package builders turns a ServiceClaim into the Kubernetes objects the platform manages.
//
// Every function here is pure: it reads only the claim, makes no API calls and uses no
// clock or randomness, so its output can be unit-tested without a cluster. The settings a
// developer cannot choose (resource limits, security context, probes, canary steps) are
// constants in this package (DECISIONS.md, ADR-001).
package builders

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// Labels carried by every managed object. The two claim labels lead from a managed object
// back to its claim; owner references cannot, because the claim lives in another
// namespace (DECISIONS.md, ADR-006).
const (
	LabelName           = "app.kubernetes.io/name"
	LabelManagedBy      = "app.kubernetes.io/managed-by"
	LabelClaim          = "paved.dev/claim"
	LabelClaimNamespace = "paved.dev/claim-namespace"
	LabelOwner          = "paved.dev/owner"

	// ManagedBy is the value of LabelManagedBy on every managed object.
	ManagedBy = "paved"
)

const (
	// PortName names the container port, the Service port and the scrape endpoint.
	PortName = "http"
	// ServicePort is the port a claim's Service exposes inside the cluster.
	ServicePort int32 = 80

	namespacePrefix = "svc-"
)

// NamespaceName is the namespace a claim is reconciled into.
func NamespaceName(sc *platformv1alpha1.ServiceClaim) string {
	return namespacePrefix + sc.Name
}

// RunbookName is the name of the ConfigMap that holds a claim's runbook.
func RunbookName(sc *platformv1alpha1.ServiceClaim) string {
	return sc.Name + "-runbook"
}

// Labels returns the labels every object managed for sc carries.
func Labels(sc *platformv1alpha1.ServiceClaim) map[string]string {
	return map[string]string{
		LabelName:           sc.Name,
		LabelManagedBy:      ManagedBy,
		LabelClaim:          sc.Name,
		LabelClaimNamespace: sc.Namespace,
		LabelOwner:          sc.Spec.Owner,
	}
}

// SelectorLabels selects a claim's pods. It holds only labels that never change for the
// life of the claim, unlike the owner label.
func SelectorLabels(sc *platformv1alpha1.ServiceClaim) map[string]string {
	return map[string]string{LabelName: sc.Name}
}

// objectMeta is the metadata for an object called name in the claim's namespace.
func objectMeta(sc *platformv1alpha1.ServiceClaim, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: NamespaceName(sc),
		Labels:    Labels(sc),
	}
}
