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
	"maps"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	testClaimName = "url-shortener"
	testOwner     = "team-links"
	testNamespace = "svc-url-shortener"
)

var defaultGoodStatuses = []int32{200, 201, 204, 301, 302, 304, 400, 404}

// newClaim returns a claim with the given SLI, with the defaults the API server fills in.
func newClaim(sliType string) *platformv1alpha1.ServiceClaim {
	return &platformv1alpha1.ServiceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: testClaimName, Namespace: "platform-claims"},
		Spec: platformv1alpha1.ServiceClaimSpec{
			Owner: testOwner,
			Image: "testsvc:0.1.0",
			Port:  8080,
			Tier:  platformv1alpha1.TierPublic,
			SLI: platformv1alpha1.SLISpec{
				Type:             sliType,
				GoodStatuses:     defaultGoodStatuses,
				LatencyThreshold: "250ms",
			},
			SLO:   platformv1alpha1.SLOSpec{Objective: "99.5", Window: "28d"},
			Scale: platformv1alpha1.ScaleSpec{Min: 2, Max: 5},
		},
	}
}

func TestRecordName(t *testing.T) {
	if got := RecordName(platformv1alpha1.SLIHTTPAvailability, "5m"); got != "sli:http_availability:error_ratio_rate5m" {
		t.Errorf("RecordName(availability, 5m) = %q", got)
	}
	if got := RecordName(platformv1alpha1.SLIHTTPLatency, "3d"); got != "sli:http_latency:error_ratio_rate3d" {
		t.Errorf("RecordName(latency, 3d) = %q", got)
	}
}

func TestRecordingRules(t *testing.T) {
	for _, sliType := range []string{platformv1alpha1.SLIHTTPAvailability, platformv1alpha1.SLIHTTPLatency} {
		t.Run(sliType, func(t *testing.T) {
			sc := newClaim(sliType)
			rules, err := RecordingRules(sc, testNamespace)
			if err != nil {
				t.Fatalf("RecordingRules: %v", err)
			}

			wantWindows := []string{"5m", "30m", "1h", "2h", "6h", "1d", "3d"}
			if len(rules) != len(wantWindows) {
				t.Fatalf("got %d rules, want one per window %v", len(rules), wantWindows)
			}
			wantLabels := map[string]string{"service": testClaimName, "owner": testOwner, "tier": "public"}
			for i, rule := range rules {
				window := wantWindows[i]
				if rule.Record != RecordName(sliType, window) {
					t.Errorf("rule %d records %q, want %q", i, rule.Record, RecordName(sliType, window))
				}
				if rule.Alert != "" || rule.For != nil {
					t.Errorf("%s: recording rule has alert fields set", rule.Record)
				}
				if !maps.Equal(rule.Labels, wantLabels) {
					t.Errorf("%s labels = %v, want %v", rule.Record, rule.Labels, wantLabels)
				}
				expr := rule.Expr.String()
				if strings.Count(expr, "["+window+"]") != 2 {
					t.Errorf("%s: want both rates over [%s], got:\n%s", rule.Record, window, expr)
				}
				if strings.Count(expr, `namespace="svc-url-shortener"`) != 2 {
					t.Errorf("%s: want both selectors scoped to the claim's namespace, got:\n%s", rule.Record, expr)
				}
			}
		})
	}
}

func TestErrorRatioExprAvailability(t *testing.T) {
	expr, err := ErrorRatioExpr(newClaim(platformv1alpha1.SLIHTTPAvailability).Spec.SLI, testNamespace, "5m")
	if err != nil {
		t.Fatal(err)
	}
	want := `(sum(rate(http_requests_total{namespace="svc-url-shortener",code!~"200|201|204|301|302|304|400|404"}[5m])) or vector(0))` +
		"\n/\n" + `sum(rate(http_requests_total{namespace="svc-url-shortener"}[5m]))`
	if expr != want {
		t.Errorf("expr =\n%s\nwant\n%s", expr, want)
	}
}

func TestErrorRatioExprLatency(t *testing.T) {
	expr, err := ErrorRatioExpr(newClaim(platformv1alpha1.SLIHTTPLatency).Spec.SLI, testNamespace, "1h")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		`http_request_duration_seconds_bucket{namespace="svc-url-shortener",le="0.25"}[1h]`,
		`http_request_duration_seconds_count{namespace="svc-url-shortener"}[1h]`,
		"1 - (",
	} {
		if !strings.Contains(expr, part) {
			t.Errorf("expr is missing %q:\n%s", part, expr)
		}
	}
}

func TestLatencyBucket(t *testing.T) {
	tests := map[string]string{"250ms": "0.25", "1s": "1.0", "1500ms": "1.5", "2.5s": "2.5", "50ms": "0.05"}
	for threshold, want := range tests {
		got, err := LatencyBucket(threshold)
		if err != nil || got != want {
			t.Errorf("LatencyBucket(%q) = %q, %v; want %q", threshold, got, err, want)
		}
	}
	for _, bad := range []string{"", "fast", "0s", "-1s"} {
		if _, err := LatencyBucket(bad); err == nil {
			t.Errorf("LatencyBucket(%q) returned no error", bad)
		}
	}
}

func TestRecordingRulesRejectInvalidSLIs(t *testing.T) {
	noGood := newClaim(platformv1alpha1.SLIHTTPAvailability)
	noGood.Spec.SLI.GoodStatuses = nil
	badThreshold := newClaim(platformv1alpha1.SLIHTTPLatency)
	badThreshold.Spec.SLI.LatencyThreshold = "soon"
	unknown := newClaim("grpc-availability")

	for name, sc := range map[string]*platformv1alpha1.ServiceClaim{
		"empty goodStatuses": noGood, "unparseable threshold": badThreshold, "unknown SLI type": unknown,
	} {
		if _, err := RecordingRules(sc, testNamespace); err == nil {
			t.Errorf("%s: RecordingRules returned no error", name)
		}
	}
}
