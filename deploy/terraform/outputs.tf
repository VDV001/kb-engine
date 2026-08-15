output "catalog_bucket" {
  description = "Bucket holding catalog.json"
  value       = aws_s3_bucket.catalog.id
}

output "catalog_bucket_arn" {
  description = "ARN of the catalog bucket"
  value       = aws_s3_bucket.catalog.arn
}

output "engine_role_arn" {
  description = "Role the engine assumes to read the catalog"
  value       = aws_iam_role.engine.arn
}

output "catalog_read_policy_arn" {
  description = "Read-only policy attached to the engine role"
  value       = aws_iam_policy.catalog_read.arn
}
