provider "aws" {
  region = var.region

  # LocalStack accepts any credentials, and putting obvious fakes here means a
  # missing profile fails loudly instead of quietly reaching a real account.
  access_key = var.localstack_endpoint != "" ? "test" : null
  secret_key = var.localstack_endpoint != "" ? "test" : null

  # Checks that cost a round-trip to AWS and mean nothing against an emulator.
  skip_credentials_validation = var.localstack_endpoint != ""
  skip_metadata_api_check     = var.localstack_endpoint != ""
  skip_requesting_account_id  = var.localstack_endpoint != ""

  # Path-style addressing: LocalStack serves buckets as /bucket-name, while real
  # S3 puts the bucket in the hostname.
  s3_use_path_style = var.localstack_endpoint != ""

  dynamic "endpoints" {
    for_each = var.localstack_endpoint != "" ? [1] : []
    content {
      s3  = var.localstack_endpoint
      iam = var.localstack_endpoint
      sts = var.localstack_endpoint
    }
  }
}
