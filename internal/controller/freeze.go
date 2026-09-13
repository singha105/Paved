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
	"fmt"
	"strconv"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

// DeploysFrozen and Ready condition reasons.
const (
	ReasonBudgetRecovering   = "BudgetRecovering"
	ReasonBudgetUnknown      = "BudgetUnknown"
	ReasonRolloutHealthy     = "RolloutHealthy"
	ReasonRolloutProgressing = "RolloutProgressing"
	ReasonRolloutDegraded    = "RolloutDegraded"
	ReasonRolloutNotFound    = "RolloutNotFound"
	ReasonRolloutUnreadable  = "RolloutUnreadable"
)

// UnfreezeBudget is the share of the error budget that must be back before a frozen claim's
// deploys unfreeze. Deploys freeze when none is left (DECISIONS.md, ADR-017).
const UnfreezeBudget = 0.05

// freezeCondition decides a claim's DeploysFrozen condition from this reconcile's SLO report and
// the condition's current status. Deploys freeze when no error budget is left and unfreeze only
// once UnfreezeBudget of it is back. Without that gap, a service hovering at zero would freeze and
// unfreeze from one reconcile to the next as its error ratio wobbled, and image changes would get
// through in the unfrozen moments. When the budget can't be measured, the last decision stands.
func freezeCondition(claim *platformv1alpha1.ServiceClaim, report sloReport) metav1.Condition {
	window := claim.Spec.SLO.Window
	previous := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionDeploysFrozen)
	wasFrozen := previous != nil && previous.Status == metav1.ConditionTrue
	unfreezeAt := slo.FormatPercent(UnfreezeBudget)

	condition := metav1.Condition{
		Type:               platformv1alpha1.ConditionDeploysFrozen,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: claim.Generation,
	}
	switch {
	case !report.measured && wasFrozen:
		condition.Status = metav1.ConditionTrue
		condition.Reason = ReasonBudgetUnknown
		condition.Message = fmt.Sprintf("The error budget can't be measured (%s), so deploys stay frozen until it can",
			report.condition.Reason)
	case !report.measured:
		condition.Reason = ReasonBudgetUnknown
		condition.Message = fmt.Sprintf("The error budget can't be measured (%s); deploys are not frozen",
			report.condition.Reason)
	case report.remaining <= 0:
		condition.Status = metav1.ConditionTrue
		condition.Reason = ReasonBudgetExhausted
		condition.Message = fmt.Sprintf("No %s error budget left; image changes are rejected until %s of it is back",
			window, unfreezeAt)
	case wasFrozen && report.remaining < UnfreezeBudget:
		condition.Status = metav1.ConditionTrue
		condition.Reason = ReasonBudgetRecovering
		condition.Message = fmt.Sprintf("%s of the %s error budget is back; image changes stay frozen until %s",
			report.budgetRemaining, window, unfreezeAt)
	default:
		condition.Reason = ReasonWithinBudget
		condition.Message = fmt.Sprintf("%s of the %s error budget left", report.budgetRemaining, window)
	}
	return condition
}

// readyCondition reports whether a claim's service is running as claimed: every managed resource
// is applied, and Argo Rollouts reports the Rollout healthy at its current generation. getErr is
// the error from reading the Rollout, if there was one.
func readyCondition(
	claim *platformv1alpha1.ServiceClaim, synced metav1.Condition, rollout *rolloutsv1alpha1.Rollout, getErr error,
) metav1.Condition {
	condition := metav1.Condition{
		Type:               platformv1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: claim.Generation,
	}
	switch {
	case synced.Status != metav1.ConditionTrue:
		condition.Reason = synced.Reason
		condition.Message = "The managed resources are not all applied: " + synced.Message
	case apierrors.IsNotFound(getErr):
		condition.Reason = ReasonRolloutNotFound
		condition.Message = "The Rollout does not exist yet"
	case getErr != nil:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = ReasonRolloutUnreadable
		condition.Message = getErr.Error()
	case rollout.Status.ObservedGeneration != strconv.FormatInt(rollout.Generation, 10):
		// Argo Rollouts' phase describes the generation it last observed, which may be older.
		condition.Reason = ReasonRolloutProgressing
		condition.Message = fmt.Sprintf("Argo Rollouts has not yet observed generation %d of the Rollout", rollout.Generation)
	case rollout.Status.Phase == rolloutsv1alpha1.RolloutPhaseHealthy:
		condition.Status = metav1.ConditionTrue
		condition.Reason = ReasonRolloutHealthy
		condition.Message = "The Rollout is healthy"
	case rollout.Status.Phase == rolloutsv1alpha1.RolloutPhaseDegraded:
		condition.Reason = ReasonRolloutDegraded
		condition.Message = rolloutPhaseMessage(rollout)
	default:
		condition.Reason = ReasonRolloutProgressing
		condition.Message = rolloutPhaseMessage(rollout)
	}
	return condition
}

// rolloutPhaseMessage describes a Rollout's phase, with Argo Rollouts' explanation when it gave one.
func rolloutPhaseMessage(rollout *rolloutsv1alpha1.Rollout) string {
	phase := string(rollout.Status.Phase)
	if phase == "" {
		phase = "not reporting a phase yet"
	}
	if rollout.Status.Message == "" {
		return "The Rollout is " + phase
	}
	return fmt.Sprintf("The Rollout is %s: %s", phase, rollout.Status.Message)
}
