variable "region" {
  type    = string
  default = "us-east-1"
}

variable "bucket_name" {
  type        = string
  description = "Globally unique bucket name."
}

variable "prefix" {
  type        = string
  default     = "riffle"
  description = "Key prefix the operator writes under (--archive-prefix)."
}

variable "infrequent_access_after_days" {
  type    = number
  default = 30
}

variable "expire_after_days" {
  type    = number
  default = 365
  validation {
    condition     = var.expire_after_days > var.infrequent_access_after_days
    error_message = "expire_after_days must be after infrequent_access_after_days."
  }
}

variable "create_user" {
  type        = bool
  default     = true
  description = "Create an IAM user and key for clusters without IRSA (k3s). On EKS, attach writer_policy_arn to the operator's role instead."
}
