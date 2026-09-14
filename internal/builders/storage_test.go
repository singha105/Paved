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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	testAccountID = "123456789012"
	testRegion    = "eu-west-2"
	testIssuer    = "paved-oidc-0123abcd.s3.eu-west-2.amazonaws.com"
	testBucket    = "paved-url-shortener-"
)

// testStorage returns the AWS link of a cluster that gives claims storage.
func testStorage() *StorageConfig {
	return &StorageConfig{
		AccountID:              testAccountID,
		Region:                 testRegion,
		Issuer:                 testIssuer,
		PermissionsBoundaryARN: iamARNPrefix + testAccountID + ":policy/paved/paved-workload-boundary",
	}
}

// newStorageClaim returns a claim of the given tier that asked for storage.
func newStorageClaim(tier string) *platformv1alpha1.ServiceClaim {
	sc := newClaim(tier)
	sc.Spec.Storage = true
	return sc
}

// specField returns the string at spec.<field> of an ACK object, failing the test if it isn't set.
func specField(t *testing.T, u *unstructured.Unstructured, field ...string) string {
	t.Helper()
	value, found, err := unstructured.NestedString(u.Object, append([]string{"spec"}, field...)...)
	if err != nil || !found {
		t.Fatalf("%s spec.%s: found=%t err=%v", u.GetKind(), strings.Join(field, "."), found, err)
	}
	return value
}

// decodePolicy decodes an IAM policy document, failing the test if it isn't valid JSON.
func decodePolicy(t *testing.T, document string) policyDocument {
	t.Helper()
	var policy policyDocument
	if err := json.Unmarshal([]byte(document), &policy); err != nil {
		t.Fatalf("policy is not JSON: %v\n%s", err, document)
	}
	return policy
}

func TestBuildWithStorageAddsRoleAndBucketAfterTheServiceAccount(t *testing.T) {
	tests := []struct {
		tier string
		want int
	}{
		{platformv1alpha1.TierPublic, 15},
		{platformv1alpha1.TierInternal, 14},
		{platformv1alpha1.TierBatch, 10},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			objects, err := Build(newStorageClaim(tt.tier), testStorage())
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(objects) != tt.want {
				t.Errorf("Build() returned %d objects, want %d", len(objects), tt.want)
			}
			// Namespace and ServiceAccount come first, as without storage; the AWS objects follow.
			for i, want := range map[int]schema.GroupVersionKind{2: RoleGVK, 3: BucketGVK} {
				if got := objects[i].GetObjectKind().GroupVersionKind(); got != want {
					t.Errorf("object %d is a %s, want a %s", i, got.Kind, want.Kind)
				}
			}
		})
	}
}

func TestBuildWithoutBothStorageAndAnAWSLinkAddsNothing(t *testing.T) {
	tests := []struct {
		name    string
		sc      *platformv1alpha1.ServiceClaim
		storage *StorageConfig
	}{
		{"the claim asked, but the cluster has no AWS link", newStorageClaim(platformv1alpha1.TierPublic), nil},
		{"the cluster is linked, but the claim did not ask", newClaim(platformv1alpha1.TierPublic), testStorage()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects, err := Build(tt.sc, tt.storage)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(objects) != 13 {
				t.Errorf("Build() returned %d objects, want the 13 of a public claim", len(objects))
			}
			for _, obj := range objects {
				if gvk := obj.GetObjectKind().GroupVersionKind(); gvk == RoleGVK || gvk == BucketGVK {
					t.Errorf("Build() produced a %s", gvk.Kind)
				}
			}
			pod := BuildRollout(tt.sc, tt.storage).Spec.Template.Spec
			if len(pod.Volumes) != 0 || len(pod.Containers[0].Env) != 0 {
				t.Errorf("pod has volumes %v and env %v, want neither", pod.Volumes, pod.Containers[0].Env)
			}
		})
	}
}

