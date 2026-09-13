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
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	currentImage = "k3d-paved-registry:5001/testsvc:0.1.1"
	nextImage    = "k3d-paved-registry:5001/testsvc:0.2.0"

	// actor is the user every admission request in these tests comes from.
	actor = "alice@example.com"
)

// testClaim returns a valid claim named name in the default namespace.
func testClaim(name string) *platformv1alpha1.ServiceClaim {
	return &platformv1alpha1.ServiceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: platformv1alpha1.ServiceClaimSpec{
			Owner: "team-links",
			Image: currentImage,
			Port:  8080,
			Tier:  platformv1alpha1.TierPublic,
			SLI:   platformv1alpha1.SLISpec{Type: platformv1alpha1.SLIHTTPAvailability},
			SLO:   platformv1alpha1.SLOSpec{Objective: "99.5", Window: "28d"},
			Scale: platformv1alpha1.ScaleSpec{Min: 2, Max: 5},
		},
	}
}

// freeze writes the status the controller records for a claim with no error budget left.
func freeze(claim *platformv1alpha1.ServiceClaim) {
	claim.Status.Conditions = []metav1.Condition{{
		Type:               platformv1alpha1.ConditionDeploysFrozen,
		Status:             metav1.ConditionTrue,
		Reason:             "BudgetExhausted",
		Message:            "No 28d error budget left; image changes are rejected until 5.0% of it is back",
		LastTransitionTime: metav1.Now(),
	}}
	claim.Status.ErrorBudgetRemaining = "0.0%"
	claim.Status.BurnRate1h = "14.20"
}

// admissionContext carries the admission request that the webhook server hands the validator.
func admissionContext(dryRun bool) context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: actor},
			DryRun:   &dryRun,
		},
	})
}

