# The one-time AWS side of paved's storage (DECISIONS.md, ADR-025):
#
#   - A public issuer: an S3 bucket where anyone can read two files, the cluster's OIDC discovery
#     document and its token signing keys, and nothing else. hack/cluster-up.sh rewrites both
#     whenever it creates a cluster.
#   - An IAM OIDC provider for that issuer, so AWS STS accepts the cluster's service-account tokens.
#   - The permissions boundary every claim's role is created under.
#   - A role for each ACK controller. Only that controller's service account can assume it, and it
#     can manage only paved's roles or paved's buckets.
#
# No access key is created, here or anywhere else.

data "aws_caller_identity" "current" {}

locals {
  account_id = data.aws_caller_identity.current.account_id

  # Bucket names are shared by every AWS account. A hash keeps this one unique without putting
  # the account ID in a public URL.
  issuer_bucket = "paved-oidc-${substr(sha256("${local.account_id}/${var.region}"), 0, 12)}"
  issuer_host   = "${local.issuer_bucket}.s3.${var.region}.amazonaws.com"

  sts_audience = "sts.amazonaws.com"

  # Claims' buckets are paved-<claim>-<hash> and their roles live under /paved/workloads/
  # (internal/builders/storage.go).
  workload_buckets = "arn:aws:s3:::paved-*"
  workload_roles   = "arn:aws:iam::${local.account_id}:role/paved/workloads/*"
  issuer_arns      = [aws_s3_bucket.issuer.arn, "${aws_s3_bucket.issuer.arn}/*"]
}

# --- The public issuer ---------------------------------------------------------------------------

resource "aws_s3_bucket" "issuer" {
  bucket = local.issuer_bucket
  # It only ever holds the two issuer documents, which cluster-up writes again.
  force_destroy = true
}

resource "aws_s3_bucket_ownership_controls" "issuer" {
  bucket = aws_s3_bucket.issuer.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

# ACLs stay blocked. Only a bucket policy may grant public reads, and the one below grants
# exactly two objects.
resource "aws_s3_bucket_public_access_block" "issuer" {
  bucket                  = aws_s3_bucket.issuer.id
  block_public_acls       = true
  ignore_public_acls      = true
  block_public_policy     = false
  restrict_public_buckets = false
}

data "aws_iam_policy_document" "issuer_public_read" {
  statement {
    sid     = "ReadTheIssuerDocuments"
    actions = ["s3:GetObject"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    resources = [
      "${aws_s3_bucket.issuer.arn}/.well-known/openid-configuration",
      "${aws_s3_bucket.issuer.arn}/openid/v1/jwks",
    ]
  }
}

resource "aws_s3_bucket_policy" "issuer" {
  bucket     = aws_s3_bucket.issuer.id
  policy     = data.aws_iam_policy_document.issuer_public_read.json
  depends_on = [aws_s3_bucket_public_access_block.issuer]
}

resource "aws_iam_openid_connect_provider" "cluster" {
  url            = "https://${local.issuer_host}"
  client_id_list = [local.sts_audience]
}

# --- What a claim's role can ever do -------------------------------------------------------------

data "aws_iam_policy_document" "workload_boundary" {
  statement {
    sid       = "ObjectsInPavedBuckets"
    actions   = ["s3:GetBucketLocation", "s3:ListBucket", "s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
    resources = [local.workload_buckets, "${local.workload_buckets}/*"]
  }
  statement {
    sid       = "NeverTheIssuer"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = local.issuer_arns
  }
}

resource "aws_iam_policy" "workload_boundary" {
  name        = "paved-workload-boundary"
  path        = "/paved/"
  description = "Caps every role paved creates for a ServiceClaim to objects in paved buckets."
  policy      = data.aws_iam_policy_document.workload_boundary.json
}

# --- The ACK controllers -------------------------------------------------------------------------

locals {
  ack_controllers = {
    iam = var.ack_iam_service_account
    s3  = var.ack_s3_service_account
  }
}

data "aws_iam_policy_document" "ack_trust" {
  for_each = local.ack_controllers

  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.cluster.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.issuer_host}:sub"
      values   = ["system:serviceaccount:${var.ack_namespace}:${each.value}"]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.issuer_host}:aud"
      values   = [local.sts_audience]
    }
  }
}

resource "aws_iam_role" "ack" {
  for_each = local.ack_controllers

  name                 = "paved-ack-${each.key}-controller"
  path                 = "/paved/controllers/"
  description          = "The ACK ${each.key} controller in the paved cluster."
  assume_role_policy   = data.aws_iam_policy_document.ack_trust[each.key].json
  max_session_duration = 3600
}

# The IAM controller creates and updates claims' roles. It can't create one without the boundary,
# can't remove the boundary, and can't touch a role outside /paved/workloads/, including its own.
data "aws_iam_policy_document" "ack_iam" {
  statement {
    sid       = "CreateRolesOnlyUnderTheBoundary"
    actions   = ["iam:CreateRole", "iam:PutRolePermissionsBoundary"]
    resources = [local.workload_roles]
    condition {
      test     = "StringEquals"
      variable = "iam:PermissionsBoundary"
      values   = [aws_iam_policy.workload_boundary.arn]
    }
  }
  statement {
    sid = "ManageClaimRoles"
    actions = [
      "iam:GetRole", "iam:DeleteRole", "iam:UpdateRole", "iam:UpdateRoleDescription", "iam:UpdateAssumeRolePolicy",
      "iam:TagRole", "iam:UntagRole", "iam:ListRoleTags",
      "iam:ListRolePolicies", "iam:GetRolePolicy", "iam:PutRolePolicy", "iam:DeleteRolePolicy",
      "iam:ListAttachedRolePolicies",
    ]
    resources = [local.workload_roles]
  }
  statement {
    sid       = "KeepTheBoundary"
    effect    = "Deny"
    actions   = ["iam:DeleteRolePermissionsBoundary"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "ack_iam" {
  name   = "manage-claim-roles"
  role   = aws_iam_role.ack["iam"].id
  policy = data.aws_iam_policy_document.ack_iam.json
}

# The S3 controller manages paved buckets' configuration. It can never read or write the data in
# them, and never touch the issuer bucket.
data "aws_iam_policy_document" "ack_s3" {
  statement {
    sid       = "ManagePavedBuckets"
    actions   = ["s3:CreateBucket", "s3:DeleteBucket", "s3:Get*", "s3:List*", "s3:Put*"]
    resources = [local.workload_buckets]
  }
  statement {
    sid       = "FindBuckets"
    actions   = ["s3:ListAllMyBuckets"]
    resources = ["*"]
  }
  statement {
    sid       = "NoObjectData"
    effect    = "Deny"
    actions   = ["s3:GetObject*", "s3:PutObject*", "s3:DeleteObject*", "s3:RestoreObject", "s3:ReplicateObject", "s3:ReplicateDelete"]
    resources = ["*"]
  }
  statement {
    sid       = "NeverTheIssuer"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = local.issuer_arns
  }
}

resource "aws_iam_role_policy" "ack_s3" {
  name   = "manage-paved-buckets"
  role   = aws_iam_role.ack["s3"].id
  policy = data.aws_iam_policy_document.ack_s3.json
}
