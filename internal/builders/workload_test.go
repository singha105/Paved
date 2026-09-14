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
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// claimWithScale returns a claim of the given tier and scale bounds.
func claimWithScale(tier string, minReplicas, maxReplicas int32) *platformv1alpha1.ServiceClaim {
	sc := newClaim(tier)
	sc.Spec.Scale = platformv1alpha1.ScaleSpec{Min: minReplicas, Max: maxReplicas}
	return sc
}

func TestMinReplicas(t *testing.T) {
	tests := []struct {
		name     string
		tier     string
		scaleMin int32
		want     int32
	}{
		{"public raises min 1 to 2", platformv1alpha1.TierPublic, 1, 2},
		{"public keeps min 3", platformv1alpha1.TierPublic, 3, 3},
		{"internal honours min 1", platformv1alpha1.TierInternal, 1, 1},
		{"batch always runs 1", platformv1alpha1.TierBatch, 3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MinReplicas(claimWithScale(tt.tier, tt.scaleMin, 5)); got != tt.want {
				t.Errorf("MinReplicas() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildRolloutReplicas(t *testing.T) {
	tests := []struct {
		tier string
		want *int32 // nil: the HPA owns the replica count
	}{
		{platformv1alpha1.TierPublic, nil},
		{platformv1alpha1.TierInternal, nil},
		{platformv1alpha1.TierBatch, new(int32(1))},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			got := BuildRollout(newClaim(tt.tier), nil).Spec.Replicas
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("replicas = %d, want unset so the HPA owns it", *got)
			case tt.want != nil && (got == nil || *got != *tt.want):
				t.Errorf("replicas = %v, want %d", got, *tt.want)
			}
		})
	}
}

func TestBuildRolloutCanarySteps(t *testing.T) {
	canary := BuildRollout(newClaim(platformv1alpha1.TierPublic), nil).Spec.Strategy.Canary
	if canary == nil {
		t.Fatal("strategy.canary is nil")
	}
	if canary.TrafficRouting != nil {
		t.Errorf("trafficRouting = %+v, want nil: canary splits by replica count", canary.TrafficRouting)
	}

	var got []string
	for _, step := range canary.Steps {
		switch {
		case step.SetWeight != nil:
			got = append(got, fmt.Sprintf("weight %d", *step.SetWeight))
		case step.Pause != nil && step.Pause.Duration != nil:
			got = append(got, "pause "+step.Pause.Duration.String())
		default:
			got = append(got, "unexpected step")
		}
	}
	want := []string{"weight 20", "pause 2m", "weight 50", "pause 2m", "weight 100"}
	if len(got) != len(want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d = %q, want %q (all steps: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildRolloutPodSpec(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	rollout := BuildRollout(sc, nil)
	pod := rollout.Spec.Template.Spec

	if !maps.Equal(rollout.Spec.Selector.MatchLabels, SelectorLabels(sc)) {
		t.Errorf("selector = %v, want %v", rollout.Spec.Selector.MatchLabels, SelectorLabels(sc))
	}
	if !maps.Equal(rollout.Spec.Template.Labels, Labels(sc)) {
		t.Errorf("pod labels = %v, want %v", rollout.Spec.Template.Labels, Labels(sc))
	}
	if pod.ServiceAccountName != testClaimName {
		t.Errorf("serviceAccountName = %q, want %q", pod.ServiceAccountName, testClaimName)
	}
	psc := pod.SecurityContext
	if psc == nil || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot || psc.RunAsUser == nil || *psc.RunAsUser != 65532 {
		t.Errorf("pod securityContext = %+v, want runAsNonRoot with UID 65532", psc)
	}

	if len(pod.Containers) != 1 {
		t.Fatalf("got %d containers, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]
	if c.Image != sc.Spec.Image {
		t.Errorf("image = %q, want %q", c.Image, sc.Spec.Image)
	}
	if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 8080 || c.Ports[0].Name != PortName {
		t.Errorf("ports = %+v, want 8080 named %q", c.Ports, PortName)
	}

	csc := c.SecurityContext
	switch {
	case csc == nil:
		t.Error("container securityContext is nil")
	case csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation:
		t.Error("allowPrivilegeEscalation must be false")
	case csc.ReadOnlyRootFilesystem == nil || !*csc.ReadOnlyRootFilesystem:
		t.Error("readOnlyRootFilesystem must be true")
	case csc.Capabilities == nil || len(csc.Capabilities.Drop) != 1 || csc.Capabilities.Drop[0] != "ALL":
		t.Errorf("capabilities = %+v, want drop ALL", csc.Capabilities)
	}

	wantResources := map[string]string{
		"requests.cpu": "50m", "requests.memory": "64Mi",
		"limits.cpu": "250m", "limits.memory": "128Mi",
	}
	gotResources := map[string]string{
		"requests.cpu":    quantity(c.Resources.Requests, corev1.ResourceCPU),
		"requests.memory": quantity(c.Resources.Requests, corev1.ResourceMemory),
		"limits.cpu":      quantity(c.Resources.Limits, corev1.ResourceCPU),
		"limits.memory":   quantity(c.Resources.Limits, corev1.ResourceMemory),
	}
	if !maps.Equal(gotResources, wantResources) {
		t.Errorf("resources = %v, want %v", gotResources, wantResources)
	}

	for name, probe := range map[string]*corev1.Probe{"/readyz": c.ReadinessProbe, "/healthz": c.LivenessProbe} {
		if probe == nil || probe.HTTPGet == nil {
			t.Errorf("probe for %s is missing", name)
			continue
		}
		if probe.HTTPGet.Path != name || probe.HTTPGet.Port.StrVal != PortName {
			t.Errorf("probe = %s on %q, want %s on %q", probe.HTTPGet.Path, probe.HTTPGet.Port.String(), name, PortName)
		}
	}
}

func TestBuildHPA(t *testing.T) {
	tests := []struct {
		name               string
		tier               string
		scaleMin, scaleMax int32
		wantMin, wantMax   int32
	}{
		{"public floor raises min", platformv1alpha1.TierPublic, 1, 4, 2, 4},
		{"public floor raises max when max is below it", platformv1alpha1.TierPublic, 1, 1, 2, 2},
		{"internal uses the claim's bounds", platformv1alpha1.TierInternal, 3, 5, 3, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hpa := BuildHPA(claimWithScale(tt.tier, tt.scaleMin, tt.scaleMax))

			if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != tt.wantMin || hpa.Spec.MaxReplicas != tt.wantMax {
				t.Errorf("replicas = %v..%d, want %d..%d", hpa.Spec.MinReplicas, hpa.Spec.MaxReplicas, tt.wantMin, tt.wantMax)
			}
			ref := hpa.Spec.ScaleTargetRef
			if ref.Kind != rolloutKind || ref.Name != testClaimName || ref.APIVersion != "argoproj.io/v1alpha1" {
				t.Errorf("scaleTargetRef = %+v, want the claim's Rollout", ref)
			}
			if len(hpa.Spec.Metrics) != 1 || hpa.Spec.Metrics[0].Resource == nil ||
				hpa.Spec.Metrics[0].Resource.Target.AverageUtilization == nil ||
				*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization != 70 {
				t.Errorf("metrics = %+v, want CPU at 70%%", hpa.Spec.Metrics)
			}
		})
	}
}

func TestBuildPodDisruptionBudget(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierInternal)
	pdb := BuildPodDisruptionBudget(sc)

	if pdb.Spec.MinAvailable != nil {
		t.Errorf("minAvailable = %v, want unset", pdb.Spec.MinAvailable)
	}
	if pdb.Spec.MaxUnavailable == nil || pdb.Spec.MaxUnavailable.IntValue() != 1 {
		t.Errorf("maxUnavailable = %v, want 1", pdb.Spec.MaxUnavailable)
	}
	if pdb.Spec.Selector == nil || !maps.Equal(pdb.Spec.Selector.MatchLabels, SelectorLabels(sc)) {
		t.Errorf("selector = %v, want %v", pdb.Spec.Selector, SelectorLabels(sc))
	}
}

func quantity(list corev1.ResourceList, name corev1.ResourceName) string {
	q, ok := list[name]
	if !ok {
		return "<unset>"
	}
	return q.String()
}

func TestBuildRolloutCanaryAnalysis(t *testing.T) {
	for _, tier := range []string{platformv1alpha1.TierPublic, platformv1alpha1.TierInternal} {
		analysis := BuildRollout(newClaim(tier), nil).Spec.Strategy.Canary.Analysis
		if analysis == nil {
			t.Fatalf("%s: the canary has no analysis", tier)
		}
		if len(analysis.Templates) != 1 || analysis.Templates[0].TemplateName != wantAnalysisName {
			t.Errorf("%s: analysis templates = %+v, want only %s", tier, analysis.Templates, wantAnalysisName)
		}
		if analysis.StartingStep == nil || *analysis.StartingStep != 1 {
			t.Errorf("%s: analysis startingStep = %v, want 1, the first pause", tier, analysis.StartingStep)
		}
	}
	if analysis := BuildRollout(newClaim(platformv1alpha1.TierBatch), nil).Spec.Strategy.Canary.Analysis; analysis != nil {
		t.Errorf("batch canary has analysis %+v, want none: a batch claim has no traffic to measure", analysis)
	}
}
