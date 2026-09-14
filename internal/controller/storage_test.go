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
	"errors"
	"maps"
	"strings"
	"testing"

	rolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
)

// completeStorageEnv is a full set of AWS settings, as hack/cluster-up.sh writes them.
var completeStorageEnv = map[string]string{
	EnvAWSAccountID:              "123456789012",
	EnvAWSRegion:                 "eu-west-2",
	EnvAWSOIDCIssuer:             "oidc.example.com",
	EnvAWSPermissionsBoundaryARN: "arn:aws:iam::123456789012:policy/paved/paved-workload-boundary",
}

func lookupIn(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestStorageConfigFromEnv(t *testing.T) {
	if storage, err := StorageConfigFromEnv(lookupIn(nil)); err != nil || storage != nil {
		t.Errorf("with no settings: got %+v, %v; want storage off and no error", storage, err)
	}

	storage, err := StorageConfigFromEnv(lookupIn(completeStorageEnv))
	want := builders.StorageConfig{
		AccountID:              completeStorageEnv[EnvAWSAccountID],
		Region:                 completeStorageEnv[EnvAWSRegion],
		Issuer:                 completeStorageEnv[EnvAWSOIDCIssuer],
		PermissionsBoundaryARN: completeStorageEnv[EnvAWSPermissionsBoundaryARN],
	}
	if err != nil || storage == nil || *storage != want {
		t.Errorf("with every setting: got %+v, %v; want %+v", storage, err, want)
	}

	partial := maps.Clone(completeStorageEnv)
	delete(partial, EnvAWSRegion)
	if _, err := StorageConfigFromEnv(lookupIn(partial)); err == nil || !strings.Contains(err.Error(), EnvAWSRegion) {
		t.Errorf("with %s missing: error = %v, want one that names it", EnvAWSRegion, err)
	}

	invalid := maps.Clone(completeStorageEnv)
	invalid[EnvAWSAccountID] = "1234"
	if _, err := StorageConfigFromEnv(lookupIn(invalid)); err == nil {
		t.Error("with a 4-digit account ID: no error, want the settings rejected")
	}
}

func TestServesStorageKinds(t *testing.T) {
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(builders.RoleGVK, meta.RESTScopeNamespace)
	if err := ServesStorageKinds(mapper); !meta.IsNoMatchError(err) {
		t.Errorf("without the Bucket kind: error = %v, want a no-match error", err)
	}

	mapper.Add(builders.BucketGVK, meta.RESTScopeNamespace)
	if err := ServesStorageKinds(mapper); err != nil {
		t.Errorf("with both kinds: error = %v, want none", err)
	}
}

// observed returns a claim's ACK object of the given kind, as read with the given status conditions.
func observed(gvk schema.GroupVersionKind, conditions ...any) observedACKObject {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetGroupVersionKind(gvk)
	if len(conditions) > 0 {
		obj.Object[ackStatusField] = map[string]any{"conditions": conditions}
	}
	return observedACKObject{kind: gvk.Kind, object: obj}
}

// ackStatus is a condition the way ACK writes one.
func ackStatus(t *testing.T, conditionType, status, message string) map[string]any {
	t.Helper()
	condition, err := runtime.DefaultUnstructuredConverter.ToUnstructured(
		&ackCondition{Type: conditionType, Status: status, Message: message})
	if err != nil {
		t.Fatalf("converting an ACK condition: %v", err)
	}
	return condition
}

func TestStorageCondition(t *testing.T) {
	synced := ackStatus(t, ackResourceSynced, ackTrue, "")
	notFound := observedACKObject{kind: builders.BucketGVK.Kind, object: &unstructured.Unstructured{},
		err: apierrors.NewNotFound(schema.GroupResource{Group: builders.BucketGVK.Group, Resource: "buckets"}, unitClaimName)}
	unreadable := observedACKObject{kind: builders.RoleGVK.Kind, object: &unstructured.Unstructured{},
		err: errors.New("connection refused")}

	tests := []struct {
		name       string
		observed   []observedACKObject
		wantStatus metav1.ConditionStatus
		wantReason string
		// wantMessage is the start of the message: ACK's own text follows it.
		wantMessage string
	}{
		{"both synced", []observedACKObject{observed(builders.RoleGVK, synced), observed(builders.BucketGVK, synced)},
			metav1.ConditionTrue, ReasonStorageProvisioned, "ACK has created the claim's role and bucket in AWS"},
		{"bucket not created yet", []observedACKObject{observed(builders.RoleGVK, synced), notFound},
			metav1.ConditionFalse, ReasonStorageProvisioning, "The ACK Bucket does not exist yet"},
		{"not reconciled by ACK yet", []observedACKObject{observed(builders.RoleGVK), observed(builders.BucketGVK, synced)},
			metav1.ConditionFalse, ReasonStorageProvisioning, "ACK has not synced the Role with AWS yet"},
		{"a recoverable error", []observedACKObject{
			observed(builders.RoleGVK, ackStatus(t, ackResourceSynced, "False", ""), ackStatus(t, ackRecoverable, ackTrue, "Throttling: rate exceeded")),
			observed(builders.BucketGVK, synced)},
			metav1.ConditionFalse, ReasonStorageProvisioning, "ACK has not synced the Role with AWS yet: Throttling: rate exceeded"},
		{"a terminal error", []observedACKObject{
			observed(builders.RoleGVK, ackStatus(t, ackTerminal, ackTrue, "AccessDenied: iam:CreateRole")),
			observed(builders.BucketGVK, synced)},
			metav1.ConditionFalse, ReasonStorageFailed, "ACK can't create the Role in AWS: AccessDenied: iam:CreateRole"},
		{"unreadable", []observedACKObject{unreadable, observed(builders.BucketGVK, synced)},
			metav1.ConditionUnknown, ReasonStorageUnreadable, "Reading the ACK Role: connection refused"},
		{"malformed status", []observedACKObject{observed(builders.RoleGVK, "not a condition"), observed(builders.BucketGVK, synced)},
			metav1.ConditionUnknown, ReasonStorageUnreadable, "Reading the ACK Role: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storageCondition(unitTestClaim(), tt.observed)
			if got.Type != platformv1alpha1.ConditionStorageReady || got.Status != tt.wantStatus ||
				got.Reason != tt.wantReason || !strings.HasPrefix(got.Message, tt.wantMessage) {
				t.Errorf("StorageReady is %s %s (%s) %q, want %s (%s) starting %q",
					got.Type, got.Status, got.Reason, got.Message, tt.wantStatus, tt.wantReason, tt.wantMessage)
			}
		})
	}
}

