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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// Platform-owned workload settings. A claim cannot override any of them (DECISIONS.md, ADR-001).
const (
	containerName = "app"
	rolloutKind   = "Rollout"

	// runAsUser is the non-root UID every container runs as: distroless's "nonroot" user.
	runAsUser int64 = 65532

	readinessPath = "/readyz"
	livenessPath  = "/healthz"

	probePeriodSeconds          int32 = 10
	probeFailureThreshold       int32 = 3
	livenessInitialDelaySeconds int32 = 10

	// canaryPause is long enough for the canary analysis to see a bad version: a scrape, a
	// recording rule evaluation and an analysis measurement are each up to 30s apart
	// (DECISIONS.md, ADR-022).
	canaryPause = "2m"

	// analysisStartingStep starts the canary analysis at the first pause, once 20% of the
	// replicas run the new version and take traffic.
	analysisStartingStep int32 = 1

	publicMinReplicas int32 = 2

	mebibyte = 1 << 20
)

// MinReplicas is the replica floor a claim's tier enforces: public never runs fewer than
// two pods, internal honours scale.min, and batch always runs exactly one.
func MinReplicas(sc *platformv1alpha1.ServiceClaim) int32 {
	switch sc.Spec.Tier {
	case platformv1alpha1.TierPublic:
		return max(sc.Spec.Scale.Min, publicMinReplicas)
	case platformv1alpha1.TierInternal:
		return sc.Spec.Scale.Min
	default:
		return 1
	}
}

// HasAutoscaling reports whether a claim's tier gets an HPA, and so a Service and a PDB.
func HasAutoscaling(sc *platformv1alpha1.ServiceClaim) bool {
	return sc.Spec.Tier == platformv1alpha1.TierPublic || sc.Spec.Tier == platformv1alpha1.TierInternal
}

// BuildRollout returns the claim's workload: an Argo Rollout that moves to a new version
// through the platform's canary steps, splitting by replica count rather than by routed
// traffic. Unless the claim is batch, an analysis of its SLI runs through the canary and aborts
// it when the error ratio passes 5%. When the tier has an HPA, spec.replicas is left unset so the HPA owns the replica
// count and the controller never reverts a scaling decision (DECISIONS.md, ADR-008). A claim with
// storage also gives its pods the AWS identity of its role (ADR-025).
func BuildRollout(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) *rolloutsv1alpha1.Rollout {
	rollout := &rolloutsv1alpha1.Rollout{
		TypeMeta:   metav1.TypeMeta{APIVersion: rolloutsv1alpha1.SchemeGroupVersion.String(), Kind: rolloutKind},
		ObjectMeta: objectMeta(sc, sc.Name),
		Spec: rolloutsv1alpha1.RolloutSpec{
			Selector: &metav1.LabelSelector{MatchLabels: SelectorLabels(sc)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: Labels(sc)},
				Spec:       podSpec(sc, storage),
			},
			Strategy: rolloutsv1alpha1.RolloutStrategy{
				Canary: &rolloutsv1alpha1.CanaryStrategy{Steps: canarySteps()},
			},
		},
	}
	if HasCanaryAnalysis(sc) {
		rollout.Spec.Strategy.Canary.Analysis = canaryAnalysis(sc)
	}
	if !HasAutoscaling(sc) {
		rollout.Spec.Replicas = new(MinReplicas(sc))
	}
	return rollout
}

// canaryAnalysis runs the claim's AnalysisTemplate in the background from the first pause until
// the rollout finishes. A failed measurement aborts the rollout, which scales the canary back
// down and leaves the stable version serving.
func canaryAnalysis(sc *platformv1alpha1.ServiceClaim) *rolloutsv1alpha1.RolloutAnalysisBackground {
	return &rolloutsv1alpha1.RolloutAnalysisBackground{
		RolloutAnalysis: rolloutsv1alpha1.RolloutAnalysis{
			Templates: []rolloutsv1alpha1.AnalysisTemplateRef{{TemplateName: CanaryAnalysisName(sc)}},
		},
		StartingStep: new(analysisStartingStep),
	}
}

// canarySteps moves 20% of replicas to the new version, waits 2m, moves to 50%, waits 2m,
// then completes the rollout.
func canarySteps() []rolloutsv1alpha1.CanaryStep {
	pause := func() *rolloutsv1alpha1.RolloutPause {
		return &rolloutsv1alpha1.RolloutPause{Duration: new(intstr.FromString(canaryPause))}
	}
	return []rolloutsv1alpha1.CanaryStep{
		{SetWeight: new(int32(20))},
		{Pause: pause()},
		{SetWeight: new(int32(50))},
		{Pause: pause()},
		{SetWeight: new(int32(100))},
	}
}

// podSpec is the pod every claim runs: one container, non-root, read-only root filesystem,
// no capabilities, fixed resources, and HTTP probes on the named port. With storage, it also
// carries the claim's AWS identity.
func podSpec(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) corev1.PodSpec {
	spec := corev1.PodSpec{
		ServiceAccountName: sc.Name,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   new(true),
			RunAsUser:      new(runAsUser),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Containers: []corev1.Container{{
			Name:  containerName,
			Image: sc.Spec.Image,
			Ports: []corev1.ContainerPort{{
				Name:          PortName,
				ContainerPort: sc.Spec.Port,
				Protocol:      corev1.ProtocolTCP,
			}},
			Resources: containerResources(),
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: new(false),
				ReadOnlyRootFilesystem:   new(true),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			ReadinessProbe: httpProbe(readinessPath, 0),
			LivenessProbe:  httpProbe(livenessPath, livenessInitialDelaySeconds),
		}},
	}
	if HasStorage(sc, storage) {
		withStorageIdentity(&spec, sc, storage)
	}
	return spec
}

// containerResources requests 50m CPU and 64Mi memory, and limits the container to 250m
// CPU and 128Mi memory.
func containerResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(50, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(64*mebibyte, resource.BinarySI),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(250, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(128*mebibyte, resource.BinarySI),
		},
	}
}

// httpProbe checks path on the container's named port every 10s.
func httpProbe(path string, initialDelaySeconds int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromString(PortName)},
		},
		InitialDelaySeconds: initialDelaySeconds,
		PeriodSeconds:       probePeriodSeconds,
		FailureThreshold:    probeFailureThreshold,
	}
}
