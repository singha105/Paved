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
	"encoding/json"
	"maps"
	"strings"
	"testing"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

func TestBuildServiceMonitor(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	sm := BuildServiceMonitor(sc)

	if !maps.Equal(sm.Spec.Selector.MatchLabels, SelectorLabels(sc)) {
		t.Errorf("selector = %v, want %v", sm.Spec.Selector.MatchLabels, SelectorLabels(sc))
	}
	if len(sm.Spec.Endpoints) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(sm.Spec.Endpoints))
	}
	ep := sm.Spec.Endpoints[0]
	if ep.Port != PortName || ep.Path != "/metrics" || ep.Interval != "30s" {
		t.Errorf("endpoint = port %q path %q every %q, want %q /metrics every 30s", ep.Port, ep.Path, ep.Interval, PortName)
	}
}

func TestBuildPrometheusRule(t *testing.T) {
	rule, err := BuildPrometheusRule(newClaim(platformv1alpha1.TierInternal))
	if err != nil {
		t.Fatal(err)
	}

	groups := rule.Spec.Groups
	if len(groups) != 2 || groups[0].Name != "url-shortener.sli" || groups[1].Name != "url-shortener.slo-alerts" {
		t.Fatalf("groups = %+v, want url-shortener.sli then url-shortener.slo-alerts", groups)
	}
	if len(groups[0].Rules) != len(slo.Windows()) {
		t.Errorf("SLI group has %d rules, want one per window (%d)", len(groups[0].Rules), len(slo.Windows()))
	}
	for _, r := range groups[0].Rules {
		if !strings.HasPrefix(r.Record, "sli:http_availability:error_ratio_rate") {
			t.Errorf("SLI group rule %+v is not an availability recording rule", r)
		}
	}
	if len(groups[1].Rules) != 4 {
		t.Fatalf("alert group has %d rules, want 4", len(groups[1].Rules))
	}
	for _, r := range groups[1].Rules {
		if r.Alert != slo.AlertName || r.Annotations["runbook"] != "svc-url-shortener/url-shortener-runbook" {
			t.Errorf("alert %q runbook annotation = %q, want the claim's runbook ConfigMap",
				r.Alert, r.Annotations["runbook"])
		}
	}
}

func TestBuildPrometheusRuleRejectsInvalidSLI(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	sc.Spec.SLI.GoodStatuses = nil
	if _, err := BuildPrometheusRule(sc); err == nil {
		t.Error("BuildPrometheusRule returned no error for an empty goodStatuses")
	}
}

// dashboardPanels decodes the dashboard a claim produces.
func dashboardPanels(t *testing.T, sc *platformv1alpha1.ServiceClaim) grafanaDashboard {
	t.Helper()
	cm, err := BuildDashboard(sc)
	if err != nil {
		t.Fatal(err)
	}
	if cm.Labels[DashboardLabel] != "1" {
		t.Errorf("label %s = %q, want \"1\" so Grafana loads it", DashboardLabel, cm.Labels[DashboardLabel])
	}
	raw, ok := cm.Data[sc.Name+".json"]
	if !ok {
		t.Fatalf("dashboard ConfigMap has no %s.json key", sc.Name)
	}
	var model grafanaDashboard
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	return model
}

func TestBuildDashboard(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	model := dashboardPanels(t, sc)

	if model.UID != DashboardUID(sc) || model.Title != testClaimName {
		t.Errorf("uid %q title %q, want %q %q", model.UID, model.Title, DashboardUID(sc), testClaimName)
	}
	titles := make([]string, 0, len(model.Panels))
	for _, p := range model.Panels {
		titles = append(titles, p.Title)
		if p.Datasource.UID != "prometheus" {
			t.Errorf("panel %q datasource uid = %q, want prometheus", p.Title, p.Datasource.UID)
		}
	}
	wantTitles := []string{PanelRequestRate, PanelErrorRatio, PanelLatency, PanelBudgetLeft}
	if strings.Join(titles, "|") != strings.Join(wantTitles, "|") {
		t.Fatalf("panels = %v, want %v", titles, wantTitles)
	}

	exprs := func(i int) string {
		all := make([]string, 0, len(model.Panels[i].Targets))
		for _, target := range model.Panels[i].Targets {
			all = append(all, target.Expr)
		}
		return strings.Join(all, "\n")
	}
	checks := []struct {
		panel int
		want  string
	}{
		{0, `sum by (code) (rate(http_requests_total{namespace="svc-url-shortener"}[$__rate_interval]))`},
		{1, `sli:http_availability:error_ratio_rate5m{service="url-shortener"}`},
		{1, `sli:http_availability:error_ratio_rate1h{service="url-shortener"}`},
		{1, "vector(0.005)"},
		{2, "histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket"},
		{3, `code!~"200|201|204|301|302|304|400|404"}[28d]`},
		{3, "/ 0.005)"},
	}
	for _, c := range checks {
		if !strings.Contains(exprs(c.panel), c.want) {
			t.Errorf("panel %q is missing %q; queries:\n%s", model.Panels[c.panel].Title, c.want, exprs(c.panel))
		}
	}
}

func TestBuildDashboardShowsTheLatencyThreshold(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierInternal)
	sc.Spec.SLI.Type = platformv1alpha1.SLIHTTPLatency
	model := dashboardPanels(t, sc)

	latency := make([]string, 0, len(model.Panels[2].Targets))
	for _, target := range model.Panels[2].Targets {
		latency = append(latency, target.Expr)
	}
	if !strings.Contains(strings.Join(latency, "\n"), "vector(0.25)") {
		t.Errorf("latency panel queries = %v, want a 0.25s threshold line", latency)
	}
	if !strings.Contains(model.Panels[1].Targets[0].Expr, "sli:http_latency:error_ratio_rate5m") {
		t.Errorf("error ratio panel reads %q, want the latency SLI", model.Panels[1].Targets[0].Expr)
	}
}

func TestDashboardUID(t *testing.T) {
	a := newClaim(platformv1alpha1.TierPublic)
	long := newClaim(platformv1alpha1.TierPublic)
	long.Name = strings.Repeat("a", 59)
	elsewhere := newClaim(platformv1alpha1.TierPublic)
	elsewhere.Namespace = "team-b"

	if DashboardUID(a) != DashboardUID(newClaim(platformv1alpha1.TierPublic)) {
		t.Error("DashboardUID is not stable for the same claim")
	}
	if got := len(DashboardUID(long)); got > 40 {
		t.Errorf("uid for a 59-character claim name is %d characters, want at most 40", got)
	}
	if DashboardUID(a) == DashboardUID(elsewhere) {
		t.Error("claims with the same name in different namespaces share a dashboard UID")
	}
}
