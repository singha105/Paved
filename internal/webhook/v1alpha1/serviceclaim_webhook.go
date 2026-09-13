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
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// SetupServiceClaimWebhookWithManager registers the ServiceClaim validating webhook with the manager.
func SetupServiceClaimWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &platformv1alpha1.ServiceClaim{}).
		WithValidator(&ServiceClaimValidator{}).
		Complete()
}

// The API server calls this webhook for every ServiceClaim create and update. failurePolicy=fail:
// while the webhook can't answer, ServiceClaim writes are rejected rather than let through
// unchecked.
// +kubebuilder:webhook:path=/validate-platform-paved-dev-v1alpha1-serviceclaim,mutating=false,failurePolicy=fail,sideEffects=None,groups=platform.paved.dev,resources=serviceclaims,verbs=create;update,versions=v1alpha1,name=vserviceclaim-v1alpha1.paved.dev,admissionReviewVersions=v1

// ServiceClaimValidator decides whether a ServiceClaim write is admitted.
type ServiceClaimValidator struct{}

// ValidateCreate admits every new claim.
func (v *ServiceClaimValidator) ValidateCreate(context.Context, *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate admits every update.
func (v *ServiceClaimValidator) ValidateUpdate(_ context.Context, _, _ *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete admits every deletion. The webhook is not registered for deletes, so the API
// server never calls it.
func (v *ServiceClaimValidator) ValidateDelete(context.Context, *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}
