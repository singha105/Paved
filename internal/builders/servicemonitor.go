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

const (
	metricsPath    = "/metrics"
	scrapeInterval = monitoringv1.Duration("30s")
)

// BuildServiceMonitor tells Prometheus to scrape /metrics on the claim's Service every 30s.
//
// A batch claim has no Service, so its ServiceMonitor matches nothing. It is still created
// because every tier includes one; scraping batch workloads is not designed yet.
func BuildServiceMonitor(sc *platformv1alpha1.ServiceClaim) *monitoringv1.ServiceMonitor {
	return &monitoringv1.ServiceMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: monitoringv1.SchemeGroupVersion.String(),
			Kind:       monitoringv1.ServiceMonitorsKind,
		},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{MatchLabels: SelectorLabels(sc)},
			Endpoints: []monitoringv1.Endpoint{{
				Port:     PortName,
				Path:     metricsPath,
				Interval: scrapeInterval,
			}},
		},
	}
}
