# Bucket for Riffle's archive of redacted matched lines.

resource "aws_s3_bucket" "archive" {
  bucket = var.bucket_name
}

resource "aws_s3_bucket_public_access_block" "archive" {
  bucket                  = aws_s3_bucket.archive.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "archive" {
  bucket = aws_s3_bucket.archive.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "archive" {
  bucket = aws_s3_bucket.archive.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "archive" {
  bucket = aws_s3_bucket.archive.id
  rule {
    id     = "archive"
    status = "Enabled"
    filter {
      prefix = "${var.prefix}/"
    }
    transition {
      days          = var.infrequent_access_after_days
      storage_class = "STANDARD_IA"
    }
    expiration {
      days = var.expire_after_days
    }
    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

data "aws_iam_policy_document" "tls_only" {
  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.archive.arn,
      "${aws_s3_bucket.archive.arn}/*",
    ]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "archive" {
  bucket = aws_s3_bucket.archive.id
  policy = data.aws_iam_policy_document.tls_only.json
  # The public access block must exist first, or a policy race can fail the apply.
  depends_on = [aws_s3_bucket_public_access_block.archive]
}

# Write-only: the operator can add objects under the prefix and nothing else.
data "aws_iam_policy_document" "writer" {
  statement {
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.archive.arn}/${var.prefix}/*"]
  }
}

resource "aws_iam_policy" "writer" {
  name   = "riffle-archive-writer-${var.bucket_name}"
  policy = data.aws_iam_policy_document.writer.json
}

resource "aws_iam_user" "writer" {
  count = var.create_user ? 1 : 0
  name  = "riffle-archive-${var.bucket_name}"
}

resource "aws_iam_user_policy_attachment" "writer" {
  count      = var.create_user ? 1 : 0
  user       = aws_iam_user.writer[0].name
  policy_arn = aws_iam_policy.writer.arn
}

# ponytail: the secret key lands in state; keep state in an encrypted backend, or create the key out of band.
resource "aws_iam_access_key" "writer" {
  count = var.create_user ? 1 : 0
  user  = aws_iam_user.writer[0].name
}
