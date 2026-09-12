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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

var _ = Describe("ServiceClaim Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		serviceclaim := &platformv1alpha1.ServiceClaim{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ServiceClaim")
			err := k8sClient.Get(ctx, typeNamespacedName, serviceclaim)
			if err != nil && errors.IsNotFound(err) {
				resource := &platformv1alpha1.ServiceClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: platformv1alpha1.ServiceClaimSpec{
						Owner: "team-test",
						Image: "registry.example.com/test:1.0.0",
						Port:  8080,
						Tier:  "internal",
						SLI:   platformv1alpha1.SLISpec{Type: "http-availability"},
						SLO:   platformv1alpha1.SLOSpec{Objective: "99.5"},
						Scale: platformv1alpha1.ScaleSpec{Min: 1, Max: 3},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &platformv1alpha1.ServiceClaim{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ServiceClaim")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should set Ready=False with reason NotImplemented", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ServiceClaimReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking the Ready condition")
			Expect(k8sClient.Get(ctx, typeNamespacedName, serviceclaim)).To(Succeed())
			ready := meta.FindStatusCondition(serviceclaim.Status.Conditions, platformv1alpha1.ConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(ReasonNotImplemented))
			Expect(ready.ObservedGeneration).To(Equal(serviceclaim.Generation))
		})

		It("should do nothing for a ServiceClaim that no longer exists", func() {
			controllerReconciler := &ServiceClaimReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: resourceNamespace},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})
})
