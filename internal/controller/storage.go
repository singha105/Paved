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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/builders"
)

// Environment variables that link the cluster to an AWS account. hack/cluster-up.sh writes them to
// the paved-aws ConfigMap, which the manager's Deployment loads when it exists (DECISIONS.md, ADR-025).
const (
	EnvAWSAccountID              = "PAVED_AWS_ACCOUNT_ID"
	EnvAWSRegion                 = "PAVED_AWS_REGION"
	EnvAWSOIDCIssuer             = "PAVED_AWS_OIDC_ISSUER"
	EnvAWSPermissionsBoundaryARN = "PAVED_AWS_PERMISSIONS_BOUNDARY_ARN"
)

// StorageReady condition reasons.
const (
	ReasonStorageProvisioned  = "Provisioned"
	ReasonStorageProvisioning = "Provisioning"
	ReasonStorageFailed       = "ProvisioningFailed"
	ReasonStorageUnavailable  = "StorageUnavailable"
	ReasonStorageUnreadable   = "StorageUnreadable"
)

// Condition types ACK sets on its objects (github.com/aws-controllers-k8s/runtime v0.63.0,
// apis/core/v1alpha1/conditions.go).
const (
	ackResourceSynced = "ACK.ResourceSynced"
	ackTerminal       = "ACK.Terminal"
	ackRecoverable    = "ACK.Recoverable"
	ackTrue           = "True"
)

// StorageConfigFromEnv reads the cluster's link to AWS through getenv. It returns nil when none of
// the variables is set. It returns an error when only some are, or when a value can't be right:
// a half-configured link is a mistake to stop on, not a reason to quietly turn storage off.
func StorageConfigFromEnv(getenv func(string) string) (*builders.StorageConfig, error) {
	storage := &builders.StorageConfig{
		AccountID:              getenv(EnvAWSAccountID),
		Region:                 getenv(EnvAWSRegion),
		Issuer:                 getenv(EnvAWSOIDCIssuer),
		PermissionsBoundaryARN: getenv(EnvAWSPermissionsBoundaryARN),
	}
	values := []string{storage.AccountID, storage.Region, storage.Issuer, storage.PermissionsBoundaryARN}
	set := 0
	for _, value := range values {
		if value != "" {
			set++
		}
	}
	switch set {
	case 0:
		return nil, nil
	case len(values):
		if err := storage.Validate(); err != nil {
			return nil, fmt.Errorf("invalid AWS storage settings: %w", err)
		}
		return storage, nil
	default:
		return nil, fmt.Errorf("set all of %s, %s, %s and %s, or none of them",
			EnvAWSAccountID, EnvAWSRegion, EnvAWSOIDCIssuer, EnvAWSPermissionsBoundaryARN)
	}
}

// ServesStorageKinds returns an error naming the first ACK kind the API server doesn't serve.
// Without the ACK CRDs, the controller couldn't watch or apply a claim's Role and Bucket.
func ServesStorageKinds(mapper meta.RESTMapper) error {
	for _, obj := range builders.StorageTypes() {
		gvk := obj.GetObjectKind().GroupVersionKind()
		if _, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			return fmt.Errorf("the API server doesn't serve %s: %w", gvk, err)
		}
	}
	return nil
}

// storageReport is what the controller knows about a claim's storage: its StorageReady condition,
// and where the bucket and role are. Both are nil for a claim without storage.
type storageReport struct {
	condition *metav1.Condition
	status    *platformv1alpha1.StorageStatus
}

// observedACKObject is one of a claim's ACK objects as read from the API server, or the error that
// reading it returned.
type observedACKObject struct {
	kind   string
	object *unstructured.Unstructured
	err    error
}

