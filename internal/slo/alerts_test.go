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
	"strings"
	"testing"
	"time"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const testRunbook = "svc-url-shortener/url-shortener-runbook"

func TestAlertRulesFollowTheWorkbookTable(t *testing.T) {
	rules, err := AlertRules(newClaim(platformv1alpha1.SLIHTTPAvailability), testRunbook)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ severity, long, short, threshold string }{
		{"page", "1h", "5m", "0.072"},
		{"page", "6h", "30m", "0.03"},
		{"ticket", "1d", "2h", "0.015"},
		{"ticket", "3d", "6h", "0.005"},
	}
	if len(rules) != len(want) {
		t.Fatalf("got %d alerts, want %d", len(rules), len(want))
	}

	for i, w := range want {
		rule := rules[i]
		if rule.Alert != AlertName || rule.Record != "" {
			t.Errorf("rule %d: alert %q record %q, want alert %q", i, rule.Alert, rule.Record, AlertName)
		}
		if rule.For != nil {
			t.Errorf("rule %d: for = %v, want no for clause", i, *rule.For)
		}
		labels := rule.Labels
		if labels["severity"] != w.severity || labels["long_window"] != w.long || labels["short_window"] != w.short {
			t.Errorf("rule %d labels = %v, want severity %s windows %s/%s", i, labels, w.severity, w.long, w.short)
		}
		if labels["service"] != testClaimName || labels["owner"] != testOwner || labels["tier"] != "public" {
			t.Errorf("rule %d labels = %v, want service, owner and tier", i, labels)
		}

		wantExpr := "sli:http_availability:error_ratio_rate" + w.long + `{service="url-shortener"} > ` + w.threshold +
			"\nand\n" +
			"sli:http_availability:error_ratio_rate" + w.short + `{service="url-shortener"} > ` + w.threshold
		if got := rule.Expr.String(); got != wantExpr {
			t.Errorf("rule %d expr =\n%s\nwant\n%s", i, got, wantExpr)
		}

		annotations := rule.Annotations
		if annotations["runbook_url"] != RunbookURL || annotations["runbook"] != testRunbook {
			t.Errorf("rule %d runbook annotations = %q, %q", i, annotations["runbook_url"], annotations["runbook"])
		}
		if annotations["owner"] != testOwner {
			t.Errorf("rule %d owner annotation = %q, want team-links", i, annotations["owner"])
		}
	}

	if desc := rules[0].Annotations["description"]; !strings.Contains(desc, "lasts about 47 hours") {
		t.Errorf("page alert description = %q, want the 28d budget to last about 47 hours at 14.4x", desc)
	}
	if desc := rules[1].Annotations["description"]; !strings.Contains(desc, "lasts about 4.7 days") {
		t.Errorf("6x alert description = %q, want about 4.7 days", desc)
	}
}

func TestAlertThresholdsScaleWithTheObjective(t *testing.T) {
	sc := newClaim(platformv1alpha1.SLIHTTPLatency)
	sc.Spec.SLO.Objective = "99.9"
	rules, err := AlertRules(sc, testRunbook)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"> 0.0144", "> 0.006", "> 0.003", "> 0.001"} {
		expr := rules[i].Expr.String()
		if strings.Count(expr, want+"\n")+strings.Count(expr, want) < 2 || !strings.Contains(expr, "sli:http_latency:") {
			t.Errorf("rule %d expr = %q, want both windows compared %s on the latency SLI", i, expr, want)
		}
	}
}

func TestAlertRulesRejectInvalidSLOs(t *testing.T) {
	badObjective := newClaim(platformv1alpha1.SLIHTTPAvailability)
	badObjective.Spec.SLO.Objective = "100"
	badWindow := newClaim(platformv1alpha1.SLIHTTPAvailability)
	badWindow.Spec.SLO.Window = "4w"

	for name, sc := range map[string]*platformv1alpha1.ServiceClaim{"objective 100": badObjective, "window 4w": badWindow} {
		if _, err := AlertRules(sc, testRunbook); err == nil {
			t.Errorf("%s: AlertRules returned no error", name)
		}
	}
}

func TestSLOWindow(t *testing.T) {
	for window, want := range map[string]time.Duration{"7d": 168 * time.Hour, "28d": 672 * time.Hour, "30d": 720 * time.Hour} {
		got, err := SLOWindow(window)
		if err != nil || got != want {
			t.Errorf("SLOWindow(%q) = %v, %v; want %v", window, got, err, want)
		}
	}
	for _, bad := range []string{"", "28", "d", "0d", "2w"} {
		if _, err := SLOWindow(bad); err == nil {
			t.Errorf("SLOWindow(%q) returned no error", bad)
		}
	}
}
