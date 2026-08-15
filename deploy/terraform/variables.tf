variable "name" {
  description = "Prefix for every resource, so two environments can share an account"
  type        = string
  default     = "kbengine"
}

variable "region" {
  description = "AWS region. LocalStack ignores it, a real account does not."
  type        = string
  default     = "eu-central-1"
}

# Empty means a real AWS account. Set to LocalStack's address to run the module
# against the emulator: every service endpoint moves at once, so there is no way
# to point half the module at production by accident.
variable "localstack_endpoint" {
  description = "LocalStack endpoint, e.g. http://localhost:4566. Empty targets real AWS."
  type        = string
  default     = ""
}

variable "catalog_retention_days" {
  description = "How long non-current catalog versions are kept before expiry"
  type        = number
  default     = 90

  validation {
    condition     = var.catalog_retention_days >= 7
    error_message = "The catalog is the product; a week is the least that makes a rollback possible."
  }
}
