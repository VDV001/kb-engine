# Deliberately empty: every value comes from -backend-config at init time.
#
# Hard-coding a bucket here would mean one repository can only ever describe one
# environment, and would make `tofu init -backend=false` — the way CI checks the
# HCL without touching any state — impossible for anyone reading this file.
#
# First time, once, by hand:
#
#   cd bootstrap && tofu init && tofu apply       # creates the state bucket
#
# Then, for the module itself:
#
#   tofu init \
#     -backend-config=bucket=kbengine-tfstate \
#     -backend-config=key=catalog/terraform.tfstate \
#     -backend-config=region=eu-central-1 \
#     -backend-config=use_lockfile=true
#
# use_lockfile puts the lock in S3 next to the state. Older setups needed a
# DynamoDB table purely for locking; that table is no longer required, and one
# less resource is one less thing to provision, pay for and forget about.
terraform {
  backend "s3" {}
}
