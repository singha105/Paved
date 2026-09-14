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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

// A claim with spec.storage gets an S3 bucket and an IAM role only its pods can assume. Both are ACK
// (AWS Controllers for Kubernetes) objects: paved applies them like every other managed object, and
// ACK's controllers create and correct the real role and bucket in AWS. No key is stored anywhere.
// A pod hands AWS STS a service-account token the cluster signed, AWS checks the signature against
// the cluster's public OIDC issuer, and the pod gets a role session that expires (DECISIONS.md,
// ADR-025).

// Storage names and settings. A claim cannot choose any of them.
const (
	storagePrefix = "paved-"
	// storageRolePath holds every claim's role, so the ACK IAM controller can be limited to it.
	storageRolePath             = "/paved/workloads/"
	storagePolicyName           = "bucket-access"
	storageSessionSeconds int64 = 3600
	bucketHashLength            = 8
	policyVersion               = "2012-10-17"
	policyAllow                 = "Allow"
	iamARNPrefix                = "arn:aws:iam::"

	// MaxStorageClaimNameLength is the longest name a claim with storage can have. Its bucket is
	// "paved-<name>-<8 hex digits>", and an S3 bucket name is at most 63 characters.
	MaxStorageClaimNameLength = 63 - len(storagePrefix) - 1 - bucketHashLength

	// Annotations the ACK runtime reads (github.com/aws-controllers-k8s/runtime v0.63.0,
	// apis/core/v1alpha1/annotations.go and pkg/runtime/util.go). adopt-or-create lets a rebuilt
	// cluster take over the role and bucket an earlier cluster created. retain keeps a bucket, and
	// its data, when its claim is deleted.
	ackAdoptionPolicyAnnotation = "services.k8s.aws/adoption-policy"
	ackDeletionPolicyAnnotation = "services.k8s.aws/deletion-policy"
	ackAdoptOrCreate            = "adopt-or-create"
	ackRetain                   = "retain"

	// awsTokenAudience is the audience AWS STS requires in a web identity token.
	awsTokenAudience = "sts.amazonaws.com"
	awsTokenVolume   = "aws-token"
	awsTokenDir      = "/var/run/secrets/paved.dev/aws"
	awsTokenFile     = "token"
	// awsTokenSeconds is how long each projected token lasts. The kubelet replaces it before then.
	awsTokenSeconds int64 = 3600

	// EnvStorageBucket tells a claim's pods the name of their bucket.
	EnvStorageBucket = "PAVED_STORAGE_BUCKET"

	// usEast1 is the one region where CreateBucket must not name a location constraint.
	usEast1 = "us-east-1"
)

// GVKs of the ACK objects a claim with storage produces (iam-controller v1.9.0, s3-controller v1.12.1).
var (
	RoleGVK   = schema.GroupVersionKind{Group: "iam.services.k8s.aws", Version: "v1alpha1", Kind: "Role"}
	BucketGVK = schema.GroupVersionKind{Group: "s3.services.k8s.aws", Version: "v1alpha1", Kind: "Bucket"}
)

// StorageConfig is the AWS account a cluster's claims get storage in. The controller reads it at
// startup. A nil *StorageConfig means the cluster isn't linked to AWS, and no claim gets storage.
type StorageConfig struct {
	// AccountID is the 12-digit AWS account the roles and buckets are created in.
	AccountID string
	// Region is the AWS region the buckets are created in.
	Region string
	// Issuer is the cluster's service-account token issuer the way IAM names its OIDC provider: the
	// issuer URL without "https://", such as "paved-oidc-0123abcd.s3.eu-west-2.amazonaws.com".
	Issuer string
	// PermissionsBoundaryARN is the managed policy that caps every role created for a claim.
	PermissionsBoundaryARN string
}

