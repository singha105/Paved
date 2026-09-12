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
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// SLORuleGroupName is the name of the rule group holding a claim's SLO rules.
func SLORuleGroupName(sc *platformv1alpha1.ServiceClaim) string {
	return sc.Name + ".slo"
}

// BuildPrometheusRule returns the claim's alerting rules. For now it holds one empty group;
// the SLO burn-rate rules arrive with the SLO work.
func BuildPrometheusRule(sc *platformv1alpha1.ServiceClaim) *monitoringv1.PrometheusRule {
	return &monitoringv1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: monitoringv1.SchemeGroupVersion.String(),
			Kind:       monitoringv1.PrometheusRuleKind,
		},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{{Name: SLORuleGroupName(sc)}},
		},
	}
}
