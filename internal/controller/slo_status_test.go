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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/singha105/paved/internal/slo"
)

// queryResult is what fakeQuerier answers for one kind of query.
type queryResult struct {
	value float64
	err   error
}

// fakeQuerier answers the 1h recorded-ratio query with hour and any other query with window,
// and remembers the queries it was asked.
type fakeQuerier struct {
	window, hour queryResult
	asked        []string
}

func (f *fakeQuerier) QueryValue(_ context.Context, query string) (float64, error) {
	f.asked = append(f.asked, query)
	if strings.Contains(query, ":error_ratio_rate1h{") {
		return f.hour.value, f.hour.err
	}
	return f.window.value, f.window.err
}

func TestEvaluateSLO(t *testing.T) {
	down := fmt.Errorf("querying Prometheus: %w", errors.New("connection refused"))
	noData := slo.ErrNoData

	tests := []struct {
		name         string
		window, hour queryResult
		wantStatus   metav1.ConditionStatus
		wantReason   string
		wantBudget   string
		wantBurnRate string
		wantQueries  int
	}{
		{"perfect service", queryResult{value: 0}, queryResult{value: 0},
			metav1.ConditionTrue, ReasonWithinBudget, "100.0%", "0.00", 2},
		{"half the budget spent", queryResult{value: 0.0025}, queryResult{value: 0.0005},
			metav1.ConditionTrue, ReasonWithinBudget, "50.0%", "0.10", 2},
		{"traffic this window but none in the last hour", queryResult{value: 0.001}, queryResult{err: noData},
			metav1.ConditionTrue, ReasonWithinBudget, "80.0%", "", 2},
		{"budget used up", queryResult{value: 0.01}, queryResult{value: 0.002},
			metav1.ConditionFalse, ReasonBudgetExhausted, "0.0%", "0.40", 2},
		{"burning at the page rate", queryResult{value: 0.001}, queryResult{value: 0.08},
			metav1.ConditionFalse, ReasonFastBurn, "80.0%", "16.00", 2},
		{"no traffic at all", queryResult{err: noData}, queryResult{err: noData},
			metav1.ConditionUnknown, ReasonNoTraffic, "", "", 2},
		{"Prometheus unreachable", queryResult{err: down}, queryResult{err: down},
			metav1.ConditionUnknown, ReasonPrometheusUnavailable, "", "", 1},
		{"Prometheus fails on the second query", queryResult{value: 0.001}, queryResult{err: down},
			metav1.ConditionUnknown, ReasonPrometheusUnavailable, "", "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			querier := &fakeQuerier{window: tt.window, hour: tt.hour}
			report := evaluateSLO(t.Context(), querier, unitTestClaim())

			c := report.condition
			if c.Type != "SLOHealthy" || c.Status != tt.wantStatus || c.Reason != tt.wantReason {
				t.Errorf("condition = %s=%s (%s): %s; want %s (%s)", c.Type, c.Status, c.Reason, c.Message, tt.wantStatus, tt.wantReason)
			}
			if c.ObservedGeneration != 1 || c.Message == "" {
				t.Errorf("condition observedGeneration %d message %q, want 1 and a message", c.ObservedGeneration, c.Message)
			}
			if report.budgetRemaining != tt.wantBudget || report.burnRate1h != tt.wantBurnRate {
				t.Errorf("budget %q burn rate %q, want %q %q", report.budgetRemaining, report.burnRate1h, tt.wantBudget, tt.wantBurnRate)
			}
			if len(querier.asked) != tt.wantQueries {
				t.Errorf("asked %d queries, want %d: %v", len(querier.asked), tt.wantQueries, querier.asked)
			}
		})
	}
}

func TestEvaluateSLOQueries(t *testing.T) {
	querier := &fakeQuerier{window: queryResult{value: 0}, hour: queryResult{value: 0}}
	evaluateSLO(t.Context(), querier, unitTestClaim())

	if len(querier.asked) != 2 {
		t.Fatalf("asked %v, want two queries", querier.asked)
	}
	if !strings.Contains(querier.asked[0], `namespace="svc-url-shortener"`) || !strings.Contains(querier.asked[0], "[28d]") {
		t.Errorf("budget query = %q, want the claim's namespace over its 28d window", querier.asked[0])
	}
	if querier.asked[1] != `sli:http_availability:error_ratio_rate1h{service="url-shortener"}` {
		t.Errorf("burn rate query = %q, want the recorded 1h error ratio", querier.asked[1])
	}
}

func TestEvaluateSLOWithoutAClient(t *testing.T) {
	report := evaluateSLO(t.Context(), nil, unitTestClaim())
	if report.condition.Status != metav1.ConditionUnknown || report.condition.Reason != ReasonPrometheusUnavailable {
		t.Errorf("condition = %s (%s), want Unknown (%s)", report.condition.Status, report.condition.Reason, ReasonPrometheusUnavailable)
	}
}
