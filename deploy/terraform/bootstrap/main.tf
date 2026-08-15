# The bucket that holds the state of everything else, and therefore cannot live
# in that state itself — the chicken-and-egg every Terraform setup meets. This
# module is applied ONCE with local state, and its own state file matters far
# less: losing it costs an import, not the infrastructure.
#
# Kept as a separate module rather than a flag on the main one, because "run me
# once, by hand, before anything else" is a different lifecycle, and mixing the
# two is how a `destroy` takes the state bucket with it.

terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.58.0"
    }
  }
}

variable "name" {
  description = "Prefix for the state bucket"
  type        = string
  default     = "kbengine"
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "eu-central-1"
}

variable "localstack_endpoint" {
  description = "LocalStack endpoint, e.g. http://localhost:4566. Empty targets real AWS."
  type        = string
  default     = ""
}

provider "aws" {
  region                      = var.region
  access_key                  = var.localstack_endpoint != "" ? "test" : null
  secret_key                  = var.localstack_endpoint != "" ? "test" : null
  skip_credentials_validation = var.localstack_endpoint != ""
  skip_metadata_api_check     = var.localstack_endpoint != ""
  skip_requesting_account_id  = var.localstack_endpoint != ""
  s3_use_path_style           = var.localstack_endpoint != ""

  dynamic "endpoints" {
    for_each = var.localstack_endpoint != "" ? [1] : []
    content {
      s3 = var.localstack_endpoint
    }
  }
}

resource "aws_s3_bucket" "state" {
  bucket = "${var.name}-tfstate"
}

# Non-negotiable here, unlike on the catalog bucket where it is merely wise: a
# corrupted state overwrite without versioning means rebuilding the mapping
# between config and real resources by hand.
resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# State lists every resource, and for some providers it holds secrets in clear
# text. A public state bucket is the worst kind of leak.
resource "aws_s3_bucket_public_access_block" "state" {
  bucket                  = aws_s3_bucket.state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

output "state_bucket" {
  description = "Bucket to point the main module's backend at"
  value       = aws_s3_bucket.state.id
}
