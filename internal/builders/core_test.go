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
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	testClaimName      = "url-shortener"
	testClaimNamespace = "platform-claims"
	testNamespace      = "svc-url-shortener"
)

// newClaim returns a valid claim of the given tier, with defaults filled in as the API
// server would store it.
func newClaim(tier string) *platformv1alpha1.ServiceClaim {
	return &platformv1alpha1.ServiceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: testClaimName, Namespace: testClaimNamespace},
		Spec: platformv1alpha1.ServiceClaimSpec{
			Owner: "team-links",
			Image: "paved-demo-app:0.1.0",
			Port:  8080,
			Tier:  tier,
			SLI:   platformv1alpha1.SLISpec{Type: "http-availability"},
			SLO:   platformv1alpha1.SLOSpec{Objective: "99.5", Window: "28d"},
			Scale: platformv1alpha1.ScaleSpec{Min: 1, Max: 4},
		},
	}
}

func TestLabels(t *testing.T) {
	got := Labels(newClaim(platformv1alpha1.TierPublic))
	want := map[string]string{
		LabelName:           testClaimName,
		LabelManagedBy:      ManagedBy,
		LabelClaim:          testClaimName,
		LabelClaimNamespace: testClaimNamespace,
		LabelOwner:          "team-links",
	}
	if !maps.Equal(got, want) {
		t.Errorf("Labels() = %v, want %v", got, want)
	}
}

func TestBuildNamespace(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	ns := BuildNamespace(sc)

	if ns.Name != testNamespace {
		t.Errorf("name = %q, want %q", ns.Name, testNamespace)
	}
	if ns.Namespace != "" {
		t.Errorf("namespace = %q, want empty: Namespace is cluster-scoped", ns.Namespace)
	}
	if !maps.Equal(ns.Labels, Labels(sc)) {
		t.Errorf("labels = %v, want %v", ns.Labels, Labels(sc))
	}
	if len(ns.OwnerReferences) != 0 {
		t.Errorf("owner references = %v, want none", ns.OwnerReferences)
	}
}

func TestNamespacedCoreObjects(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	objects := map[string]metav1.Object{
		"ServiceAccount": BuildServiceAccount(sc),
		"Service":        BuildService(sc),
		"Ingress":        BuildIngress(sc),
		"NetworkPolicy":  BuildNetworkPolicy(sc),
	}
	for kind, obj := range objects {
		t.Run(kind, func(t *testing.T) {
			if obj.GetNamespace() != testNamespace {
				t.Errorf("namespace = %q, want %q", obj.GetNamespace(), testNamespace)
			}
			if obj.GetName() != testClaimName {
				t.Errorf("name = %q, want %q", obj.GetName(), testClaimName)
			}
			if !maps.Equal(obj.GetLabels(), Labels(sc)) {
				t.Errorf("labels = %v, want %v", obj.GetLabels(), Labels(sc))
			}
			if len(obj.GetOwnerReferences()) != 0 {
				t.Errorf("owner references = %v, want none", obj.GetOwnerReferences())
			}
		})
	}
}

func TestBuildService(t *testing.T) {
	svc := BuildService(newClaim(platformv1alpha1.TierInternal))

	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type = %q, want ClusterIP", svc.Spec.Type)
	}
	if !maps.Equal(svc.Spec.Selector, map[string]string{LabelName: testClaimName}) {
		t.Errorf("selector = %v", svc.Spec.Selector)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(svc.Spec.Ports))
	}
	port := svc.Spec.Ports[0]
	if port.Port != ServicePort || port.TargetPort.StrVal != PortName || port.Name != PortName {
		t.Errorf("port = %+v, want %d -> %q named %q", port, ServicePort, PortName, PortName)
	}
}

func TestBuildIngress(t *testing.T) {
	ing := BuildIngress(newClaim(platformv1alpha1.TierPublic))

	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != IngressClassName {
		t.Errorf("ingressClassName = %v, want %q", ing.Spec.IngressClassName, IngressClassName)
	}
	if len(ing.Spec.Rules) != 1 || ing.Spec.Rules[0].HTTP == nil || len(ing.Spec.Rules[0].HTTP.Paths) != 1 {
		t.Fatalf("want exactly one rule with one path, got %+v", ing.Spec.Rules)
	}
	rule := ing.Spec.Rules[0]
	if rule.Host != "url-shortener.localhost" {
		t.Errorf("host = %q, want url-shortener.localhost", rule.Host)
	}
	path := rule.HTTP.Paths[0]
	if path.Path != "/" || path.PathType == nil || *path.PathType != networkingv1.PathTypePrefix {
		t.Errorf("path = %q (%v), want / (Prefix)", path.Path, path.PathType)
	}
	backend := path.Backend.Service
	if backend == nil || backend.Name != testClaimName || backend.Port.Name != PortName {
		t.Errorf("backend = %+v, want service %q port %q", backend, testClaimName, PortName)
	}
}

func TestBuildNetworkPolicy(t *testing.T) {
	tests := []struct {
		tier              string
		wantNamespaces    []string
		wantSameNamespace bool
	}{
		{platformv1alpha1.TierPublic, []string{MonitoringNamespace, PlatformNamespace, IngressNamespace}, true},
		{platformv1alpha1.TierInternal, []string{MonitoringNamespace, PlatformNamespace}, true},
		{platformv1alpha1.TierBatch, []string{MonitoringNamespace}, false},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			np := BuildNetworkPolicy(newClaim(tt.tier))

			if !slices.Equal(np.Spec.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}) {
				t.Errorf("policyTypes = %v, want only Ingress so egress stays open", np.Spec.PolicyTypes)
			}
			if len(np.Spec.PodSelector.MatchLabels) != 0 || len(np.Spec.PodSelector.MatchExpressions) != 0 {
				t.Errorf("podSelector = %+v, want every pod in the namespace", np.Spec.PodSelector)
			}

			var namespaces []string
			sameNamespace := false
			for _, rule := range np.Spec.Ingress {
				for _, peer := range rule.From {
					switch {
					case peer.NamespaceSelector != nil:
						ns := peer.NamespaceSelector.MatchLabels[namespaceNameLabel]
						namespaces = append(namespaces, ns)
						restricted := ns == MonitoringNamespace || ns == IngressNamespace
						if restricted && (len(rule.Ports) != 1 || rule.Ports[0].Port.IntVal != 8080) {
							t.Errorf("rule for %q ports = %+v, want only 8080", ns, rule.Ports)
						}
					case peer.PodSelector != nil:
						sameNamespace = true
					}
				}
			}
			if !slices.Equal(namespaces, tt.wantNamespaces) {
				t.Errorf("allowed namespaces = %v, want %v", namespaces, tt.wantNamespaces)
			}
			if sameNamespace != tt.wantSameNamespace {
				t.Errorf("same-namespace traffic allowed = %v, want %v", sameNamespace, tt.wantSameNamespace)
			}
		})
	}
}
