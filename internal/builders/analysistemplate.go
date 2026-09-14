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

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

// Canary analysis settings. Like the canary steps, they belong to the platform
// (DECISIONS.md, ADR-022).
const (
	analysisTemplateKind = "AnalysisTemplate"

	// CanaryMetric is the name of the one measurement a canary analysis takes.
	CanaryMetric = "sli-error-ratio"

	// canaryErrorRatioLimit fails a canary once its service's SLI error ratio passes 5%.
	canaryErrorRatioLimit = "0.05"

	// canaryWindow is the recording rule window the analysis reads: the shortest one, so a bad
	// canary shows up soonest.
	canaryWindow = "5m"

	// canaryInterval is how often the analysis measures.
	canaryInterval = "30s"
)

// HasCanaryAnalysis reports whether a claim's canary is gated on its SLI. A batch claim has no
// Service, so there are no requests to measure.
func HasCanaryAnalysis(sc *platformv1alpha1.ServiceClaim) bool {
	return sc.Spec.Tier != platformv1alpha1.TierBatch
}

// CanaryAnalysisName is the name of a claim's AnalysisTemplate.
func CanaryAnalysisName(sc *platformv1alpha1.ServiceClaim) string {
	return sc.Name + "-canary"
}

// BuildAnalysisTemplate returns the analysis that gates a claim's canary. It reads the same 5m
// SLI recording rule that the burn-rate alerts and the deploy freeze are built on, and fails the
// canary as soon as the service's error ratio passes 5%. A query that returns no data, such as
// for a service with no traffic, doesn't fail it.
func BuildAnalysisTemplate(sc *platformv1alpha1.ServiceClaim) *rolloutsv1alpha1.AnalysisTemplate {
	query := fmt.Sprintf("%s{%s=%q}", slo.RecordName(sc.Spec.SLI.Type, canaryWindow), slo.LabelService, sc.Name)
	return &rolloutsv1alpha1.AnalysisTemplate{
		TypeMeta:   metav1.TypeMeta{APIVersion: rolloutsv1alpha1.SchemeGroupVersion.String(), Kind: analysisTemplateKind},
		ObjectMeta: objectMeta(sc, CanaryAnalysisName(sc)),
		Spec: rolloutsv1alpha1.AnalysisTemplateSpec{
			Metrics: []rolloutsv1alpha1.Metric{{
				Name:     CanaryMetric,
				Interval: canaryInterval,
				// Argo Rollouts hands a Prometheus vector to the condition as a list of values.
				FailureCondition: fmt.Sprintf("len(result) > 0 && result[0] > %s", canaryErrorRatioLimit),
				FailureLimit:     new(intstr.FromInt32(0)),
				Provider: rolloutsv1alpha1.MetricProvider{
					Prometheus: &rolloutsv1alpha1.PrometheusMetric{Address: slo.DefaultPrometheusURL, Query: query},
				},
			}},
		},
	}
}
