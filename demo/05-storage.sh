#!/usr/bin/env bash
#
# 05-storage.sh: two teams ask for storage with one line each, and get an S3 bucket and an IAM role
# that only their own pods can use, with no AWS key anywhere. It proves it from inside a pod that
# runs as one claim's service account, with exactly the identity paved put in that claim's Rollout:
# the pod can write and read its own bucket, and AWS refuses it the other claim's bucket. Then it
# scans the cluster for AWS keys, deletes a claim, and checks from AWS that the role went and the
# bucket, with its data, stayed.
#
# Expects a cluster linked to AWS (PAVED_AWS_PROFILE=<profile> make demo), testsvc 0.1.1 in the local
# registry, and PAVED_AWS_PROFILE naming the same short-lived session, which the host-side checks use.
# It applies its two claims with kubectl rather than git, and deletes them at the end. Their buckets
# are kept, by design: re-running the demo adopts them.
#
# Recorded with:
#   asciinema rec --headless --window-size 120x34 --idle-time-limit 2 \
#     --command ./demo/05-storage.sh demo/05-storage.cast
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

CLAIM_NS=platform-claims
CLAIMS=(invoices avatars)
IMAGE=k3d-paved-registry:5001/testsvc:0.1.1
AWS_CLI_IMAGE=amazon/aws-cli:2.36.44
PROOF_POD=storage-proof
PAVED_AWS_PROFILE=${PAVED_AWS_PROFILE:-}

say() { printf '\n\033[1;36m# %s\033[0m\n' "$*"; }
show() { printf '\033[1;32m$\033[0m %s\n' "$*"; }
run() {
  show "$*"
  "$@"
}
fail() {
  printf '\n\033[1;31mdemo failed: %s\033[0m\n' "$*" >&2
  exit 1
}
# host_aws runs the AWS CLI on this machine with the demo's session, in the region the cluster was
# linked to. `aws login` doesn't save a region to the profile.
# The pager is off: in a terminal, as when recording, the AWS CLI would page its output and wait for
# a key.
host_aws() {
  local region
  region=$(kubectl get configmap paved-aws -n paved-system -o jsonpath='{.data.PAVED_AWS_REGION}' 2>/dev/null || true)
  env AWS_PAGER="" AWS_PROFILE="$PAVED_AWS_PROFILE" ${region:+AWS_REGION="$region"} aws "$@"
}

claim() { kubectl get serviceclaim "$1" -n "$CLAIM_NS" -o jsonpath="$2" 2>/dev/null || true; }
condition() { claim "$1" "{.status.conditions[?(@.type==\"$2\")].$3}"; }
state() {
  local name out="" storage reason ready
  for name in "${CLAIMS[@]}"; do
    # One read per claim, so the status and the reason come from the same version of it.
    IFS='|' read -r storage reason ready <<<"$(claim "$name" \
      '{.status.conditions[?(@.type=="StorageReady")].status}|{.status.conditions[?(@.type=="StorageReady")].reason}|{.status.conditions[?(@.type=="Ready")].status}')"
    out+=$(printf '%-9s StorageReady=%-5s %-18s Ready=%-5s   ' "$name" "$storage" "$reason" "$ready")
  done
  printf '%s' "$out"
}
all_ready() {
  local name
  for name in "${CLAIMS[@]}"; do
    [ "$(condition "$name" StorageReady status)" = True ] && [ "$(condition "$name" Ready status)" = True ] || return 1
  done
}
claims_gone() {
  local name
  for name in "$@"; do
    if kubectl get serviceclaim "$name" -n "$CLAIM_NS" >/dev/null 2>&1; then return 1; fi
  done
}
seconds_between() {
  python3 -c 'import sys; from datetime import datetime as d; p = lambda s: d.fromisoformat(s.replace("Z", "+00:00")); print(int((p(sys.argv[2]) - p(sys.argv[1])).total_seconds()))' "$1" "$2"
}

