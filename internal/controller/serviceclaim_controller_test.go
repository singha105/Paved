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

package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
)

// The specs share one claim and run in order through its lifecycle: create, reconcile again,
// repair drift, delete.
var _ = Describe("ServiceClaim Controller", Ordered, func() {
	const (
		claimName      = "test-claim"
		claimNamespace = "default"
	)

	var (
		ctx        = context.Background()
		key        = types.NamespacedName{Name: claimName, Namespace: claimNamespace}
		reconciler *ServiceClaimReconciler
	)

	reconcileClaim := func() reconcile.Result {
		GinkgoHelper()
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	getClaim := func() *platformv1alpha1.ServiceClaim {
		GinkgoHelper()
		claim := &platformv1alpha1.ServiceClaim{}
		Expect(k8sClient.Get(ctx, key, claim)).To(Succeed())
		return claim
	}

	// resourceVersions fetches every object the claim's tier requires and returns each one's
	// resourceVersion, failing if any object is missing.
	resourceVersions := func(claim *platformv1alpha1.ServiceClaim) map[string]string {
		GinkgoHelper()
		versions := map[string]string{}
		objects, err := builders.Build(claim)
		Expect(err).NotTo(HaveOccurred())
		for _, obj := range objects {
			id := fmt.Sprintf("%s %s", obj.GetObjectKind().GroupVersionKind().Kind, client.ObjectKeyFromObject(obj))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed(), "getting %s", id)
			versions[id] = obj.GetResourceVersion()
		}
		return versions
	}

	BeforeAll(func() {
		reconciler = &ServiceClaimReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			// Half the 28d budget spent, burning at 0.1x over the last hour.
			Prometheus: &fakeQuerier{window: queryResult{value: 0.0025}, hour: queryResult{value: 0.0005}},
		}
		Expect(k8sClient.Create(ctx, &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: claimName, Namespace: claimNamespace},
			Spec: platformv1alpha1.ServiceClaimSpec{
				Owner: "team-test",
				Image: "registry.example.com/test:1.0.0",
				Port:  8080,
				Tier:  platformv1alpha1.TierInternal,
				SLI:   platformv1alpha1.SLISpec{Type: "http-availability"},
				SLO:   platformv1alpha1.SLOSpec{Objective: testObjective},
				Scale: platformv1alpha1.ScaleSpec{Min: 1, Max: 3},
			},
		})).To(Succeed())
	})

	It("adds the finalizer, applies the internal tier's 11 resources and reports status", func() {
		result := reconcileClaim()
		Expect(result.RequeueAfter).To(Equal(time.Minute), "the claim must requeue to keep its budget fresh")

		claim := getClaim()
		Expect(controllerutil.ContainsFinalizer(claim, Finalizer)).To(BeTrue())
		Expect(resourceVersions(claim)).To(HaveLen(11))

		synced := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionResourcesSynced)
		Expect(synced).NotTo(BeNil())
		Expect(synced.Status).To(Equal(metav1.ConditionTrue))
		Expect(synced.Reason).To(Equal(ReasonApplied))
		Expect(claim.Status.ManagedResources).To(Equal(11))
		Expect(claim.Status.ObservedGeneration).To(Equal(claim.Generation))
		Expect(claim.Status.LastReconcileTime).NotTo(BeNil())

		Expect(claim.Status.ErrorBudgetRemaining).To(Equal("50.0%"))
		Expect(claim.Status.BurnRate1h).To(Equal("0.10"))
		healthy := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionSLOHealthy)
		Expect(healthy).NotTo(BeNil())
		Expect(healthy.Status).To(Equal(metav1.ConditionTrue))
		Expect(healthy.Reason).To(Equal(ReasonWithinBudget))

		ready := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(ReasonNotImplemented))
	})

	It("clears the budget and reports Unknown when Prometheus is unreachable, without failing the reconcile", func() {
		healthyQuerier := reconciler.Prometheus
		reconciler.Prometheus = &fakeQuerier{
			window: queryResult{err: fmt.Errorf("querying Prometheus: connection refused")},
			hour:   queryResult{err: fmt.Errorf("querying Prometheus: connection refused")},
		}
		DeferCleanup(func() { reconciler.Prometheus = healthyQuerier })

		result := reconcileClaim()
		Expect(result.RequeueAfter).To(Equal(time.Minute))

		claim := getClaim()
		Expect(claim.Status.ErrorBudgetRemaining).To(BeEmpty())
		Expect(claim.Status.BurnRate1h).To(BeEmpty())
		healthy := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionSLOHealthy)
		Expect(healthy).NotTo(BeNil())
		Expect(healthy.Status).To(Equal(metav1.ConditionUnknown))
		Expect(healthy.Reason).To(Equal(ReasonPrometheusUnavailable))
		synced := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionResourcesSynced)
		Expect(synced.Status).To(Equal(metav1.ConditionTrue), "resources must still be reconciled")
	})

	It("leaves every resourceVersion unchanged when it reconciles again", func() {
		reconcileClaim()
		before := resourceVersions(getClaim())

		reconcileClaim()

		Expect(resourceVersions(getClaim())).To(Equal(before))
	})

	It("recreates a managed object that was deleted", func() {
		svc := builders.BuildService(getClaim())
		Expect(k8sClient.Delete(ctx, svc)).To(Succeed())
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(svc), &corev1.Service{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "Service still exists: %v", err)

		reconcileClaim()

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(svc), &corev1.Service{})).To(Succeed())
	})

	It("deletes the namespace, then releases the finalizer once the namespace is gone", func() {
		Expect(k8sClient.Delete(ctx, getClaim())).To(Succeed())

		By("deleting the namespace and keeping the finalizer")
		result := reconcileClaim()
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "svc-" + claimName}, ns)).To(Succeed())
		Expect(ns.DeletionTimestamp).NotTo(BeNil())
		Expect(controllerutil.ContainsFinalizer(getClaim(), Finalizer)).To(BeTrue())

		By("finishing the namespace deletion, which envtest has no namespace controller to do")
		ns.Spec.Finalizers = nil
		Expect(k8sClient.SubResource("finalize").Update(ctx, ns)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), &corev1.Namespace{}))
		}).WithTimeout(10 * time.Second).Should(BeTrue())

		By("releasing the finalizer, which lets the claim go")
		reconcileClaim()
		err := k8sClient.Get(ctx, key, &platformv1alpha1.ServiceClaim{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "claim still exists: %v", err)
	})

	It("does nothing for a ServiceClaim that no longer exists", func() {
		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: claimNamespace},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})
})

