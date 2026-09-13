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
	"errors"
	"testing"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

// measuredReport is the SLO report of a reconcile that found remaining of the error budget left.
func measuredReport(remaining float64) sloReport {
	return sloReport{
		condition:       metav1.Condition{Reason: ReasonWithinBudget},
		measured:        true,
		remaining:       remaining,
		budgetRemaining: slo.FormatPercent(remaining),
	}
}

// unmeasuredReport is the SLO report of a reconcile that could not read the error budget.
func unmeasuredReport() sloReport {
	return sloReport{condition: metav1.Condition{Reason: ReasonPrometheusUnavailable}}
}

func TestFreezeConditionHysteresis(t *testing.T) {
	claim := unitTestClaim()
	// Each step reconciles the claim as the previous step left it.
	steps := []struct {
		name       string
		report     sloReport
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"a full budget", measuredReport(1), metav1.ConditionFalse, ReasonWithinBudget},
		{"1% left", measuredReport(0.01), metav1.ConditionFalse, ReasonWithinBudget},
		{"nothing left", measuredReport(0), metav1.ConditionTrue, ReasonBudgetExhausted},
		{"3% back", measuredReport(0.03), metav1.ConditionTrue, ReasonBudgetRecovering},
		{"budget unknown while frozen", unmeasuredReport(), metav1.ConditionTrue, ReasonBudgetUnknown},
		{"just under the unfreeze line", measuredReport(0.0499), metav1.ConditionTrue, ReasonBudgetRecovering},
		{"5% back", measuredReport(UnfreezeBudget), metav1.ConditionFalse, ReasonWithinBudget},
		{"back down to 3%", measuredReport(0.03), metav1.ConditionFalse, ReasonWithinBudget},
		{"budget unknown while not frozen", unmeasuredReport(), metav1.ConditionFalse, ReasonBudgetUnknown},
		{"used up again", measuredReport(0), metav1.ConditionTrue, ReasonBudgetExhausted},
	}
	for _, step := range steps {
		got := freezeCondition(claim, step.report)
		if got.Status != step.wantStatus || got.Reason != step.wantReason {
			t.Fatalf("%s: DeploysFrozen is %s (%s), want %s (%s); message %q",
				step.name, got.Status, got.Reason, step.wantStatus, step.wantReason, got.Message)
		}
		meta.SetStatusCondition(&claim.Status.Conditions, got)
	}
}

func TestFreezeConditionForANewClaimWithoutABudget(t *testing.T) {
	got := freezeCondition(unitTestClaim(), unmeasuredReport())
	if got.Status != metav1.ConditionFalse || got.Reason != ReasonBudgetUnknown {
		t.Errorf("DeploysFrozen is %s (%s), want False (%s)", got.Status, got.Reason, ReasonBudgetUnknown)
	}
}

func TestFreezeConditionMessages(t *testing.T) {
	claim := unitTestClaim()
	frozen := freezeCondition(claim, measuredReport(0))
	if want := "No 28d error budget left; image changes are rejected until 5.0% of it is back"; frozen.Message != want {
		t.Errorf("exhausted message %q, want %q", frozen.Message, want)
	}
	meta.SetStatusCondition(&claim.Status.Conditions, frozen)

	recovering := freezeCondition(claim, measuredReport(0.032))
	if want := "3.2% of the 28d error budget is back; image changes stay frozen until 5.0%"; recovering.Message != want {
		t.Errorf("recovering message %q, want %q", recovering.Message, want)
	}

	held := freezeCondition(claim, unmeasuredReport())
	if want := "The error budget can't be measured (PrometheusUnavailable), so deploys stay frozen until it can"; held.Message != want {
		t.Errorf("held message %q, want %q", held.Message, want)
	}
}

func TestReadyCondition(t *testing.T) {
	synced := metav1.Condition{
		Type: platformv1alpha1.ConditionResourcesSynced, Status: metav1.ConditionTrue, Reason: ReasonApplied,
	}
	notSynced := metav1.Condition{
		Type: platformv1alpha1.ConditionResourcesSynced, Status: metav1.ConditionFalse, Reason: ReasonApplyFailed,
		Message: "applying Service: forbidden",
	}
	rollout := func(observedGeneration string, phase rolloutsv1alpha1.RolloutPhase, message string) *rolloutsv1alpha1.Rollout {
		r := &rolloutsv1alpha1.Rollout{}
		r.Generation = 2
		r.Status.ObservedGeneration = observedGeneration
		r.Status.Phase = phase
		r.Status.Message = message
		return r
	}
	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "rollouts"}, unitClaimName)

	tests := []struct {
		name        string
		synced      metav1.Condition
		rollout     *rolloutsv1alpha1.Rollout
		getErr      error
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{"healthy", synced, rollout("2", rolloutsv1alpha1.RolloutPhaseHealthy, ""), nil,
			metav1.ConditionTrue, ReasonRolloutHealthy, "The Rollout is healthy"},
		{"resources not applied", notSynced, nil, nil,
			metav1.ConditionFalse, ReasonApplyFailed, "The managed resources are not all applied: applying Service: forbidden"},
		{"Rollout not created yet", synced, &rolloutsv1alpha1.Rollout{}, notFound,
			metav1.ConditionFalse, ReasonRolloutNotFound, "The Rollout does not exist yet"},
		{"Rollout unreadable", synced, &rolloutsv1alpha1.Rollout{}, errors.New("cache is not started"),
			metav1.ConditionUnknown, ReasonRolloutUnreadable, "cache is not started"},
		{"healthy at an older generation", synced, rollout("1", rolloutsv1alpha1.RolloutPhaseHealthy, ""), nil,
			metav1.ConditionFalse, ReasonRolloutProgressing, "Argo Rollouts has not yet observed generation 2 of the Rollout"},
		{"canary paused", synced, rollout("2", rolloutsv1alpha1.RolloutPhasePaused, "CanaryPauseStep"), nil,
			metav1.ConditionFalse, ReasonRolloutProgressing, "The Rollout is Paused: CanaryPauseStep"},
		{"progressing", synced, rollout("2", rolloutsv1alpha1.RolloutPhaseProgressing, ""), nil,
			metav1.ConditionFalse, ReasonRolloutProgressing, "The Rollout is Progressing"},
		{"degraded", synced, rollout("2", rolloutsv1alpha1.RolloutPhaseDegraded, "ProgressDeadlineExceeded"), nil,
			metav1.ConditionFalse, ReasonRolloutDegraded, "The Rollout is Degraded: ProgressDeadlineExceeded"},
		{"no phase yet", synced, rollout("2", "", ""), nil,
			metav1.ConditionFalse, ReasonRolloutProgressing, "The Rollout is not reporting a phase yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readyCondition(unitTestClaim(), tt.synced, tt.rollout, tt.getErr)
			if got.Type != platformv1alpha1.ConditionReady || got.Status != tt.wantStatus ||
				got.Reason != tt.wantReason || got.Message != tt.wantMessage {
				t.Errorf("Ready is %s %s (%s) %q, want %s (%s) %q",
					got.Type, got.Status, got.Reason, got.Message, tt.wantStatus, tt.wantReason, tt.wantMessage)
			}
		})
	}
}
