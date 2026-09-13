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
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
	"github.com/singha105/paved/internal/slo"
)

// SLOHealthy condition reasons.
const (
	ReasonWithinBudget          = "WithinBudget"
	ReasonBudgetExhausted       = "BudgetExhausted"
	ReasonFastBurn              = "FastBurn"
	ReasonNoTraffic             = "NoTraffic"
	ReasonPrometheusUnavailable = "PrometheusUnavailable"
	ReasonUnusableData          = "UnusableData"

	// sloRefreshInterval is how often a claim is reconciled, even when nothing changed, so
	// its error budget in status stays current.
	sloRefreshInterval = time.Minute

	// burnRateWindow is the window the status burn rate is read over.
	burnRateWindow = "1h"
)

// sloReport is what one reconcile learned about a claim's SLO.
type sloReport struct {
	condition metav1.Condition
	// budgetRemaining and burnRate1h are formatted for status, or empty when unknown.
	budgetRemaining string
	burnRate1h      string
}

// evaluateSLO reads the claim's error ratio over its SLO window, and its recorded error ratio
// over the last hour, and turns them into status.
//
// It fails open (DECISIONS.md, ADR-014). If Prometheus can't be queried, SLOHealthy is Unknown
// and both values are left empty rather than guessed; a service with no traffic is Unknown
// too. Only a measured value makes SLOHealthy False: no budget left, or a 1h burn rate at or
// above the fastest page alert's.
func evaluateSLO(ctx context.Context, prometheus slo.Querier, claim *platformv1alpha1.ServiceClaim) sloReport {
	report := sloReport{condition: metav1.Condition{
		Type:               platformv1alpha1.ConditionSLOHealthy,
		ObservedGeneration: claim.Generation,
	}}
	setCondition := func(status metav1.ConditionStatus, reason, message string) sloReport {
		report.condition.Status = status
		report.condition.Reason = reason
		report.condition.Message = message
		return report
	}

	if prometheus == nil {
		return setCondition(metav1.ConditionUnknown, ReasonPrometheusUnavailable, "No Prometheus client is configured")
	}
	budget, err := slo.ErrorBudget(claim.Spec.SLO.Objective)
	if err != nil {
		return setCondition(metav1.ConditionUnknown, ReasonInvalidSpec, err.Error())
	}
	fastBurn, err := slo.BurnRateAlerts()[0].Rate()
	if err != nil {
		return setCondition(metav1.ConditionUnknown, ReasonInvalidSpec, err.Error())
	}
	windowQuery, err := slo.ErrorRatioExpr(claim.Spec.SLI, builders.NamespaceName(claim), claim.Spec.SLO.Window)
	if err != nil {
		return setCondition(metav1.ConditionUnknown, ReasonInvalidSpec, err.Error())
	}
	hourQuery := fmt.Sprintf("%s{%s=%q}", slo.RecordName(claim.Spec.SLI.Type, burnRateWindow), slo.LabelService, claim.Name)

	windowRatio, err := prometheus.QueryValue(ctx, windowQuery)
	windowHasData := err == nil
	if err != nil && !errors.Is(err, slo.ErrNoData) {
		// Don't wait out a second timeout against a Prometheus that just failed.
		return setCondition(metav1.ConditionUnknown, ReasonPrometheusUnavailable,
			fmt.Sprintf("Could not read the %s error ratio: %v", claim.Spec.SLO.Window, err))
	}
	hourRatio, err := prometheus.QueryValue(ctx, hourQuery)
	hourHasData := err == nil
	if err != nil && !errors.Is(err, slo.ErrNoData) {
		return setCondition(metav1.ConditionUnknown, ReasonPrometheusUnavailable,
			fmt.Sprintf("Could not read the %s burn rate: %v", burnRateWindow, err))
	}

	if !windowHasData {
		return setCondition(metav1.ConditionUnknown, ReasonNoTraffic,
			fmt.Sprintf("No requests in the last %s, so there is no error ratio to measure", claim.Spec.SLO.Window))
	}
	use, err := slo.SpendBudget(windowRatio, budget)
	if err != nil {
		return setCondition(metav1.ConditionUnknown, ReasonUnusableData, err.Error())
	}
	report.budgetRemaining = slo.FormatPercent(use.Remaining)

	burnRate := 0.0
	if hourHasData {
		burnRate, err = slo.BurnRate(hourRatio, budget)
		if err != nil {
			return setCondition(metav1.ConditionUnknown, ReasonUnusableData, err.Error())
		}
		report.burnRate1h = slo.FormatBurnRate(burnRate)
	}

	switch {
	case use.Remaining <= 0:
		return setCondition(metav1.ConditionFalse, ReasonBudgetExhausted,
			fmt.Sprintf("The %s error budget is used up: %.2fx of it spent", claim.Spec.SLO.Window, use.Consumed))
	case hourHasData && burnRate >= fastBurn:
		return setCondition(metav1.ConditionFalse, ReasonFastBurn,
			fmt.Sprintf("The last hour burned the error budget at %sx, at or above the %gx page threshold",
				report.burnRate1h, fastBurn))
	case hourHasData:
		return setCondition(metav1.ConditionTrue, ReasonWithinBudget,
			fmt.Sprintf("%s of the %s error budget left; 1h burn rate %sx",
				report.budgetRemaining, claim.Spec.SLO.Window, report.burnRate1h))
	default:
		return setCondition(metav1.ConditionTrue, ReasonWithinBudget,
			fmt.Sprintf("%s of the %s error budget left; no requests in the last hour",
				report.budgetRemaining, claim.Spec.SLO.Window))
	}
}
