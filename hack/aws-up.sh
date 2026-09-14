#!/usr/bin/env bash
#
# aws-up.sh: create or update paved's one-time AWS resources (infra/aws): the public issuer bucket,
# the IAM OIDC provider, the permissions boundary for claims' roles, and the ACK controllers' roles.
# Idempotent: a second run changes nothing.
#
# It runs only with a short-lived session. Log in first, then:
#   aws login --profile paved
#   AWS_PROFILE=paved ./hack/aws-up.sh        (or PAVED_AWS_PROFILE=paved make aws-up)
# PAVED_AWS_REGION picks the region (default eu-west-2).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

for tool in aws terraform; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool not found on PATH: $tool"
done
[ -n "${AWS_PROFILE:-}" ] || fail "set AWS_PROFILE to a profile you logged in to with: aws login --profile <name>"

# No long-lived credentials anywhere, including on this machine (DECISIONS.md, ADR-025). A profile
# with a stored access key is refused rather than used; the key itself is never printed.
if aws configure get aws_access_key_id --profile "$AWS_PROFILE" >/dev/null 2>&1; then
  fail "profile '$AWS_PROFILE' stores an access key. Use a short-lived session: aws login --profile <name>"
fi
aws sts get-caller-identity --output text --query Arn >/dev/null ||
  fail "profile '$AWS_PROFILE' has no valid session. Run: aws login --profile $AWS_PROFILE"

region_args=()
if [ -n "${PAVED_AWS_REGION:-}" ]; then
  region_args=(-var "region=${PAVED_AWS_REGION}")
fi

terraform -chdir="$ROOT/infra/aws" init -input=false >/dev/null
terraform -chdir="$ROOT/infra/aws" apply -input=false -auto-approve ${region_args[@]+"${region_args[@]}"}
