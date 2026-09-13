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
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	// BreakGlassAnnotation lets one image change through a deploy freeze. Its value is the reason,
	// which is recorded, with the user who made the change, in a BreakGlassUsed Event.
	BreakGlassAnnotation = "paved.dev/break-glass"

	// ReasonBreakGlassUsed is the reason of the Event recorded when break-glass lets an image
	// change through a freeze.
	ReasonBreakGlassUsed = "BreakGlassUsed"

	// eventNoteLimit is the longest note an events.k8s.io Event may carry.
	eventNoteLimit = 1024
)

// SetupServiceClaimWebhookWithManager registers the ServiceClaim validating webhook with the manager.
func SetupServiceClaimWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &platformv1alpha1.ServiceClaim{}).
		WithValidator(&ServiceClaimValidator{Recorder: mgr.GetEventRecorder("paved-webhook")}).
		Complete()
}

// The API server calls this webhook for every ServiceClaim create and update. failurePolicy=fail:
// while the webhook can't answer, ServiceClaim writes are rejected rather than let through
// unchecked. sideEffects=NoneOnDryRun: the only side effect, the BreakGlassUsed Event, is skipped
// for dry runs.
// +kubebuilder:webhook:path=/validate-platform-paved-dev-v1alpha1-serviceclaim,mutating=false,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=platform.paved.dev,resources=serviceclaims,verbs=create;update,versions=v1alpha1,name=vserviceclaim-v1alpha1.paved.dev,admissionReviewVersions=v1

// ServiceClaimValidator rejects image changes to a claim whose deploys are frozen, unless the
// change carries a break-glass reason (DECISIONS.md, ADR-018).
type ServiceClaimValidator struct {
	// Recorder records BreakGlassUsed Events. When nil, break-glass use is only logged.
	Recorder events.EventRecorder
}

// DeploysFrozenError rejects an image change to a claim whose deploys are frozen.
type DeploysFrozenError struct {
	// Claim is the claim's name, and Window its SLO window.
	Claim, Window string
	// Budget and BurnRate are the error budget remaining and the 1h burn rate as the controller
	// last recorded them, formatted for the message.
	Budget, BurnRate string
}

func (e *DeploysFrozenError) Error() string {
	return fmt.Sprintf("deploys frozen: %s has %s error budget remaining in a %s window (burn rate %s). "+
		"Override with annotation %s=\"<reason>\" — this is audited.",
		e.Claim, e.Budget, e.Window, e.BurnRate, BreakGlassAnnotation)
}

// ValidateCreate admits every new claim: a claim that doesn't exist yet can't be frozen.
func (v *ServiceClaimValidator) ValidateCreate(context.Context, *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate rejects a change to spec.image while the claim's DeploysFrozen condition is True,
// unless the update sets a break-glass reason. A break-glass change is admitted with a warning and
// recorded in a BreakGlassUsed Event carrying the reason and the user who made it. Changes that
// leave the image alone are always admitted.
func (v *ServiceClaimValidator) ValidateUpdate(
	ctx context.Context, oldClaim, newClaim *platformv1alpha1.ServiceClaim,
) (admission.Warnings, error) {
	// The stored claim carries the status: the update can't change it.
	frozen := meta.IsStatusConditionTrue(oldClaim.Status.Conditions, platformv1alpha1.ConditionDeploysFrozen)
	if !frozen || oldClaim.Spec.Image == newClaim.Spec.Image {
		return nil, nil
	}

	log := logf.FromContext(ctx)
	reason, ok := breakGlassReason(oldClaim, newClaim)
	if !ok {
		log.Info("Rejected an image change during a deploy freeze",
			"from", oldClaim.Spec.Image, "to", newClaim.Spec.Image)
		return nil, &DeploysFrozenError{
			Claim:    oldClaim.Name,
			Window:   oldClaim.Spec.SLO.Window,
			Budget:   budgetForMessage(oldClaim.Status.ErrorBudgetRemaining),
			BurnRate: burnRateForMessage(oldClaim.Status.BurnRate1h),
		}
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		// Without the request there is no actor to audit, so the change is not let through.
		return nil, fmt.Errorf("reading the admission request to audit break-glass: %w", err)
	}
	actor := req.UserInfo.Username
	dryRun := req.DryRun != nil && *req.DryRun
	log.Info("Admitted an image change during a deploy freeze with break-glass",
		"from", oldClaim.Spec.Image, "to", newClaim.Spec.Image, "user", actor, "reason", reason, "dryRun", dryRun)
	if v.Recorder != nil && !dryRun {
		note := fmt.Sprintf("%s changed the image from %s to %s during a deploy freeze: %s",
			actor, oldClaim.Spec.Image, newClaim.Spec.Image, reason)
		v.Recorder.Eventf(newClaim, nil, corev1.EventTypeWarning, ReasonBreakGlassUsed, "UpdateImage",
			"%s", truncate(note, eventNoteLimit))
	}
	return admission.Warnings{fmt.Sprintf(
		"deploys are frozen: this image change was let through by %s and recorded in a %s Event",
		BreakGlassAnnotation, ReasonBreakGlassUsed)}, nil
}

// ValidateDelete admits every deletion. The webhook is not registered for deletes, so the API
// server never calls it.
func (v *ServiceClaimValidator) ValidateDelete(context.Context, *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}

// breakGlassReason returns the break-glass reason an update sets, and whether it sets one. The
// annotation counts only when the update adds it or changes its value, so a reason left on the
// claim from an earlier emergency can't carry a later image change through a freeze. A blank
// reason counts as none.
func breakGlassReason(oldClaim, newClaim *platformv1alpha1.ServiceClaim) (string, bool) {
	value := newClaim.Annotations[BreakGlassAnnotation]
	reason := strings.TrimSpace(value)
	if reason == "" || value == oldClaim.Annotations[BreakGlassAnnotation] {
		return "", false
	}
	return reason, true
}

// budgetForMessage turns the recorded error budget, such as "0.0%" or "3.2%", into "0%" or "3.2%".
func budgetForMessage(recorded string) string {
	if recorded == "" {
		return "unknown"
	}
	return strings.Replace(recorded, ".0%", "%", 1)
}

// burnRateForMessage turns the recorded burn rate, such as "14.20", into "14.2x".
func burnRateForMessage(recorded string) string {
	rate, err := strconv.ParseFloat(recorded, 64)
	if err != nil {
		return "unknown"
	}
	return strconv.FormatFloat(rate, 'f', 1, 64) + "x"
}

// truncate shortens s to at most limit bytes without splitting a character.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	const ellipsis = "…"
	cut := limit - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}