# wait_for DESCRIPTION TIMEOUT CHECK...: print the state each time it changes, until CHECK
# succeeds. Fails the demo if TIMEOUT seconds pass first.
wait_for() {
  local description=$1 timeout=$2 start=$SECONDS last="" now
  shift 2
  printf '  waiting until %s\n' "$description"
  while true; do
    now=$(state)
    if [ "$now" != "$last" ]; then
      printf '  %s\n' "$now"
      last=$now
    fi
    if "$@"; then return 0; fi
    if ((SECONDS - start > timeout)); then fail "timed out after ${timeout}s waiting until $description"; fi
    sleep 2
  done
}

for tool in aws kubectl python3 curl; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
[ -n "$PAVED_AWS_PROFILE" ] || fail "set PAVED_AWS_PROFILE to the profile the cluster was linked with"
host_aws sts get-caller-identity >/dev/null || fail "no valid session for profile $PAVED_AWS_PROFILE: aws login --profile $PAVED_AWS_PROFILE"
kubectl get configmap paved-aws -n paved-system >/dev/null 2>&1 ||
  fail "this cluster isn't linked to AWS: PAVED_AWS_PROFILE=$PAVED_AWS_PROFILE make demo"
kubectl -n ack-system wait --for=condition=Available deployment --all --timeout=60s >/dev/null ||
  fail "the ACK controllers aren't available in ack-system"
for name in "${CLAIMS[@]}"; do
  if kubectl get serviceclaim "$name" -n "$CLAIM_NS" >/dev/null 2>&1; then fail "claim $name already exists"; fi
done
curl -sf "http://localhost:5001/v2/testsvc/tags/list" | grep -q '"0.1.1"' || fail "$IMAGE is not in the registry"

say "Two teams need somewhere to put files. Each writes its usual claim, plus one line: storage: true"
started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
for name in "${CLAIMS[@]}"; do
  manifest=$(
    cat <<EOF
apiVersion: platform.paved.dev/v1alpha1
kind: ServiceClaim
metadata:
  name: $name
  namespace: $CLAIM_NS
spec:
  owner: team-$name
  image: $IMAGE
  port: 8080
  tier: batch
  sli:
    type: http-availability
  slo:
    objective: "99.5"
  scale:
    min: 1
    max: 1
  storage: true
EOF
  )
  if [ "$name" = "${CLAIMS[0]}" ]; then printf '%s\n' "$manifest"; fi
  printf '%s\n' "$manifest" | kubectl apply -f - >/dev/null
done
show "kubectl apply -f invoices.yaml -f avatars.yaml"

wait_for "ACK has created both roles and buckets in AWS, and both services are Ready" 600 all_ready
storage_ready_at=$(condition "${CLAIMS[0]}" StorageReady lastTransitionTime)
ready_at=$(condition "${CLAIMS[0]}" Ready lastTransitionTime)
storage_seconds=$(seconds_between "$started" "$storage_ready_at")
ready_seconds=$(seconds_between "$started" "$ready_at")
say "invoices: StorageReady ${storage_seconds}s and Ready ${ready_seconds}s after the claims were applied"
run kubectl get serviceclaims -n "$CLAIM_NS" "${CLAIMS[@]}"