// Validate returns an error for the first setting that can't be right.
func (c *StorageConfig) Validate() error {
	switch {
	case len(c.AccountID) != 12 || strings.Trim(c.AccountID, "0123456789") != "":
		return fmt.Errorf("AWS account ID %q is not 12 digits", c.AccountID)
	case c.Region == "" || strings.Trim(c.Region, "abcdefghijklmnopqrstuvwxyz0123456789-") != "":
		return fmt.Errorf("AWS region %q is not a region name", c.Region)
	case c.Issuer == "" || strings.Contains(c.Issuer, "://") || strings.HasSuffix(c.Issuer, "/"):
		return fmt.Errorf("OIDC issuer %q must be a host and optional path, with no scheme or trailing slash", c.Issuer)
	case !strings.HasPrefix(c.PermissionsBoundaryARN, iamARNPrefix+c.AccountID+":policy/"):
		return fmt.Errorf("permissions boundary %q is not a policy ARN in account %s", c.PermissionsBoundaryARN, c.AccountID)
	}
	return nil
}

// HasStorage reports whether a claim gets a bucket and a role: it asked for storage, and the cluster
// is linked to an AWS account.
func HasStorage(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) bool {
	return sc.Spec.Storage && storage != nil
}

// StorageBucketName is the claim's S3 bucket. Bucket names are shared by every AWS account, so the
// name ends in a hash of the account, the region and the claim.
func StorageBucketName(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) string {
	sum := sha256.Sum256([]byte(storage.AccountID + "/" + storage.Region + "/" + sc.Namespace + "/" + sc.Name))
	return storagePrefix + sc.Name + "-" + hex.EncodeToString(sum[:])[:bucketHashLength]
}

// StorageRoleName is the name of the claim's IAM role.
func StorageRoleName(sc *platformv1alpha1.ServiceClaim) string {
	return storagePrefix + sc.Name
}

// StorageRoleARN is the role the claim's pods assume. It is known before the role exists, so the
// Rollout doesn't wait for ACK.
func StorageRoleARN(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) string {
	return iamARNPrefix + storage.AccountID + ":role" + storageRolePath + StorageRoleName(sc)
}

// StorageTypes returns an empty object of each ACK kind a claim with storage produces.
func StorageTypes() []client.Object {
	return []client.Object{newUnstructured(RoleGVK), newUnstructured(BucketGVK)}
}

// policyDocument is an IAM policy. encoding/json writes map keys in sorted order, so a claim always
// produces the same document, and applying it again changes nothing.
type policyDocument struct {
	Version   string            `json:"Version"`
	Statement []policyStatement `json:"Statement"`
}