func TestStorageNames(t *testing.T) {
	sc := newStorageClaim(platformv1alpha1.TierInternal)
	bucket := StorageBucketName(sc, testStorage())

	if !strings.HasPrefix(bucket, testBucket) || len(bucket) != len(testBucket)+bucketHashLength {
		t.Errorf("bucket = %q, want %s followed by %d hex digits", bucket, testBucket, bucketHashLength)
	}
	if again := StorageBucketName(newStorageClaim(platformv1alpha1.TierBatch), testStorage()); again != bucket {
		t.Errorf("bucket name changed between builds: %q, then %q", bucket, again)
	}

	otherAccount, otherRegion := testStorage(), testStorage()
	otherAccount.AccountID = "210987654321"
	otherRegion.Region = "eu-west-1"
	otherNamespace := newStorageClaim(platformv1alpha1.TierInternal)
	otherNamespace.Namespace = "team-a"
	for name, other := range map[string]string{
		"account":         StorageBucketName(sc, otherAccount),
		"region":          StorageBucketName(sc, otherRegion),
		"claim namespace": StorageBucketName(otherNamespace, testStorage()),
	} {
		if other == bucket {
			t.Errorf("a different %s gives the same bucket name %q", name, bucket)
		}
	}

	if got, want := StorageRoleName(sc), "paved-url-shortener"; got != want {
		t.Errorf("role name = %q, want %q", got, want)
	}
	if got, want := StorageRoleARN(sc, testStorage()), "arn:aws:iam::123456789012:role/paved/workloads/paved-url-shortener"; got != want {
		t.Errorf("role ARN = %q, want %q", got, want)
	}
}

func TestLongestStorageClaimNameStillMakesAValidBucketName(t *testing.T) {
	sc := newStorageClaim(platformv1alpha1.TierBatch)
	sc.Name = strings.Repeat("a", MaxStorageClaimNameLength)
	bucket := StorageBucketName(sc, testStorage())

	if len(bucket) != 63 {
		t.Errorf("bucket name is %d characters, want exactly S3's limit of 63: %q", len(bucket), bucket)
	}
	valid := strings.Trim(bucket, "abcdefghijklmnopqrstuvwxyz0123456789-") == "" &&
		!strings.HasPrefix(bucket, "-") && !strings.HasSuffix(bucket, "-")
	if !valid {
		t.Errorf("bucket name %q breaks S3's naming rules", bucket)
	}
	if role := StorageRoleName(sc); len(role) > 64 {
		t.Errorf("role name is %d characters, over IAM's limit of 64", len(role))
	}
}

func TestCRDLimitsStorageClaimNamesToWhatTheBucketNameAllows(t *testing.T) {
	crd, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "platform.paved.dev_serviceclaims.yaml"))
	if err != nil {
		t.Fatalf("reading the generated CRD: %v", err)
	}
	// controller-gen folds long rules across lines, so compare with the whitespace collapsed.
	flat := strings.Join(strings.Fields(string(crd)), " ")
	if want := fmt.Sprintf("size(self.metadata.name) <= %d", MaxStorageClaimNameLength); !strings.Contains(flat, want) {
		t.Errorf("the CRD's name rule doesn't match MaxStorageClaimNameLength: want it to contain %q", want)
	}
}

func TestStorageRoleTrustsOnlyTheClaimsServiceAccount(t *testing.T) {
	role, err := BuildStorageRole(newStorageClaim(platformv1alpha1.TierPublic), testStorage())
	if err != nil {
		t.Fatalf("BuildStorageRole: %v", err)
	}
	document := specField(t, role, "assumeRolePolicyDocument")

	want := policyDocument{Version: policyVersion, Statement: []policyStatement{{
		Effect:    policyAllow,
		Principal: map[string]string{"Federated": "arn:aws:iam::123456789012:oidc-provider/" + testIssuer},
		Action:    []string{"sts:AssumeRoleWithWebIdentity"},
		Condition: map[string]map[string]string{"StringEquals": {
			testIssuer + ":aud": awsTokenAudience,
			testIssuer + ":sub": "system:serviceaccount:svc-url-shortener:url-shortener",
		}},
	}}}
	if got := decodePolicy(t, document); !reflect.DeepEqual(got, want) {
		t.Errorf("trust policy = %+v, want %+v", got, want)
	}
	if strings.Contains(document, "*") {
		t.Errorf("trust policy has a wildcard: %s", document)
	}
}

