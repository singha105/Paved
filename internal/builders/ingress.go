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
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	// IngressClassName is the ingress controller that serves public claims (DECISIONS.md, ADR-003).
	IngressClassName = "traefik"

	ingressHostSuffix = ".localhost"
)

// IngressHost is the hostname a public claim is served on.
func IngressHost(sc *platformv1alpha1.ServiceClaim) string {
	return sc.Name + ingressHostSuffix
}

// BuildIngress routes <claim>.localhost through Traefik to the claim's Service. There is no
// TLS yet.
func BuildIngress(sc *platformv1alpha1.ServiceClaim) *networkingv1.Ingress {
	return &networkingv1.Ingress{
		TypeMeta:   metav1.TypeMeta{APIVersion: networkingv1.SchemeGroupVersion.String(), Kind: "Ingress"},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: networkingv1.IngressSpec{
			IngressClassName: new(IngressClassName),
			Rules: []networkingv1.IngressRule{{
				Host: IngressHost(sc),
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: new(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: sc.Name,
									Port: networkingv1.ServiceBackendPort{Name: PortName},
								},
							},
						}},
					},
				},
			}},
		},
	}
}
