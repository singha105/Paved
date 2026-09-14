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

package test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
)

const (
	claimNamespace = "default"
	waitTimeout    = 30 * time.Second
	pollInterval   = 250 * time.Millisecond
)

// newClaim returns a valid claim of the given tier, as a developer would write it.
func newClaim(name, tier string) *platformv1alpha1.ServiceClaim {
	return &platformv1alpha1.ServiceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claimNamespace},
		Spec: platformv1alpha1.ServiceClaimSpec{
			Owner: "team-envtest",
			Image: "registry.example.com/app:1.0.0",
			Port:  8080,
			Tier:  tier,
			SLI:   platformv1alpha1.SLISpec{Type: platformv1alpha1.SLIHTTPAvailability},
			SLO:   platformv1alpha1.SLOSpec{Objective: "99.5", Window: "28d"},
			Scale: platformv1alpha1.ScaleSpec{Min: 2, Max: 4},
		},
	}
}

// createClaim creates claim in the API server; the running controller does the rest.
func createClaim(claim *platformv1alpha1.ServiceClaim) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, claim)).To(Succeed())
}

// managedObjects lists every object, of every kind paved manages, that carries the claim's
// ownership labels, across all namespaces.
func managedObjects(g Gomega, claim *platformv1alpha1.ServiceClaim) []unstructured.Unstructured {
	kinds := builders.ManagedTypes()
	objects := make([]unstructured.Unstructured, 0, len(kinds))
	for _, kind := range kinds {
		gvk, err := apiutil.GVKForObject(kind, k8sClient.Scheme())
		g.Expect(err).NotTo(HaveOccurred())
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		g.Expect(k8sClient.List(ctx, list, client.MatchingLabels{
			builders.LabelClaim:          claim.Name,
			builders.LabelClaimNamespace: claim.Namespace,
		})).To(Succeed())
		objects = append(objects, list.Items...)
	}
	return objects
}

// kindCounts counts objects by kind.
func kindCounts(objects []unstructured.Unstructured) map[string]int {
	counts := map[string]int{}
	for _, obj := range objects {
		counts[obj.GetKind()]++
	}
	return counts
}

// resourceVersions maps each object, by kind, namespace and name, to its resourceVersion.
func resourceVersions(objects []unstructured.Unstructured) map[string]string {
	versions := map[string]string{}
	for _, obj := range objects {
		versions[obj.GetKind()+" "+obj.GetNamespace()+"/"+obj.GetName()] = obj.GetResourceVersion()
	}
	return versions
}

