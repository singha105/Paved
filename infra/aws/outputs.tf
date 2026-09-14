# hack/cluster-up.sh reads these to configure the cluster's issuer, the ACK controllers and paved.

output "account_id" {
  value = local.account_id
}

output "region" {
  value = var.region
}

output "issuer_bucket" {
  value = aws_s3_bucket.issuer.bucket
}

# The OIDC issuer as IAM names it, without https://. It goes in every trust policy's condition keys.
output "issuer_host" {
  value = local.issuer_host
}

output "permissions_boundary_arn" {
  value = aws_iam_policy.workload_boundary.arn
}

output "ack_iam_role_arn" {
  value = aws_iam_role.ack["iam"].arn
}

output "ack_s3_role_arn" {
  value = aws_iam_role.ack["s3"].arn
}
