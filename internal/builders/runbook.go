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
	"fmt"
	"math/big"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

// RunbookKey is the ConfigMap key that holds the runbook text.
const RunbookKey = "runbook.md"

// BuildRunbook returns a ConfigMap holding the claim's runbook: what the service is, who owns
// it, what each burn-rate alert means with this claim's numbers, and the first three debugging
// steps. The alerts' "runbook" annotation names this ConfigMap (DECISIONS.md, ADR-011).
func BuildRunbook(sc *platformv1alpha1.ServiceClaim) (*corev1.ConfigMap, error) {
	text, err := runbookText(sc)
	if err != nil {
		return nil, fmt.Errorf("building runbook: %w", err)
	}
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "ConfigMap"},
		ObjectMeta: objectMeta(sc, RunbookName(sc)),
		Data:       map[string]string{RunbookKey: text},
	}, nil
}

func runbookText(sc *platformv1alpha1.ServiceClaim) (string, error) {
	budget, err := slo.ErrorBudget(sc.Spec.SLO.Objective)
	if err != nil {
		return "", err
	}
	window, err := slo.SLOWindow(sc.Spec.SLO.Window)
	if err != nil {
		return "", err
	}
	badRequest, err := badRequestDefinition(sc.Spec.SLI)
	if err != nil {
		return "", err
	}
	namespace := NamespaceName(sc)
	budgetPercent := slo.Decimal(new(big.Rat).Mul(budget, big.NewRat(100, 1)))

	lines := []string{
		"# Runbook: " + sc.Name,
		"",
		"## What this service is",
		"",
		fmt.Sprintf("%s is a %s service onboarded through paved. It runs `%s` in the namespace `%s`.",
			sc.Name, sc.Spec.Tier, sc.Spec.Image, namespace),
		"",
		fmt.Sprintf("- **SLI:** %s. %s", sc.Spec.SLI.Type, badRequest),
		fmt.Sprintf("- **SLO:** %s%% of requests are good over %s, so the error budget is %s (%s%% of requests).",
			sc.Spec.SLO.Objective, sc.Spec.SLO.Window, slo.Decimal(budget), budgetPercent),
		"",
		"## Who owns it",
		"",
		fmt.Sprintf("**%s** owns this service. Everything in `%s` is generated from the ServiceClaim `%s/%s`, "+
			"so change it there rather than in the namespace.", sc.Spec.Owner, namespace, sc.Namespace, sc.Name),
		"",
		"## What each alert means",
		"",
		fmt.Sprintf("Every alert is `%s` with `service=\"%s\"`; its `severity` and window labels say which one fired.",
			slo.AlertName, sc.Name),
		"",
		fmt.Sprintf("| Severity | Windows | Fires above error ratio | At that rate the %s budget lasts |", sc.Spec.SLO.Window),
		"|---|---|---|---|",
	}
	for _, alert := range slo.BurnRateAlerts() {
		threshold, err := alert.Threshold(budget)
		if err != nil {
			return "", err
		}
		lifetime, err := alert.BudgetLifetime(window)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("| %s | %s and %s | %s (%sx) | about %s |",
			alert.Severity, alert.LongWindow, alert.ShortWindow, threshold, alert.BurnRate, lifetime))
	}

	lines = append(lines,
		"",
		"A **page** means users are affected now and the budget runs out within days: respond immediately. "+
			"A **ticket** means a slower burn that will still use up the budget before the window ends: "+
			"handle it during working hours.",
		"",
		"## First three debugging steps",
		"",
		fmt.Sprintf("1. **Check whether a deploy is involved.** Compare the claim's image with the Rollout: "+
			"`kubectl get serviceclaim %[1]s -n %[2]s -o jsonpath='{.spec.image}'` and "+
			"`kubectl get rollout %[1]s -n %[3]s`.", sc.Name, sc.Namespace, namespace),
		fmt.Sprintf("2. **Find when the burn started** on the Grafana dashboard `%s` (tag `paved`): the error ratio "+
			"against its budget line, and request rate by status code.", sc.Name),
		fmt.Sprintf("3. **Read recent logs:** `kubectl logs -n %s -l %s=%s --since=1h --tail=200`. If the burn began "+
			"with an image change, set `spec.image` in the ServiceClaim back to the previous version.",
			namespace, LabelName, sc.Name),
		"",
		"General guidance for these alerts: "+slo.RunbookURL,
	)
	return strings.Join(lines, "\n") + "\n", nil
}

// badRequestDefinition says, in one sentence, what counts against the claim's SLO.
func badRequestDefinition(sli platformv1alpha1.SLISpec) (string, error) {
	switch sli.Type {
	case platformv1alpha1.SLIHTTPAvailability:
		if len(sli.GoodStatuses) == 0 {
			return "", fmt.Errorf("goodStatuses is empty")
		}
		codes := make([]string, 0, len(sli.GoodStatuses))
		for _, status := range sli.GoodStatuses {
			codes = append(codes, strconv.Itoa(int(status)))
		}
		return "A request is bad when its status code is not one of " + strings.Join(codes, ", ") + ".", nil
	case platformv1alpha1.SLIHTTPLatency:
		if _, err := slo.LatencyBucket(sli.LatencyThreshold); err != nil {
			return "", err
		}
		return "A request is bad when it takes longer than " + sli.LatencyThreshold + ".", nil
	default:
		return "", fmt.Errorf("unsupported SLI type %q", sli.Type)
	}
}