own_bucket=$(claim "${CLAIMS[0]}" '{.status.storage.bucket}')
other_bucket=$(claim "${CLAIMS[1]}" '{.status.storage.bucket}')
role_arn=$(claim "${CLAIMS[0]}" '{.status.storage.roleARN}')
role_name=${role_arn##*/}

say "In the cluster: the ACK objects paved applied. In AWS: what ACK made of them"
run kubectl get roles.iam.services.k8s.aws,buckets.s3.services.k8s.aws -n "svc-${CLAIMS[0]}"
show "aws iam get-role --role-name $role_name"
host_aws iam get-role --role-name "$role_name" --output json | python3 -c '
import json, sys
role = json.load(sys.stdin)["Role"]
print("arn:      ", role["Arn"])
print("boundary: ", role["PermissionsBoundary"]["PermissionsBoundaryArn"])
statement = role["AssumeRolePolicyDocument"]["Statement"][0]
print("trusts:   ", statement["Principal"]["Federated"])
for key, value in sorted(statement["Condition"]["StringEquals"].items()):
    print("  only if ", key.split(":")[-1], "=", value)'
run host_aws s3api get-public-access-block --bucket "$own_bucket" --query PublicAccessBlockConfiguration --output text

say "Proof from inside the cluster: a pod running as invoices, with the AWS identity paved gave its Rollout"
rollout_json=$(kubectl get rollout "${CLAIMS[0]}" -n "svc-${CLAIMS[0]}" -o json)
proof_script='set -eu
echo "I am:    $(aws sts get-caller-identity --query Arn --output text)"
echo "written by invoices at $(date -u +%Y-%m-%dT%H:%M:%SZ)" | aws s3 cp - "s3://$PAVED_STORAGE_BUCKET/proof.txt"
echo "wrote:   s3://$PAVED_STORAGE_BUCKET/proof.txt"
echo "read:    $(aws s3 cp "s3://$PAVED_STORAGE_BUCKET/proof.txt" -)"
if aws s3 ls "s3://$OTHER_BUCKET/" 2>/tmp/list.err; then echo "UNEXPECTED: listed $OTHER_BUCKET"; exit 1; fi
grep -q AccessDenied /tmp/list.err || { cat /tmp/list.err; exit 1; }
echo "list    s3://$OTHER_BUCKET: AccessDenied"
if echo nope | aws s3 cp - "s3://$OTHER_BUCKET/intrusion.txt" 2>/tmp/put.err; then echo "UNEXPECTED: wrote to $OTHER_BUCKET"; exit 1; fi
grep -q AccessDenied /tmp/put.err || { cat /tmp/put.err; exit 1; }
echo "write   s3://$OTHER_BUCKET: AccessDenied"'
printf '%s' "$rollout_json" | python3 -c '
import json, sys
rollout = json.load(sys.stdin)
pod_name, image, other_bucket, script = sys.argv[1:5]
template = rollout["spec"]["template"]["spec"]
app = template["containers"][0]
identity = [e for e in app["env"] if e["name"].startswith("AWS_") or e["name"] == "PAVED_STORAGE_BUCKET"]
print(json.dumps({
    "apiVersion": "v1",
    "kind": "Pod",
    "metadata": {"name": pod_name, "namespace": rollout["metadata"]["namespace"]},
    "spec": {
        "serviceAccountName": template["serviceAccountName"],
        "restartPolicy": "Never",
        "securityContext": {"runAsNonRoot": True, "runAsUser": 1000, "seccompProfile": {"type": "RuntimeDefault"}},
        "volumes": template["volumes"],
        "containers": [{
            "name": "aws",
            "image": image,
            "command": ["/bin/sh", "-c", script],
            "env": identity + [{"name": "OTHER_BUCKET", "value": other_bucket}, {"name": "HOME", "value": "/tmp"}],
            "volumeMounts": app["volumeMounts"],
            "securityContext": {"allowPrivilegeEscalation": False, "capabilities": {"drop": ["ALL"]}},
        }],
    },
}))' "$PROOF_POD" "$AWS_CLI_IMAGE" "$other_bucket" "$proof_script" | kubectl apply -f - >/dev/null
show "kubectl get pod $PROOF_POD -n svc-${CLAIMS[0]} -o jsonpath='{.spec.containers[0].env[*].name}'"
kubectl get pod "$PROOF_POD" -n "svc-${CLAIMS[0]}" -o jsonpath='{.spec.containers[0].env[*].name}{"\n"}'
phase=""
for _ in $(seq 1 90); do
  phase=$(kubectl get pod "$PROOF_POD" -n "svc-${CLAIMS[0]}" -o jsonpath='{.status.phase}')
  if [ "$phase" = Succeeded ] || [ "$phase" = Failed ]; then break; fi
  sleep 2
done
run kubectl logs "$PROOF_POD" -n "svc-${CLAIMS[0]}"
[ "$phase" = Succeeded ] || fail "the proof pod ended $phase"

say "No AWS key anywhere in the cluster: every Secret, ConfigMap and pod, decoded and searched"
show "kubectl get secrets,configmaps,pods -A -o json | ./scan-for-aws-keys"
kubectl get secrets,configmaps,pods -A -o json | python3 -c '
import base64, gzip, json, re, sys
# An AWS access key ID is AKIA (long-lived) or ASIA (temporary) and 16 upper-case letters or digits.
# Case matters: matched case-insensitively, random base64 text looks like keys.
key_id = re.compile(r"(?<![A-Za-z0-9])(AKIA|ASIA)[A-Z0-9]{16}(?![A-Za-z0-9])")
secret_name = re.compile(r"aws_secret_access_key", re.IGNORECASE)
key_vars = {"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}

def decoded(value):
    data = base64.b64decode(value)
    # Helm keeps each release in a Secret as base64 of gzipped JSON: search the release itself.
    if data[:4] == b"H4sI":
        data = gzip.decompress(base64.b64decode(data))
    return data.decode("utf-8", "replace")

counts, hits = {}, []
for item in json.load(sys.stdin)["items"]:
    kind, ref = item["kind"], item["metadata"]["namespace"] + "/" + item["metadata"]["name"]
    counts[kind] = counts.get(kind, 0) + 1
    if kind == "Secret":
        values = [decoded(v) for v in (item.get("data") or {}).values()]
    elif kind == "ConfigMap":
        values = list((item.get("data") or {}).values())
    else:
        spec = item["spec"]
        containers = spec.get("containers", []) + spec.get("initContainers", [])
        values = []
        for c in containers:
            for e in c.get("env", []):
                if e["name"] in key_vars:
                    hits.append("%s %s: sets %s" % (kind, ref, e["name"]))
                values.append(e.get("value", ""))
    if any(key_id.search(v) or secret_name.search(v) for v in values):
        hits.append("%s %s: holds something shaped like an AWS key" % (kind, ref))
for kind in sorted(counts):
    print("  %-11s scanned %4d" % (kind + "s", counts[kind]))
print("  AWS keys found: %d" % len(hits))
for hit in hits:
    print("   ", hit)
sys.exit(1 if hits else 0)' || fail "found AWS key material in the cluster"

say "invoices is retired. Its role goes with it; its bucket, and the data in it, stays"
deleted_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
run kubectl delete serviceclaim "${CLAIMS[0]}" -n "$CLAIM_NS" --wait=false
wait_for "the claim, its namespace and its ACK objects are gone" 300 claims_gone "${CLAIMS[0]}"
gone_seconds=$(seconds_between "$deleted_at" "$(date -u +%Y-%m-%dT%H:%M:%SZ)")
show "aws iam get-role --role-name $role_name"
if host_aws iam get-role --role-name "$role_name" >/dev/null 2>"${TMPDIR:-/tmp}/get-role.err"; then
  fail "role $role_name still exists after its claim was deleted"
fi
grep -q NoSuchEntity "${TMPDIR:-/tmp}/get-role.err" || fail "unexpected error: $(cat "${TMPDIR:-/tmp}/get-role.err")"
echo "NoSuchEntity: the role is gone"
run host_aws s3 ls "s3://$own_bucket/"

say "Tidying up: avatars goes too. Both buckets stay, and are the account owner's to delete"
run kubectl delete serviceclaim "${CLAIMS[1]}" -n "$CLAIM_NS" --wait=false
wait_for "the avatars claim is gone" 300 claims_gone "${CLAIMS[1]}"

say "Done: storage in ${storage_seconds}s, Ready in ${ready_seconds}s, cross-claim access denied, no keys, role removed ${gone_seconds}s after its claim"
echo "storage_ready_seconds=$storage_seconds ready_seconds=$ready_seconds claim_deleted_seconds=$gone_seconds kept_buckets=$own_bucket,$other_bucket"