func TestStorageRoleReachesOnlyItsBucketAndIsBounded(t *testing.T) {
	sc, storage := newStorageClaim(platformv1alpha1.TierPublic), testStorage()
	role, err := BuildStorageRole(sc, storage)
	if err != nil {
		t.Fatalf("BuildStorageRole: %v", err)
	}

	policies, found, err := unstructured.NestedStringMap(role.Object, "spec", "inlinePolicies")
	if err != nil || !found || len(policies) != 1 {
		t.Fatalf("inlinePolicies = %v (found=%t, err=%v), want exactly one policy", policies, found, err)
	}
	bucketARN := "arn:aws:s3:::" + StorageBucketName(sc, storage)
	for _, statement := range decodePolicy(t, policies[storagePolicyName]).Statement {
		if statement.Effect != policyAllow {
			t.Errorf("statement effect = %q, want Allow", statement.Effect)
		}
		for _, action := range statement.Action {
			if !strings.HasPrefix(action, "s3:") || strings.Contains(action, "*") {
				t.Errorf("action %q is not a single S3 action", action)
			}
		}
		for _, resource := range statement.Resource {
			if resource != bucketARN && resource != bucketARN+"/*" {
				t.Errorf("resource %q is outside the claim's bucket %s", resource, bucketARN)
			}
		}
	}

	if got := specField(t, role, "permissionsBoundary"); got != storage.PermissionsBoundaryARN {
		t.Errorf("permissionsBoundary = %q, want %q", got, storage.PermissionsBoundaryARN)
	}
	if got := specField(t, role, "path"); got != storageRolePath {
		t.Errorf("path = %q, want %q", got, storageRolePath)
	}
	if got := role.GetAnnotations(); !reflect.DeepEqual(got, map[string]string{ackAdoptionPolicyAnnotation: ackAdoptOrCreate}) {
		t.Errorf("annotations = %v, want only adoption-policy adopt-or-create: the role goes with its claim", got)
	}
}

func TestStorageBucketBlocksPublicAccessAndOutlivesItsClaim(t *testing.T) {
	sc, storage := newStorageClaim(platformv1alpha1.TierPublic), testStorage()
	bucket := BuildStorageBucket(sc, storage)

	if got := specField(t, bucket, "name"); got != StorageBucketName(sc, storage) {
		t.Errorf("name = %q, want %q", got, StorageBucketName(sc, storage))
	}
	for _, setting := range []string{"blockPublicACLs", "blockPublicPolicy", "ignorePublicACLs", "restrictPublicBuckets"} {
		if on, found, err := unstructured.NestedBool(bucket.Object, "spec", "publicAccessBlock", setting); err != nil || !found || !on {
			t.Errorf("publicAccessBlock.%s = %t (found=%t, err=%v), want true", setting, on, found, err)
		}
	}
	wantAnnotations := map[string]string{ackAdoptionPolicyAnnotation: ackAdoptOrCreate, ackDeletionPolicyAnnotation: ackRetain}
	if got := bucket.GetAnnotations(); !reflect.DeepEqual(got, wantAnnotations) {
		t.Errorf("annotations = %v, want %v", got, wantAnnotations)
	}
	if got := specField(t, bucket, "createBucketConfiguration", "locationConstraint"); got != testRegion {
		t.Errorf("locationConstraint = %q, want %q", got, testRegion)
	}

	storage.Region = usEast1
	if _, found, _ := unstructured.NestedMap(BuildStorageBucket(sc, storage).Object, "spec", "createBucketConfiguration"); found {
		t.Error("a us-east-1 bucket names a location constraint, which S3 rejects there")
	}
}

