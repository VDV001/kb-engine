terraform {
  # Both tools read this file. The floor is the version whose state format both
  # OpenTofu and Terraform still agree on; CI proves the claim by running the
  # module through each of them rather than asserting compatibility here.
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      # Pinned to 6.58.x, the newest line older than the seven-day floor this
      # repository applies to dependencies (6.60.0 was two days old here).
      #
      # Written with three components on purpose: "~> 6.58" allows any 6.x and
      # duly resolved to 6.60.0 on the first init — the rule was a comment while
      # the constraint said otherwise. "~> 6.58.0" allows 6.58.x only.
      version = "~> 6.58.0"
    }
  }
}