type policyStatement struct {
	Effect    string                       `json:"Effect"`
	Principal map[string]string            `json:"Principal,omitempty"`
	Action    []string                     `json:"Action"`
	Resource  []string                     `json:"Resource,omitempty"`
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

// BuildStorageRole returns the ACK Role for the claim's IAM role:
//   - Only a token this cluster issued to the claim's service account, for AWS STS, can assume it.
//   - Its one inline policy reaches only the claim's bucket.
//   - The platform's permissions boundary caps it, whatever the policy says.
func BuildStorageRole(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) (*unstructured.Unstructured, error) {
	trust, err := json.Marshal(policyDocument{Version: policyVersion, Statement: []policyStatement{{
		Effect:    policyAllow,
		Principal: map[string]string{"Federated": iamARNPrefix + storage.AccountID + ":oidc-provider/" + storage.Issuer},
		Action:    []string{"sts:AssumeRoleWithWebIdentity"},
		Condition: map[string]map[string]string{"StringEquals": {
			storage.Issuer + ":aud": awsTokenAudience,
			storage.Issuer + ":sub": serviceAccountSubject(sc),
		}},
	}}})
	if err != nil {
		return nil, fmt.Errorf("encoding the trust policy of claim %s/%s: %w", sc.Namespace, sc.Name, err)
	}

	bucketARN := "arn:aws:s3:::" + StorageBucketName(sc, storage)
	access, err := json.Marshal(policyDocument{Version: policyVersion, Statement: []policyStatement{
		{Effect: policyAllow, Action: []string{"s3:GetBucketLocation", "s3:ListBucket"}, Resource: []string{bucketARN}},
		{Effect: policyAllow, Action: []string{"s3:DeleteObject", "s3:GetObject", "s3:PutObject"}, Resource: []string{bucketARN + "/*"}},
	}})
	if err != nil {
		return nil, fmt.Errorf("encoding the bucket policy of claim %s/%s: %w", sc.Namespace, sc.Name, err)
	}

	role := newStorageObject(sc, RoleGVK, map[string]string{ackAdoptionPolicyAnnotation: ackAdoptOrCreate})
	role.Object["spec"] = map[string]any{
		"name":                     StorageRoleName(sc),
		"path":                     storageRolePath,
		"description":              "Storage access for ServiceClaim " + sc.Namespace + "/" + sc.Name + ", managed by paved",
		"assumeRolePolicyDocument": string(trust),
		"inlinePolicies":           map[string]any{storagePolicyName: string(access)},
		"permissionsBoundary":      storage.PermissionsBoundaryARN,
		"maxSessionDuration":       storageSessionSeconds,
		"tags":                     storageTags(sc),
	}
	return role, nil
}

// BuildStorageBucket returns the ACK Bucket for the claim's S3 bucket. Every kind of public access is
// blocked, and the bucket is kept, with its data, when the claim is deleted.
func BuildStorageBucket(sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) *unstructured.Unstructured {
	spec := map[string]any{
		"name": StorageBucketName(sc, storage),
		"publicAccessBlock": map[string]any{
			"blockPublicACLs":       true,
			"blockPublicPolicy":     true,
			"ignorePublicACLs":      true,
			"restrictPublicBuckets": true,
		},
		"tagging": map[string]any{"tagSet": storageTags(sc)},
	}
	if storage.Region != usEast1 {
		spec["createBucketConfiguration"] = map[string]any{"locationConstraint": storage.Region}
	}

	bucket := newStorageObject(sc, BucketGVK, map[string]string{
		ackAdoptionPolicyAnnotation: ackAdoptOrCreate,
		ackDeletionPolicyAnnotation: ackRetain,
	})
	bucket.Object["spec"] = spec
	return bucket
}

// withStorageIdentity gives a claim's pods everything an AWS SDK needs to assume the claim's role by
// itself: a projected service-account token for STS, the role, the region, and the bucket's name.
func withStorageIdentity(spec *corev1.PodSpec, sc *platformv1alpha1.ServiceClaim, storage *StorageConfig) {
	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: awsTokenVolume,
		VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
			Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
				Audience:          awsTokenAudience,
				ExpirationSeconds: new(awsTokenSeconds),
				Path:              awsTokenFile,
			}}},
		}},
	})
	for i := range spec.Containers {
		container := &spec.Containers[i]
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name: awsTokenVolume, MountPath: awsTokenDir, ReadOnly: true,
		})
		container.Env = append(container.Env,
			corev1.EnvVar{Name: "AWS_ROLE_ARN", Value: StorageRoleARN(sc, storage)},
			corev1.EnvVar{Name: "AWS_WEB_IDENTITY_TOKEN_FILE", Value: awsTokenDir + "/" + awsTokenFile},
			corev1.EnvVar{Name: "AWS_REGION", Value: storage.Region},
			corev1.EnvVar{Name: "AWS_DEFAULT_REGION", Value: storage.Region},
			corev1.EnvVar{Name: EnvStorageBucket, Value: StorageBucketName(sc, storage)},
		)
	}
}

// serviceAccountSubject is the sub claim of a token issued to the claim's service account.
func serviceAccountSubject(sc *platformv1alpha1.ServiceClaim) string {
	return "system:serviceaccount:" + NamespaceName(sc) + ":" + sc.Name
}

// storageTags tags the AWS role and bucket with their claim, so either can be traced back from AWS.
func storageTags(sc *platformv1alpha1.ServiceClaim) []any {
	return []any{
		map[string]any{"key": LabelClaim, "value": sc.Name},
		map[string]any{"key": LabelClaimNamespace, "value": sc.Namespace},
	}
}

// newStorageObject returns an ACK object of kind gvk in the claim's namespace, named and labelled as
// the claim's.
func newStorageObject(sc *platformv1alpha1.ServiceClaim, gvk schema.GroupVersionKind, annotations map[string]string) *unstructured.Unstructured {
	u := newUnstructured(gvk)
	u.SetName(sc.Name)
	u.SetNamespace(NamespaceName(sc))
	u.SetLabels(Labels(sc))
	u.SetAnnotations(annotations)
	return u
}

func newUnstructured(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	return u
}