// evaluateStorage reads the claim's ACK Role and Bucket, past the cache, and reports whether ACK has
// created both in AWS.
func (r *ServiceClaimReconciler) evaluateStorage(ctx context.Context, claim *platformv1alpha1.ServiceClaim) storageReport {
	if !claim.Spec.Storage {
		return storageReport{}
	}
	if r.Storage == nil {
		return storageReport{condition: &metav1.Condition{
			Type:               platformv1alpha1.ConditionStorageReady,
			Status:             metav1.ConditionFalse,
			Reason:             ReasonStorageUnavailable,
			Message:            "This cluster isn't linked to an AWS account, so the claim's bucket and role can't be created",
			ObservedGeneration: claim.Generation,
		}}
	}

	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	key := types.NamespacedName{Namespace: builders.NamespaceName(claim), Name: claim.Name}
	kinds := builders.StorageTypes()
	observed := make([]observedACKObject, 0, len(kinds))
	for _, kind := range kinds {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(kind.GetObjectKind().GroupVersionKind())
		err := reader.Get(ctx, key, obj)
		observed = append(observed, observedACKObject{kind: obj.GetKind(), object: obj, err: err})
	}

	return storageReport{
		condition: new(storageCondition(claim, observed)),
		status: &platformv1alpha1.StorageStatus{
			Bucket:  builders.StorageBucketName(claim, r.Storage),
			RoleARN: builders.StorageRoleARN(claim, r.Storage),
		},
	}
}

// storageCondition is StorageReady for a claim whose ACK objects were observed as given. It is True
// once ACK reports every object synced with AWS. It is False while one doesn't exist or is still
// syncing, or once ACK has given up on one, and Unknown when one couldn't be read.
func storageCondition(claim *platformv1alpha1.ServiceClaim, observed []observedACKObject) metav1.Condition {
	condition := metav1.Condition{
		Type:               platformv1alpha1.ConditionStorageReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: claim.Generation,
	}
	for _, o := range observed {
		if apierrors.IsNotFound(o.err) {
			condition.Reason = ReasonStorageProvisioning
			condition.Message = fmt.Sprintf("The ACK %s does not exist yet", o.kind)
			return condition
		}
		conditions, err := ackConditions(o.object)
		if o.err != nil || err != nil {
			condition.Status = metav1.ConditionUnknown
			condition.Reason = ReasonStorageUnreadable
			condition.Message = fmt.Sprintf("Reading the ACK %s: %v", o.kind, firstError(o.err, err))
			return condition
		}
		if terminal := conditions[ackTerminal]; terminal.Status == ackTrue {
			condition.Reason = ReasonStorageFailed
			condition.Message = fmt.Sprintf("ACK can't create the %s in AWS: %s", o.kind, terminal.Message)
			return condition
		}
		if synced := conditions[ackResourceSynced]; synced.Status != ackTrue {
			condition.Reason = ReasonStorageProvisioning
			condition.Message = fmt.Sprintf("ACK has not synced the %s with AWS yet", o.kind)
			if detail := firstNonEmpty(conditions[ackRecoverable].Message, synced.Message); detail != "" {
				condition.Message += ": " + detail
			}
			return condition
		}
	}
	condition.Status = metav1.ConditionTrue
	condition.Reason = ReasonStorageProvisioned
	condition.Message = "ACK has created the claim's role and bucket in AWS"
	return condition
}

// ackCondition is the part of an ACK condition paved reads. ACK's conditions are not
// metav1.Conditions: their message is optional and they have no reason.
type ackCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ackStatusField is the field of an ACK object that holds its conditions.
const ackStatusField = "status"

// ackConditions returns an ACK object's status conditions by type. An object ACK hasn't
// reconciled yet has none.
func ackConditions(obj *unstructured.Unstructured) (map[string]ackCondition, error) {
	conditions := map[string]ackCondition{}
	if obj == nil {
		return conditions, nil
	}
	raw, found, err := unstructured.NestedMap(obj.Object, ackStatusField)
	if err != nil {
		return nil, fmt.Errorf("status of %s %s: %w", obj.GetKind(), objectRef(obj), err)
	}
	if !found {
		return conditions, nil
	}
	var status struct {
		Conditions []ackCondition `json:"conditions"`
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &status); err != nil {
		return nil, fmt.Errorf("conditions of %s %s: %w", obj.GetKind(), objectRef(obj), err)
	}
	for _, c := range status.Conditions {
		conditions[c.Type] = c
	}
	return conditions, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
