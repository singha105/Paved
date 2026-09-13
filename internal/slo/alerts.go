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

package slo

import (
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	// AlertName is shared by all four burn-rate alerts; their labels tell them apart.
	AlertName = "SLOErrorBudgetBurn"

	// RunbookURL explains what a burn-rate alert means and how to respond to one.
	RunbookURL = "https://github.com/singha105/paved/blob/main/docs/runbooks/slo-burn-rate.md"

	LabelSeverity    = "severity"
	LabelLongWindow  = "long_window"
	LabelShortWindow = "short_window"

	SeverityPage   = "page"
	SeverityTicket = "ticket"
)

// BurnRateAlert is one row of the multi-window, multi-burn-rate alert table from the Google
// SRE Workbook ("Alerting on SLOs", table 2).
type BurnRateAlert struct {
	Severity string
	// BurnRate is how many times faster than sustainable the budget is being spent. A burn
	// rate of 1 uses exactly the whole budget over the SLO window.
	BurnRate    string
	LongWindow  string
	ShortWindow string
}

var burnRateAlerts = []BurnRateAlert{
	{Severity: SeverityPage, BurnRate: "14.4", LongWindow: "1h", ShortWindow: "5m"},
	{Severity: SeverityPage, BurnRate: "6", LongWindow: "6h", ShortWindow: "30m"},
	{Severity: SeverityTicket, BurnRate: "3", LongWindow: "1d", ShortWindow: "2h"},
	{Severity: SeverityTicket, BurnRate: "1", LongWindow: "3d", ShortWindow: "6h"},
}

// BurnRateAlerts returns the alert table, most urgent first.
func BurnRateAlerts() []BurnRateAlert {
	return slices.Clone(burnRateAlerts)
}

// Threshold returns the error ratio above which the alert fires for a claim with the given
// error budget: burnRate × budget, as an exact decimal.
func (a BurnRateAlert) Threshold(budget *big.Rat) (string, error) {
	burnRate, ok := new(big.Rat).SetString(a.BurnRate)
	if !ok {
		return "", fmt.Errorf("burn rate %q is not a number", a.BurnRate)
	}
	return Decimal(new(big.Rat).Mul(burnRate, budget)), nil
}

// BudgetLifetime returns how long a full error budget for window lasts when spent at the
// alert's burn rate, rounded for people: "47 hours", "4.7 days".
func (a BurnRateAlert) BudgetLifetime(window time.Duration) (string, error) {
	burnRate, err := strconv.ParseFloat(a.BurnRate, 64)
	if err != nil || burnRate <= 0 {
		return "", fmt.Errorf("burn rate %q is not a positive number", a.BurnRate)
	}
	hours := window.Hours() / burnRate
	if hours < 48 {
		return strconv.FormatFloat(math.Round(hours), 'f', -1, 64) + " hours", nil
	}
	return strconv.FormatFloat(math.Round(hours/24*10)/10, 'f', -1, 64) + " days", nil
}

// AlertRules returns the four burn-rate alerts for sc. Each fires only while the error ratio
// over both its long and its short window exceeds burnRate × (1 - objective). The long window
// shows the burn is big enough to matter; the short one shows it is still happening, so an
// alert clears within one short window after the burn stops, without a "for" delay.
// runbook names the claim's runbook ConfigMap.
func AlertRules(sc *platformv1alpha1.ServiceClaim, runbook string) ([]monitoringv1.Rule, error) {
	budget, err := ErrorBudget(sc.Spec.SLO.Objective)
	if err != nil {
		return nil, err
	}
	window, err := SLOWindow(sc.Spec.SLO.Window)
	if err != nil {
		return nil, err
	}

	rules := make([]monitoringv1.Rule, 0, len(burnRateAlerts))
	for _, alert := range burnRateAlerts {
		threshold, err := alert.Threshold(budget)
		if err != nil {
			return nil, err
		}
		lifetime, err := alert.BudgetLifetime(window)
		if err != nil {
			return nil, err
		}

		labels := RuleLabels(sc)
		labels[LabelSeverity] = alert.Severity
		labels[LabelLongWindow] = alert.LongWindow
		labels[LabelShortWindow] = alert.ShortWindow

		rules = append(rules, monitoringv1.Rule{
			Alert: AlertName,
			Expr: intstr.FromString(fmt.Sprintf("%s{service=%q} > %s\nand\n%s{service=%q} > %s",
				RecordName(sc.Spec.SLI.Type, alert.LongWindow), sc.Name, threshold,
				RecordName(sc.Spec.SLI.Type, alert.ShortWindow), sc.Name, threshold)),
			Labels: labels,
			Annotations: map[string]string{
				"summary": fmt.Sprintf("%s is spending its error budget %sx faster than it can sustain",
					sc.Name, alert.BurnRate),
				"description": fmt.Sprintf("The error ratio is above %s over both the last %s and the last %s. "+
					"At %sx, the %s error budget of %s lasts about %s.",
					threshold, alert.LongWindow, alert.ShortWindow,
					alert.BurnRate, sc.Spec.SLO.Window, Decimal(budget), lifetime),
				"owner":       sc.Spec.Owner,
				"runbook_url": RunbookURL,
				"runbook":     runbook,
			},
		})
	}
	return rules, nil
}

// SLOWindow parses an SLO window such as "28d".
func SLOWindow(window string) (time.Duration, error) {
	days, err := strconv.Atoi(strings.TrimSuffix(window, "d"))
	if err != nil || !strings.HasSuffix(window, "d") || days <= 0 {
		return 0, fmt.Errorf("SLO window %q is not a whole number of days such as 28d", window)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}
