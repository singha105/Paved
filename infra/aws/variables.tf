variable "region" {
  description = "AWS region for the issuer bucket and every claim's bucket."
  type        = string
  default     = "eu-west-2"
}

variable "ack_namespace" {
  description = "Namespace the ACK controllers run in (hack/cluster-up.sh)."
  type        = string
  default     = "ack-system"
}

variable "ack_iam_service_account" {
  description = "Service account of the ACK IAM controller, from the iam-chart 1.9.0 default."
  type        = string
  default     = "ack-iam-controller"
}

variable "ack_s3_service_account" {
  description = "Service account of the ACK S3 controller, from the s3-chart 1.12.1 default."
  type        = string
  default     = "ack-s3-controller"
}