func TestStorageObjectsAreTheClaimsAndSurviveACopy(t *testing.T) {
	sc, storage := newStorageClaim(platformv1alpha1.TierPublic), testStorage()
	role, err := BuildStorageRole(sc, storage)
	if err != nil {
		t.Fatalf("BuildStorageRole: %v", err)
	}
	for _, obj := range []*unstructured.Unstructured{role, BuildStorageBucket(sc, storage)} {
		if obj.GetName() != testClaimName || obj.GetNamespace() != testNamespace {
			t.Errorf("%s is %s/%s, want %s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName(), testNamespace, testClaimName)
		}
		if !reflect.DeepEqual(obj.GetLabels(), Labels(sc)) {
			t.Errorf("%s labels = %v, want %v", obj.GetKind(), obj.GetLabels(), Labels(sc))
		}
		// The cache and the apply both deep-copy objects, which only works on JSON-compatible values.
		if copied := obj.DeepCopy(); !reflect.DeepEqual(copied.Object, obj.Object) {
			t.Errorf("%s changed when copied", obj.GetKind())
		}
	}
}

func TestStorageTypesMatchBuild(t *testing.T) {
	objects, err := Build(newStorageClaim(platformv1alpha1.TierPublic), testStorage())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var built []string
	for _, obj := range objects {
		if _, isUnstructured := obj.(*unstructured.Unstructured); isUnstructured {
			built = append(built, obj.GetObjectKind().GroupVersionKind().String())
		}
	}
	types := StorageTypes()
	listed := make([]string, 0, len(types))
	for _, obj := range types {
		listed = append(listed, obj.GetObjectKind().GroupVersionKind().String())
	}
	if !slices.Equal(built, listed) {
		t.Errorf("Build() produces unstructured kinds %v, StorageTypes() lists %v", built, listed)
	}
}

func TestRolloutPodsGetTheClaimsAWSIdentity(t *testing.T) {
	sc, storage := newStorageClaim(platformv1alpha1.TierPublic), testStorage()
	pod := BuildRollout(sc, storage).Spec.Template.Spec

	if len(pod.Volumes) != 1 || pod.Volumes[0].Projected == nil || len(pod.Volumes[0].Projected.Sources) != 1 {
		t.Fatalf("volumes = %+v, want one projected volume with one source", pod.Volumes)
	}
	token := pod.Volumes[0].Projected.Sources[0].ServiceAccountToken
	if token == nil || token.Audience != awsTokenAudience || token.ExpirationSeconds == nil ||
		*token.ExpirationSeconds != awsTokenSeconds || token.Path != awsTokenFile {
		t.Fatalf("token projection = %+v, want audience %s, %ds, path %s", token, awsTokenAudience, awsTokenSeconds, awsTokenFile)
	}

	container := pod.Containers[0]
	if len(container.VolumeMounts) != 1 || !container.VolumeMounts[0].ReadOnly || container.VolumeMounts[0].Name != awsTokenVolume {
		t.Fatalf("volume mounts = %+v, want the token volume mounted read-only", container.VolumeMounts)
	}
	env := map[string]string{}
	for _, variable := range container.Env {
		env[variable.Name] = variable.Value
	}
	want := map[string]string{
		"AWS_ROLE_ARN":                StorageRoleARN(sc, storage),
		"AWS_WEB_IDENTITY_TOKEN_FILE": container.VolumeMounts[0].MountPath + "/" + token.Path,
		"AWS_REGION":                  testRegion,
		"AWS_DEFAULT_REGION":          testRegion,
		EnvStorageBucket:              StorageBucketName(sc, storage),
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}

func TestStorageConfigValidate(t *testing.T) {
	if err := testStorage().Validate(); err != nil {
		t.Fatalf("a valid config was rejected: %v", err)
	}
	tests := map[string]func(*StorageConfig){
		"short account ID":             func(c *StorageConfig) { c.AccountID = "12345" },
		"account ID with a letter":     func(c *StorageConfig) { c.AccountID = "12345678901a" },
		"empty region":                 func(c *StorageConfig) { c.Region = "" },
		"upper-case region":            func(c *StorageConfig) { c.Region = "EU-WEST-2" },
		"issuer with a scheme":         func(c *StorageConfig) { c.Issuer = "https://" + testIssuer },
		"issuer with a trailing slash": func(c *StorageConfig) { c.Issuer = testIssuer + "/" },
		"boundary in another account":  func(c *StorageConfig) { c.PermissionsBoundaryARN = iamARNPrefix + "210987654321:policy/b" },
		"boundary that isn't a policy": func(c *StorageConfig) { c.PermissionsBoundaryARN = iamARNPrefix + testAccountID + ":role/b" },
	}
	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			config := testStorage()
			breakIt(config)
			if err := config.Validate(); err == nil {
				t.Errorf("Validate() accepted %+v", config)
			}
		})
	}
}
