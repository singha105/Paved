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
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// Namespaces that may reach claimed workloads.
const (
	IngressNamespace    = "traefik"
	MonitoringNamespace = "monitoring"
	PlatformNamespace   = "platform-system"
)

// namespaceNameLabel is set by Kubernetes on every namespace to the namespace's own name.
const namespaceNameLabel = "kubernetes.io/metadata.name"

// BuildNetworkPolicy denies all incoming traffic to the claim's pods except:
//   - Prometheus, on the service port, for every tier, so SLO metrics can be scraped;
//   - pods in the same namespace and in the platform namespace, for public and internal;
//   - the ingress controller, on the service port, for public.
//
// Only ingress is restricted. Outbound traffic, DNS included, is left open (DECISIONS.md, ADR-007).
func BuildNetworkPolicy(sc *platformv1alpha1.ServiceClaim) *networkingv1.NetworkPolicy {
	rules := []networkingv1.NetworkPolicyIngressRule{{
		From:  []networkingv1.NetworkPolicyPeer{namespacePeer(MonitoringNamespace)},
		Ports: servicePortOnly(sc),
	}}
	if sc.Spec.Tier == platformv1alpha1.TierPublic || sc.Spec.Tier == platformv1alpha1.TierInternal {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{}},
				namespacePeer(PlatformNamespace),
			},
		})
	}
	if sc.Spec.Tier == platformv1alpha1.TierPublic {
		rules = append(rules, networkingv1.NetworkPolicyIngressRule{
			From:  []networkingv1.NetworkPolicyPeer{namespacePeer(IngressNamespace)},
			Ports: servicePortOnly(sc),
		})
	}

	return &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: networkingv1.SchemeGroupVersion.String(), Kind: "NetworkPolicy"},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     rules,
		},
	}
}

// namespacePeer matches every pod in the named namespace.
func namespacePeer(namespace string) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{namespaceNameLabel: namespace},
		},
	}
}

// servicePortOnly limits a rule to the claim's container port.
func servicePortOnly(sc *platformv1alpha1.ServiceClaim) []networkingv1.NetworkPolicyPort {
	return []networkingv1.NetworkPolicyPort{{
		Protocol: new(corev1.ProtocolTCP),
		Port:     new(intstr.FromInt32(sc.Spec.Port)),
	}}
}
