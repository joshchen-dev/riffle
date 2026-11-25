output "bucket" {
  value = aws_s3_bucket.archive.id
}

output "writer_policy_arn" {
  value = aws_iam_policy.writer.arn
}

output "access_key_id" {
  value = var.create_user ? aws_iam_access_key.writer[0].id : null
}

output "secret_access_key" {
  value     = var.create_user ? aws_iam_access_key.writer[0].secret : null
  sensitive = true
}
