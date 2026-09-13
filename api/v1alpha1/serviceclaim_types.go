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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types reported in ServiceClaimStatus.Conditions.
const (
	ConditionResourcesSynced = "ResourcesSynced"
	ConditionSLOHealthy      = "SLOHealthy"
	ConditionDeploysFrozen   = "DeploysFrozen"
	ConditionReady           = "Ready"
)

// SLI types a ServiceClaim can request.
const (
	SLIHTTPAvailability = "http-availability"
	SLIHTTPLatency      = "http-latency"
)

// Tiers a ServiceClaim can request. Each tier maps to a fixed set of managed resources.
const (
	TierPublic   = "public"
	TierInternal = "internal"
	TierBatch    = "batch"
)

// ServiceClaimSpec is everything a developer declares about a service. Resource limits,
// security context, rollout strategy, probes and canary steps are deliberately absent:
// the platform owns them (DECISIONS.md, ADR-001).
type ServiceClaimSpec struct {
	// +kubebuilder:validation:MinLength=1
	Owner string `json:"owner"`

	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// +kubebuilder:validation:Enum=public;internal;batch
	Tier string `json:"tier"`

	SLI   SLISpec   `json:"sli"`
	SLO   SLOSpec   `json:"slo"`
	Scale ScaleSpec `json:"scale"`
}

// SLISpec selects how "good" requests are measured.
type SLISpec struct {
	// +kubebuilder:validation:Enum=http-availability;http-latency
	Type string `json:"type"`
	// Statuses counted as "good". Used by http-availability.
	// +kubebuilder:default={200,201,204,301,302,304,400,404}
	GoodStatuses []int32 `json:"goodStatuses,omitempty"`
	// Latency threshold for http-latency, e.g. "250ms".
	// +kubebuilder:default="250ms"
	LatencyThreshold string `json:"latencyThreshold,omitempty"`
}

// SLOSpec is the objective the error budget is computed from.
type SLOSpec struct {
	// Target availability percentage, e.g. 99.5. A string, not a float, per Kubernetes
	// API conventions; the range is checked by CEL because Minimum/Maximum only apply
	// to numeric schema types (DECISIONS.md, ADR-004).
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +kubebuilder:validation:XValidation:rule="double(self) >= 90.0 && double(self) < 100.0",message="objective must be at least 90 and less than 100"
	Objective string `json:"objective"` // resource.Quantity-style string, parse it
	// +kubebuilder:validation:Enum=7d;28d;30d
	// +kubebuilder:default="28d"
	Window string `json:"window,omitempty"`
}

// ScaleSpec bounds replica autoscaling. The tier may raise the minimum.
type ScaleSpec struct {
	// +kubebuilder:validation:Minimum=1
	Min int32 `json:"min"`
	// +kubebuilder:validation:Minimum=1
	Max int32 `json:"max"`
}

// ServiceClaimStatus is what the controller last observed and decided.
type ServiceClaimStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions           []metav1.Condition `json:"conditions,omitempty"`
	ErrorBudgetRemaining string             `json:"errorBudgetRemaining,omitempty"` // "62%"
	BurnRate1h           string             `json:"burnRate1h,omitempty"`
	ManagedResources     int                `json:"managedResources,omitempty"`
	ObservedGeneration   int64              `json:"observedGeneration,omitempty"`
	LastReconcileTime    *metav1.Time       `json:"lastReconcileTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.tier`
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=`.spec.owner`
// +kubebuilder:printcolumn:name="Budget",type=string,JSONPath=`.status.errorBudgetRemaining`
// +kubebuilder:printcolumn:name="Frozen",type=string,JSONPath=`.status.conditions[?(@.type=="DeploysFrozen")].status`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceClaim is one developer's request for a production-shaped service. The
// controller reconciles it into its own namespace, svc-<name>.
type ServiceClaim struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ServiceClaim
	// +required
	Spec ServiceClaimSpec `json:"spec"`

	// status defines the observed state of ServiceClaim
	// +optional
	Status ServiceClaimStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServiceClaimList contains a list of ServiceClaim
type ServiceClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ServiceClaim `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ServiceClaim{}, &ServiceClaimList{})
		return nil
	})
}
