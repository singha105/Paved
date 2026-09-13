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
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// Build returns every object a claim's tier requires. The Namespace comes first, so the
// objects inside it can be created on the same reconcile.
//
//	every tier:        Namespace, ServiceAccount, Rollout, NetworkPolicy, PrometheusRule,
//	                   ServiceMonitor, dashboard ConfigMap
//	public, internal:  + Service, HorizontalPodAutoscaler, PodDisruptionBudget
//	public:            + Ingress
//
// That is 11 objects for public, 10 for internal and 7 for batch. It returns an error when
// the claim's SLI or SLO can't be turned into rules, such as an unparseable latencyThreshold.
func Build(sc *platformv1alpha1.ServiceClaim) ([]client.Object, error) {
	rule, err := BuildPrometheusRule(sc)
	if err != nil {
		return nil, err
	}
	dashboard, err := BuildDashboard(sc)
	if err != nil {
		return nil, err
	}

	objects := []client.Object{
		BuildNamespace(sc),
		BuildServiceAccount(sc),
		BuildRollout(sc),
		BuildNetworkPolicy(sc),
		rule,
		BuildServiceMonitor(sc),
		dashboard,
	}
	if HasAutoscaling(sc) {
		objects = append(objects, BuildService(sc), BuildHPA(sc), BuildPodDisruptionBudget(sc))
	}
	if sc.Spec.Tier == platformv1alpha1.TierPublic {
		objects = append(objects, BuildIngress(sc))
	}
	return objects, nil
}

// ManagedTypes returns an empty object of every kind Build can produce. The controller
// watches and caches exactly these kinds.
func ManagedTypes() []client.Object {
	return []client.Object{
		&corev1.Namespace{},
		&corev1.ServiceAccount{},
		&rolloutsv1alpha1.Rollout{},
		&networkingv1.NetworkPolicy{},
		&monitoringv1.PrometheusRule{},
		&monitoringv1.ServiceMonitor{},
		&corev1.ConfigMap{},
		&corev1.Service{},
		&autoscalingv2.HorizontalPodAutoscaler{},
		&policyv1.PodDisruptionBudget{},
		&networkingv1.Ingress{},
	}
}