// waitForObjects waits until the claim's managed objects number exactly count, and returns them.
func waitForObjects(claim *platformv1alpha1.ServiceClaim, count int) []unstructured.Unstructured {
	GinkgoHelper()
	var objects []unstructured.Unstructured
	Eventually(func(g Gomega) {
		objects = managedObjects(g, claim)
		g.Expect(objects).To(HaveLen(count))
	}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(Succeed())
	return objects
}

var _ = Describe("ServiceClaim controller", func() {
	It("tier: public produces exactly 13 resources, labelled as the claim's, with no owner references", func() {
		claim := newClaim("public-claim", platformv1alpha1.TierPublic)
		createClaim(claim)

		objects := waitForObjects(claim, 13)
		Consistently(func(g Gomega) {
			g.Expect(managedObjects(g, claim)).To(HaveLen(13))
		}).WithTimeout(2*time.Second).WithPolling(pollInterval).Should(Succeed(), "no more objects appear later")

		Expect(kindCounts(objects)).To(Equal(map[string]int{
			"Namespace": 1, "ServiceAccount": 1, "AnalysisTemplate": 1, "Rollout": 1, "NetworkPolicy": 1,
			"PrometheusRule": 1, "ServiceMonitor": 1, "ConfigMap": 2, "Service": 1,
			"HorizontalPodAutoscaler": 1, "PodDisruptionBudget": 1, "Ingress": 1,
		}))
		for _, obj := range objects {
			id := obj.GetKind() + " " + obj.GetNamespace() + "/" + obj.GetName()
			// Children live in svc-<name>, the claim in its own namespace: an owner reference can't
			// cross namespaces, so the labels are the ownership (DECISIONS.md, ADR-006).
			Expect(obj.GetOwnerReferences()).To(BeEmpty(), id)
			Expect(obj.GetLabels()).To(HaveKeyWithValue(builders.LabelManagedBy, builders.ManagedBy), id)
		}
	})

	It("tier: batch produces 8, and no Service or Ingress", func() {
		claim := newClaim("batch-claim", platformv1alpha1.TierBatch)
		createClaim(claim)

		objects := waitForObjects(claim, 8)
		counts := kindCounts(objects)
		Expect(counts).NotTo(HaveKey("Service"))
		Expect(counts).NotTo(HaveKey("Ingress"))
		Expect(counts).To(Equal(map[string]int{
			"Namespace": 1, "ServiceAccount": 1, "Rollout": 1, "NetworkPolicy": 1,
			"PrometheusRule": 1, "ServiceMonitor": 1, "ConfigMap": 2,
		}))
	})

	It("drift: a deleted managed PrometheusRule is recreated", func() {
		claim := newClaim("drift-claim", platformv1alpha1.TierInternal)
		createClaim(claim)
		waitForObjects(claim, 12)

		key := types.NamespacedName{Namespace: builders.NamespaceName(claim), Name: claim.Name}
		rule := &monitoringv1.PrometheusRule{}
		Expect(k8sClient.Get(ctx, key, rule)).To(Succeed())
		Expect(k8sClient.Delete(ctx, rule)).To(Succeed())

		Eventually(func(g Gomega) {
			recreated := &monitoringv1.PrometheusRule{}
			g.Expect(k8sClient.Get(ctx, key, recreated)).To(Succeed())
			g.Expect(recreated.UID).NotTo(Equal(rule.UID), "a new object, not the deleted one")
			g.Expect(recreated.Spec.Groups).NotTo(BeEmpty())
		}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(Succeed())
	})

	It("idempotency: reconciling twice changes no managed object's resourceVersion", func() {
		claim := newClaim("idempotent-claim", platformv1alpha1.TierPublic)
		createClaim(claim)
		waitForObjects(claim, 13)
		Eventually(func(g Gomega) {
			current := &platformv1alpha1.ServiceClaim{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), current)).To(Succeed())
			synced := meta.IsStatusConditionTrue(current.Status.Conditions, platformv1alpha1.ConditionResourcesSynced)
			g.Expect(synced).To(BeTrue())
		}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(Succeed())

		var before map[string]string
		Eventually(func(g Gomega) { before = resourceVersions(managedObjects(g, claim)) }).Should(Succeed())

		request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(claim)}
		for range 2 {
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}

		Eventually(func(g Gomega) {
			g.Expect(resourceVersions(managedObjects(g, claim))).To(Equal(before))
		}).Should(Succeed())
	})

	It("cascade: deleting the claim deletes its namespace, and the finalizer holds the claim until it is gone", func() {
		claim := newClaim("cascade-claim", platformv1alpha1.TierInternal)
		createClaim(claim)
		waitForObjects(claim, 12)
		Expect(k8sClient.Delete(ctx, claim)).To(Succeed())

		By("deleting the claim's namespace, which in a real cluster removes every object in it")
		namespace := &corev1.Namespace{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: builders.NamespaceName(claim)}, namespace)).To(Succeed())
			g.Expect(namespace.DeletionTimestamp).NotTo(BeNil())
		}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(Succeed())

		By("keeping the claim while its namespace is still terminating")
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), &platformv1alpha1.ServiceClaim{})).To(Succeed())
		}).WithTimeout(2 * time.Second).WithPolling(pollInterval).Should(Succeed())

		By("finishing the namespace's deletion, which envtest has no namespace controller to do")
		namespace.Spec.Finalizers = nil
		Expect(k8sClient.SubResource("finalize").Update(ctx, namespace)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{}))
		}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(BeTrue())

		By("releasing the finalizer once the namespace is gone, so the claim is deleted")
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(claim), &platformv1alpha1.ServiceClaim{}))
		}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(BeTrue())
	})

	It("status: observedGeneration tracks metadata.generation after a spec edit", func() {
		claim := newClaim("status-claim", platformv1alpha1.TierInternal)
		createClaim(claim)
		key := client.ObjectKeyFromObject(claim)

		expectObserved := func(generation int64) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				current := &platformv1alpha1.ServiceClaim{}
				g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				g.Expect(current.Generation).To(Equal(generation))
				g.Expect(current.Status.ObservedGeneration).To(Equal(generation))
				synced := meta.FindStatusCondition(current.Status.Conditions, platformv1alpha1.ConditionResourcesSynced)
				g.Expect(synced).NotTo(BeNil())
				g.Expect(synced.ObservedGeneration).To(Equal(generation))
			}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(Succeed())
		}
		expectObserved(1)

		By("raising scale.max, which is a new generation")
		Eventually(func(g Gomega) {
			current := &platformv1alpha1.ServiceClaim{}
			g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
			current.Spec.Scale.Max = 6
			g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
		}).WithTimeout(waitTimeout).WithPolling(pollInterval).Should(Succeed())
		expectObserved(2)

		hpa := &autoscalingv2.HorizontalPodAutoscaler{}
		hpaKey := types.NamespacedName{Namespace: builders.NamespaceName(claim), Name: claim.Name}
		Expect(k8sClient.Get(ctx, hpaKey, hpa)).To(Succeed())
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(6)), "the edit reached the managed objects too")
	})
})
