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
	"testing"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// wantAnalysisName is the AnalysisTemplate name every test claim should get.
const wantAnalysisName = testClaimName + "-canary"

func TestBuildAnalysisTemplate(t *testing.T) {
	template := BuildAnalysisTemplate(newClaim(platformv1alpha1.TierPublic))
	if template.Name != wantAnalysisName || template.Namespace != testNamespace {
		t.Errorf("AnalysisTemplate is %s/%s, want %s/%s", template.Namespace, template.Name, testNamespace, wantAnalysisName)
	}
	if len(template.Spec.Metrics) != 1 {
		t.Fatalf("metrics = %+v, want exactly one", template.Spec.Metrics)
	}
	metric := template.Spec.Metrics[0]
	if metric.FailureLimit == nil {
		t.Fatal("failureLimit is nil, want 0: the first failed measurement aborts the rollout")
	}
	if metric.Provider.Prometheus == nil {
		t.Fatal("provider.prometheus is nil")
	}

	for _, check := range []struct{ field, got, want string }{
		{"name", metric.Name, "sli-error-ratio"},
		{"interval", string(metric.Interval), "30s"},
		{"failureCondition", metric.FailureCondition, "len(result) > 0 && result[0] > 0.05"},
		{"successCondition", metric.SuccessCondition, ""},
		{"failureLimit", metric.FailureLimit.String(), "0"},
		{"address", metric.Provider.Prometheus.Address, "http://kps-kube-prometheus-stack-prometheus.monitoring.svc:9090"},
		{"query", metric.Provider.Prometheus.Query, `sli:http_availability:error_ratio_rate5m{service="` + testClaimName + `"}`},
	} {
		if check.got != check.want {
			t.Errorf("%s = %q, want %q", check.field, check.got, check.want)
		}
	}
}

func TestBuildAnalysisTemplateReadsTheClaimsOwnSLI(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierInternal)
	sc.Spec.SLI.Type = platformv1alpha1.SLIHTTPLatency

	got := BuildAnalysisTemplate(sc).Spec.Metrics[0].Provider.Prometheus.Query
	if want := `sli:http_latency:error_ratio_rate5m{service="` + testClaimName + `"}`; got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
}

func TestHasCanaryAnalysis(t *testing.T) {
	for tier, want := range map[string]bool{
		platformv1alpha1.TierPublic:   true,
		platformv1alpha1.TierInternal: true,
		platformv1alpha1.TierBatch:    false,
	} {
		if got := HasCanaryAnalysis(newClaim(tier)); got != want {
			t.Errorf("HasCanaryAnalysis(%s) = %t, want %t", tier, got, want)
		}
	}
}
