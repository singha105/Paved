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
	"slices"
	"strconv"
	"strings"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// The metrics a claimed service must expose. README.md ("Service contract") documents them.
const (
	// RequestsMetric counts HTTP requests, with the status code in a label named "code".
	RequestsMetric = "http_requests_total"
	// DurationMetric is a histogram of request latency in seconds. Latency claims need a
	// bucket at exactly their latencyThreshold.
	DurationMetric = "http_request_duration_seconds"
)

// Labels every generated rule carries.
const (
	LabelService = "service"
	LabelOwner   = "owner"
	LabelTier    = "tier"
)

// windows are the windows each SLI error ratio is recorded over: every window the burn-rate
// alerts compare.
var windows = []string{"5m", "30m", "1h", "2h", "6h", "1d", "3d"}

// Windows returns the recorded windows, shortest first.
func Windows() []string {
	return slices.Clone(windows)
}

// RecordName is the recording rule that holds an SLI's error ratio over a window, for
// example sli:http_availability:error_ratio_rate5m.
func RecordName(sliType, window string) string {
	return "sli:" + strings.ReplaceAll(sliType, "-", "_") + ":error_ratio_rate" + window
}

// RuleLabels returns the labels every rule generated for sc carries.
func RuleLabels(sc *platformv1alpha1.ServiceClaim) map[string]string {
	return map[string]string{
		LabelService: sc.Name,
		LabelOwner:   sc.Spec.Owner,
		LabelTier:    sc.Spec.Tier,
	}
}

// RecordingRules returns one rule per window that records the claim's SLI error ratio,
// reading the service's metrics from namespace.
func RecordingRules(sc *platformv1alpha1.ServiceClaim, namespace string) ([]monitoringv1.Rule, error) {
	rules := make([]monitoringv1.Rule, 0, len(windows))
	for _, window := range windows {
		expr, err := ErrorRatioExpr(sc.Spec.SLI, namespace, window)
		if err != nil {
			return nil, err
		}
		rules = append(rules, monitoringv1.Rule{
			Record: RecordName(sc.Spec.SLI.Type, window),
			Expr:   intstr.FromString(expr),
			Labels: RuleLabels(sc),
		})
	}
	return rules, nil
}

// ErrorRatioExpr returns PromQL for the fraction of a service's requests that were bad over
// window:
//   - http-availability: requests whose status code is not in goodStatuses, divided by all
//     requests. "or vector(0)" makes a service that has never failed read 0, not empty.
//   - http-latency: 1 minus the fraction of requests that finished within latencyThreshold.
//
// When the window holds no requests the result is empty or NaN, so nothing is recorded and
// no alert can fire.
func ErrorRatioExpr(sli platformv1alpha1.SLISpec, namespace, window string) (string, error) {
	selector := fmt.Sprintf("namespace=%q", namespace)

	switch sli.Type {
	case platformv1alpha1.SLIHTTPAvailability:
		good, err := goodStatusPattern(sli.GoodStatuses)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"(sum(rate(%[1]s{%[2]s,code!~%[3]q}[%[4]s])) or vector(0))\n/\nsum(rate(%[1]s{%[2]s}[%[4]s]))",
			RequestsMetric, selector, good, window,
		), nil

	case platformv1alpha1.SLIHTTPLatency:
		le, err := LatencyBucket(sli.LatencyThreshold)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"1 - (\n  sum(rate(%[1]s_bucket{%[2]s,le=%[3]q}[%[4]s]))\n  /\n  sum(rate(%[1]s_count{%[2]s}[%[4]s]))\n)",
			DurationMetric, selector, le, window,
		), nil

	default:
		return "", fmt.Errorf("unsupported SLI type %q", sli.Type)
	}
}

// LatencyBucket returns the histogram "le" label value for a latency threshold such as
// "250ms". Prometheus 3 stores classic histogram bucket bounds in float format, so whole
// seconds read "1.0", not "1".
func LatencyBucket(threshold string) (string, error) {
	d, err := time.ParseDuration(threshold)
	if err != nil {
		return "", fmt.Errorf("latencyThreshold %q is not a duration: %w", threshold, err)
	}
	if d <= 0 {
		return "", fmt.Errorf("latencyThreshold %q must be positive", threshold)
	}
	le := strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
	if !strings.Contains(le, ".") {
		le += ".0"
	}
	return le, nil
}

// goodStatusPattern joins status codes into an alternation for a PromQL regex matcher,
// which Prometheus anchors at both ends.
func goodStatusPattern(statuses []int32) (string, error) {
	if len(statuses) == 0 {
		return "", fmt.Errorf("goodStatuses is empty, so every request would count as an error")
	}
	codes := make([]string, 0, len(statuses))
	for _, status := range statuses {
		codes = append(codes, strconv.Itoa(int(status)))
	}
	return strings.Join(codes, "|"), nil
}