var _ = Describe("ServiceClaim webhook", func() {
	Describe("ValidateUpdate", func() {
		const reason = "roll back to the last good release"
		var (
			recorder  *events.FakeRecorder
			validator *ServiceClaimValidator
			oldClaim  *platformv1alpha1.ServiceClaim
			newClaim  *platformv1alpha1.ServiceClaim
		)

		BeforeEach(func() {
			recorder = events.NewFakeRecorder(5)
			validator = &ServiceClaimValidator{Recorder: recorder}
			oldClaim = testClaim("url-shortener")
			freeze(oldClaim)
			newClaim = oldClaim.DeepCopy()
			newClaim.Spec.Image = nextImage
		})

		It("rejects an image change while deploys are frozen, and says how to override", func() {
			warnings, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).To(MatchError(`deploys frozen: url-shortener has 0% error budget remaining in a 28d window ` +
				`(burn rate 14.2x). Override with annotation paved.dev/break-glass="<reason>" — this is audited.`))
			Expect(warnings).To(BeEmpty())
			Expect(recorder.Events).To(BeEmpty())
		})

		It("reports a partly recovered budget and the burn rate to one decimal", func() {
			oldClaim.Status.ErrorBudgetRemaining = "3.2%"
			oldClaim.Status.BurnRate1h = "6.08"
			_, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).To(MatchError(ContainSubstring("has 3.2% error budget remaining in a 28d window (burn rate 6.1x)")))
		})

		It("says the budget and burn rate are unknown when the controller could not measure them", func() {
			oldClaim.Status.ErrorBudgetRemaining = ""
			oldClaim.Status.BurnRate1h = ""
			_, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).To(MatchError(ContainSubstring(
				"url-shortener has unknown error budget remaining in a 28d window (burn rate unknown)")))
		})

		It("admits a change that leaves the image alone", func() {
			newClaim.Spec.Image = currentImage
			newClaim.Spec.Scale.Max = 10
			warnings, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})

		It("admits an image change when deploys are not frozen", func() {
			oldClaim.Status.Conditions[0].Status = metav1.ConditionFalse
			_, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).NotTo(HaveOccurred())

			oldClaim.Status.Conditions = nil
			_, err = validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).NotTo(HaveOccurred())
			Expect(recorder.Events).To(BeEmpty())
		})

		It("admits a break-glass image change, with a warning, and records who made it and why", func() {
			newClaim.Annotations = map[string]string{BreakGlassAnnotation: reason}
			warnings, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(ConsistOf(ContainSubstring("recorded in a BreakGlassUsed Event")))
			Expect(recorder.Events).To(Receive(Equal("Warning BreakGlassUsed alice@example.com changed the image from " +
				currentImage + " to " + nextImage + " during a deploy freeze: " + reason)))
		})

		It("records no Event for a dry run", func() {
			newClaim.Annotations = map[string]string{BreakGlassAnnotation: reason}
			_, err := validator.ValidateUpdate(admissionContext(true), oldClaim, newClaim)
			Expect(err).NotTo(HaveOccurred())
			Expect(recorder.Events).To(BeEmpty())
		})

		It("does not let a reason left on the claim by an earlier update through again", func() {
			oldClaim.Annotations = map[string]string{BreakGlassAnnotation: reason}
			newClaim.Annotations = map[string]string{BreakGlassAnnotation: reason}
			_, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			var frozen *DeploysFrozenError
			Expect(errors.As(err, &frozen)).To(BeTrue(), "got %v", err)
			Expect(recorder.Events).To(BeEmpty())
		})

		It("treats a blank reason as no reason", func() {
			newClaim.Annotations = map[string]string{BreakGlassAnnotation: "   "}
			_, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			var frozen *DeploysFrozenError
			Expect(errors.As(err, &frozen)).To(BeTrue(), "got %v", err)
		})

		It("keeps a long reason's audit note within the Event size limit", func() {
			newClaim.Annotations = map[string]string{BreakGlassAnnotation: strings.Repeat("é", eventNoteLimit)}
			_, err := validator.ValidateUpdate(admissionContext(false), oldClaim, newClaim)
			Expect(err).NotTo(HaveOccurred())
			var event string
			Expect(recorder.Events).To(Receive(&event))
			note := strings.TrimPrefix(event, "Warning BreakGlassUsed ")
			Expect(len(note)).To(BeNumerically("<=", eventNoteLimit))
			Expect(utf8.ValidString(note)).To(BeTrue())
		})
	})

	Describe("through the API server", func() {
		It("admits a new claim and an image change to it", func() {
			claim := testClaim("admitted")
			Expect(k8sClient.Create(ctx, claim)).To(Succeed())
			claim.Spec.Image = nextImage
			Expect(k8sClient.Update(ctx, claim)).To(Succeed())
			Expect(k8sClient.Delete(ctx, claim)).To(Succeed())
		})

		It("rejects an image change to a frozen claim, admits other changes, and records break-glass use", func() {
			claim := testClaim("frozen")
			Expect(k8sClient.Create(ctx, claim)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, claim))).To(Succeed()) })
			freeze(claim)
			Expect(k8sClient.Status().Update(ctx, claim)).To(Succeed())

			By("rejecting the image change")
			claim.Spec.Image = nextImage
			err := k8sClient.Update(ctx, claim)
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "got %v", err)
			Expect(err.Error()).To(ContainSubstring(
				"deploys frozen: frozen has 0% error budget remaining in a 28d window (burn rate 14.2x)"))

			By("admitting a change to anything else")
			claim = &platformv1alpha1.ServiceClaim{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(testClaim("frozen")), claim)).To(Succeed())
			claim.Spec.Scale.Max = 10
			Expect(k8sClient.Update(ctx, claim)).To(Succeed())

			By("admitting the image change with break-glass, and recording it")
			claim.Spec.Image = nextImage
			claim.Annotations = map[string]string{BreakGlassAnnotation: "incident 42"}
			Expect(k8sClient.Update(ctx, claim)).To(Succeed())
			Eventually(func(g Gomega) {
				var list eventsv1.EventList
				g.Expect(k8sClient.List(ctx, &list, client.InNamespace(claim.Namespace))).To(Succeed())
				notes := make([]string, 0, len(list.Items))
				for _, event := range list.Items {
					if event.Reason == ReasonBreakGlassUsed && event.Regarding.Name == claim.Name {
						notes = append(notes, event.Note)
					}
				}
				g.Expect(notes).To(ConsistOf(HaveSuffix(
					"changed the image from " + currentImage + " to " + nextImage + " during a deploy freeze: incident 42")))
			}).WithTimeout(10 * time.Second).Should(Succeed())
		})
	})
})
