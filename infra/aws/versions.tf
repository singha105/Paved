terraform {
  required_version = ">= 1.16.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.64.0"
    }
  }
}

# Credentials come from the environment: `make aws-up` sets AWS_PROFILE to a short-lived session
# (`aws login --profile paved`). Nothing here names a key or a profile.
provider "aws" {
  region = var.region

  default_tags {
    tags = {
      "paved.dev/stack" = "paved"
    }
  }
}
