# The problem this module exists for, measured rather than assumed: the live
# catalog is 1 807 283 bytes and a ConfigMap tops out at 1 MiB. The Kubernetes
# manifests next door therefore work with a demo catalog and cannot carry a real
# one — the pod never starts, because the ConfigMap is refused on creation.
#
# So the catalog needs object storage. This module provisions it, plus the role
# that may read it.

resource "aws_s3_bucket" "catalog" {
  bucket = "${var.name}-catalog"
}

# The catalog is the product. Versioning is what makes "restore yesterday's"
# possible at all — without it an overwrite is final.
resource "aws_s3_bucket_versioning" "catalog" {
  bucket = aws_s3_bucket.catalog.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "catalog" {
  bucket = aws_s3_bucket.catalog.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Four separate flags rather than one: each blocks a different way a bucket ends
# up public, and AWS treats them independently.
resource "aws_s3_bucket_public_access_block" "catalog" {
  bucket                  = aws_s3_bucket.catalog.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Versioning without expiry grows without bound: every save of a 1.7 MiB catalog
# keeps the previous copy forever.
resource "aws_s3_bucket_lifecycle_configuration" "catalog" {
  bucket = aws_s3_bucket.catalog.id

  rule {
    id     = "expire-old-catalog-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = var.catalog_retention_days
    }

    # An upload that failed halfway leaves parts behind that are invisible in
    # the object listing and still billed.
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

# Who may read the catalog. Written as a data source so the policy JSON is
# validated at plan time instead of being a string nobody checks.
data "aws_iam_policy_document" "catalog_read" {
  statement {
    sid    = "ReadCatalogObjects"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:GetObjectVersion",
    ]
    resources = ["${aws_s3_bucket.catalog.arn}/*"]
  }

  statement {
    sid       = "ListBucketOnly"
    effect    = "Allow"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.catalog.arn]
  }
}

resource "aws_iam_policy" "catalog_read" {
  name        = "${var.name}-catalog-read"
  description = "Read-only access to the kb-engine catalog bucket"
  policy      = data.aws_iam_policy_document.catalog_read.json
}

# The engine only ever reads the catalog: every write goes through the CLI on
# the owner's machine. A role that cannot write is therefore not a restriction
# to work around later, it is the actual shape of the system.
data "aws_iam_policy_document" "assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "engine" {
  name               = "${var.name}-engine"
  description        = "Identity the engine runs under when it reads the catalog"
  assume_role_policy = data.aws_iam_policy_document.assume.json
}

resource "aws_iam_role_policy_attachment" "engine_catalog_read" {
  role       = aws_iam_role.engine.name
  policy_arn = aws_iam_policy.catalog_read.arn
}