func TestReadyWaitsForStorage(t *testing.T) {
	synced := metav1.Condition{Type: platformv1alpha1.ConditionResourcesSynced, Status: metav1.ConditionTrue, Reason: ReasonApplied}
	healthy := &rolloutsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Status:     rolloutsv1alpha1.RolloutStatus{ObservedGeneration: "2", Phase: rolloutsv1alpha1.RolloutPhaseHealthy},
	}
	healthyMessage := "The Rollout is healthy"
	storage := func(status metav1.ConditionStatus, reason, message string) *metav1.Condition {
		return &metav1.Condition{Type: platformv1alpha1.ConditionStorageReady, Status: status, Reason: reason, Message: message}
	}

	tests := []struct {
		name        string
		storage     *metav1.Condition
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{"no storage", nil, metav1.ConditionTrue, ReasonRolloutHealthy, healthyMessage},
		{"storage provisioned", storage(metav1.ConditionTrue, ReasonStorageProvisioned, "created"),
			metav1.ConditionTrue, ReasonRolloutHealthy, healthyMessage},
		{"storage provisioning", storage(metav1.ConditionFalse, ReasonStorageProvisioning, "The ACK Bucket does not exist yet"),
			metav1.ConditionFalse, ReasonStorageProvisioning, "The claim's storage is not ready: The ACK Bucket does not exist yet"},
		{"storage unreadable", storage(metav1.ConditionUnknown, ReasonStorageUnreadable, "Reading the ACK Role: timeout"),
			metav1.ConditionUnknown, ReasonStorageUnreadable, "The claim's storage is not ready: Reading the ACK Role: timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readyCondition(unitTestClaim(), synced, tt.storage, healthy, nil)
			if got.Status != tt.wantStatus || got.Reason != tt.wantReason || got.Message != tt.wantMessage {
				t.Errorf("Ready is %s (%s) %q, want %s (%s) %q",
					got.Status, got.Reason, got.Message, tt.wantStatus, tt.wantReason, tt.wantMessage)
			}
		})
	}
}
