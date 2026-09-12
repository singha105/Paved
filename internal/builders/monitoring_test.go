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
	rule := BuildPrometheusRule(newClaim(platformv1alpha1.TierInternal))

	if len(rule.Spec.Groups) != 1 || rule.Spec.Groups[0].Name != "url-shortener.slo" {
		t.Fatalf("groups = %+v, want one group named url-shortener.slo", rule.Spec.Groups)
	}
	if len(rule.Spec.Groups[0].Rules) != 0 {
		t.Errorf("rules = %+v, want none until the SLO rules exist", rule.Spec.Groups[0].Rules)
	}
}

func TestBuildDashboard(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierBatch)
	cm := BuildDashboard(sc)

	if cm.Labels[DashboardLabel] != "1" {
		t.Errorf("label %s = %q, want \"1\" so Grafana loads it", DashboardLabel, cm.Labels[DashboardLabel])
	}
	raw, ok := cm.Data["url-shortener.json"]
	if !ok {
		t.Fatalf("data keys = %v, want url-shortener.json", slicesOfKeys(cm.Data))
	}

	var model struct {
		UID   string `json:"uid"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v\n%s", err, raw)
	}
	if model.Title != testClaimName {
		t.Errorf("title = %q, want %q", model.Title, testClaimName)
	}
	if model.UID != DashboardUID(sc) || len(model.UID) > 40 || !strings.HasPrefix(model.UID, "paved-") {
		t.Errorf("uid = %q, want DashboardUID() (%q), at most 40 characters", model.UID, DashboardUID(sc))
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

func slicesOfKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
