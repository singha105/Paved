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
	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// cpuTargetUtilization is the average CPU use, as a percentage of requests, the HPA aims for.
const cpuTargetUtilization int32 = 70

// BuildHPA scales the claim's Rollout on CPU between the tier's replica floor and scale.max.
// When scale.max is below the floor the floor wins, so the HPA is never rejected for having
// minReplicas above maxReplicas (DECISIONS.md, ADR-008).
func BuildHPA(sc *platformv1alpha1.ServiceClaim) *autoscalingv2.HorizontalPodAutoscaler {
	minReplicas := MinReplicas(sc)
	return &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: autoscalingv2.SchemeGroupVersion.String(),
			Kind:       "HorizontalPodAutoscaler",
		},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: rolloutsv1alpha1.SchemeGroupVersion.String(),
				Kind:       rolloutKind,
				Name:       sc.Name,
			},
			MinReplicas: new(minReplicas),
			MaxReplicas: max(sc.Spec.Scale.Max, minReplicas),
			Metrics: []autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceCPU,
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: new(cpuTargetUtilization),
					},
				},
			}},
		},
	}
}