var _ = Describe("ServiceClaim Controller with a spec it cannot build", func() {
	It("reports InvalidSpec and applies nothing", func() {
		ctx := context.Background()
		key := types.NamespacedName{Name: "bad-threshold", Namespace: "default"}
		Expect(k8sClient.Create(ctx, &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: platformv1alpha1.ServiceClaimSpec{
				Owner: "team-test",
				Image: "registry.example.com/test:1.0.0",
				Port:  8080,
				Tier:  platformv1alpha1.TierInternal,
				// Passes CRD validation, which doesn't check the duration format.
				SLI:   platformv1alpha1.SLISpec{Type: platformv1alpha1.SLIHTTPLatency, LatencyThreshold: "soon"},
				SLO:   platformv1alpha1.SLOSpec{Objective: testObjective},
				Scale: platformv1alpha1.ScaleSpec{Min: 1, Max: 3},
			},
		})).To(Succeed())

		reconciler := &ServiceClaimReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		claim := &platformv1alpha1.ServiceClaim{}
		Expect(k8sClient.Get(ctx, key, claim)).To(Succeed())
		synced := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionResourcesSynced)
		Expect(synced).NotTo(BeNil())
		Expect(synced.Status).To(Equal(metav1.ConditionFalse))
		Expect(synced.Reason).To(Equal(ReasonInvalidSpec))
		Expect(synced.Message).To(ContainSubstring("latencyThreshold"))
		healthy := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionSLOHealthy)
		Expect(healthy).NotTo(BeNil())
		Expect(healthy.Status).To(Equal(metav1.ConditionUnknown))

		err = k8sClient.Get(ctx, types.NamespacedName{Name: "svc-" + key.Name}, &corev1.Namespace{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "namespace was created for an invalid claim: %v", err)

		By("cleaning up: releasing the finalizer the reconcile added, then deleting the claim")
		Expect(k8sClient.Delete(ctx, claim)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	})
})
