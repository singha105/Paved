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

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

var _ = Describe("ServiceClaim webhook", func() {
	It("is called by the API server, which admits a new claim and an update to it", func() {
		claim := &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "admitted", Namespace: "default"},
			Spec: platformv1alpha1.ServiceClaimSpec{
				Owner: "team-links",
				Image: "testsvc:0.1.1",
				Port:  8080,
				Tier:  platformv1alpha1.TierPublic,
				SLI:   platformv1alpha1.SLISpec{Type: platformv1alpha1.SLIHTTPAvailability},
				SLO:   platformv1alpha1.SLOSpec{Objective: "99.5", Window: "28d"},
				Scale: platformv1alpha1.ScaleSpec{Min: 2, Max: 5},
			},
		}
		Expect(k8sClient.Create(ctx, claim)).To(Succeed())

		claim.Spec.Image = "testsvc:0.2.0"
		Expect(k8sClient.Update(ctx, claim)).To(Succeed())

		Expect(k8sClient.Delete(ctx, claim)).To(Succeed())
	})
})
