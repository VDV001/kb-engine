# The bundled ruleset only knows HCL itself — unused variables, missing versions,
# naming. It has no idea what a valid S3 or IAM configuration looks like, so the
# AWS plugin is where the actual value is.
plugin "aws" {
  enabled = true
  version = "0.48.0"
  source  = "github.com/terraform-linters/tflint-ruleset-aws"

  # deep_check calls AWS to verify that referenced resources exist. Off here:
  # this module is verified against LocalStack, and a linter that needs an
  # account is a linter that does not run in CI.
  deep_check = false
}

rule "terraform_required_version" {
  enabled = true
}

rule "terraform_required_providers" {
  enabled = true
}

# Catches the class of mistake this module is most exposed to: a variable added
# during a refactor and then left behind, still documented, never read.
rule "terraform_unused_declarations" {
  enabled = true
}

rule "terraform_deprecated_interpolation" {
  enabled = true
}

rule "terraform_documented_variables" {
  enabled = true
}

rule "terraform_documented_outputs" {
  enabled = true
}

rule "terraform_typed_variables" {
  enabled = true
}

# Naming is enforced rather than suggested: mixed conventions in HCL are how a
# module becomes unreadable, and nothing else checks it.
rule "terraform_naming_convention" {
  enabled = true
  format  = "snake_case"
}
