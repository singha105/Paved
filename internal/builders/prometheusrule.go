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

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

// SLIRuleGroupName is the rule group holding a claim's SLI recording rules.
func SLIRuleGroupName(sc *platformv1alpha1.ServiceClaim) string {
	return sc.Name + ".sli"
}

// AlertRuleGroupName is the rule group holding a claim's burn-rate alerts.
func AlertRuleGroupName(sc *platformv1alpha1.ServiceClaim) string {
	return sc.Name + ".slo-alerts"
}

// BuildPrometheusRule returns the claim's SLO rules in two groups: recording rules for the
// SLI error ratio over each window, and the burn-rate alerts that compare them against the
// objective (see internal/slo).
func BuildPrometheusRule(sc *platformv1alpha1.ServiceClaim) (*monitoringv1.PrometheusRule, error) {
	namespace := NamespaceName(sc)
	records, err := slo.RecordingRules(sc, namespace)
	if err != nil {
		return nil, fmt.Errorf("building SLI recording rules: %w", err)
	}
	alerts, err := slo.AlertRules(sc, namespace+"/"+RunbookName(sc))
	if err != nil {
		return nil, fmt.Errorf("building burn-rate alerts: %w", err)
	}

	return &monitoringv1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: monitoringv1.SchemeGroupVersion.String(),
			Kind:       monitoringv1.PrometheusRuleKind,
		},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{Name: SLIRuleGroupName(sc), Rules: records},
				{Name: AlertRuleGroupName(sc), Rules: alerts},
			},
		},
	}, nil
}
