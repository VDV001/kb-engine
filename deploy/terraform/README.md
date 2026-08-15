# Infrastructure as code

Object storage for the catalog, plus the identity allowed to read it. Runs
against LocalStack for free, and against a real AWS account unchanged.

## The problem it exists for

Measured, not assumed:

| | bytes | |
|---|---:|---|
| live catalog | 1 807 283 | 1.72 MiB |
| ConfigMap ceiling | 1 048 576 | 1.00 MiB |

The Kubernetes manifests next door mount the catalog from a ConfigMap. That works
for the demo catalog shipped with them and **cannot carry a real one** — the pod
never starts, because Kubernetes refuses the ConfigMap on creation. So the
catalog needs somewhere else to live, and that is what this module provisions.

## What it creates

- **S3 bucket** for `catalog.json`, with versioning (so "restore yesterday's" is
  possible at all), AES256 encryption, all four public-access blocks, and a
  lifecycle rule expiring non-current versions — versioning without expiry grows
  forever, and every save keeps another 1.7 MiB copy.
- **IAM policy** granting `GetObject`/`GetObjectVersion` on the bucket contents
  and `ListBucket` on the bucket, and nothing else.
- **IAM role** the engine assumes, with that policy attached. The engine only
  reads the catalog — every write goes through the CLI on the owner's machine —
  so a role that cannot write is the shape of the system, not a limitation to
  work around later.

## Running it

Against LocalStack:

```sh
docker run -d --name localstack -p 4566:4566 -e SERVICES=s3,iam,sts localstack/localstack:4.14
export TF_VAR_localstack_endpoint=http://localhost:4566
tofu init && tofu apply
```

⚠️ **The image tag is pinned to 4.14 deliberately.** `localstack/localstack:latest`
now points at the 2026.x line, which **exits with code 55 unless you supply a
licence token**. The 4.x line still runs without one. A `latest` that silently
became paid is exactly the reason this repository pins images.

Against real AWS: leave `localstack_endpoint` empty and let the provider find
your credentials the usual way.

## State lives in a bucket, not on a laptop

Local state is fine for one person and wrong the moment there are two: whoever
applies second overwrites what the first did, and nothing warns them.

The chicken-and-egg — the bucket holding the state cannot itself live in that
state — is solved by a separate `bootstrap/` module, applied once by hand:

```sh
cd bootstrap && tofu init && tofu apply    # creates kbengine-tfstate
```

Then the main module points at it. `backend.tf` is deliberately empty and every
value arrives at init time, so one repository can describe several environments:

```sh
tofu init \
  -backend-config=bucket=kbengine-tfstate \
  -backend-config=key=catalog/terraform.tfstate \
  -backend-config=region=eu-central-1 \
  -backend-config=use_lockfile=true
```

`use_lockfile` keeps the lock in S3 beside the state. Older setups needed a
DynamoDB table for locking alone — one more resource to create, pay for and
forget. CI checks the lock the only way that means anything: it plants someone
else's lock and requires the next run to refuse.

## Linting

`tflint` with the AWS plugin. The bundled rules know HCL, the plugin knows what
a valid S3 or IAM block looks like. It exits 2 on findings, so a green step is
evidence rather than decoration — verified by planting a badly-named unused
variable and watching three rules fire at once.

## OpenTofu and Terraform

The HCL targets both. CI runs `fmt`, `validate` and `plan` under **each** tool,
so "works with both" is measured rather than claimed.

⚠️ **The working directory does not survive the crossing.** Two separate
things, and the second one only surfaced on a real run:

- the lock file names `registry.opentofu.org/…` where Terraform expects
  `registry.terraform.io/…`, so the committed lock is OpenTofu's and pinning is
  real only on that path;
- `.terraform/` stores the backend configuration OpenTofu wrote, and Terraform
  refuses to decode it — `unsupported attribute assume_role_duration_seconds`.

So "the same HCL runs under both" is true, and "the same directory does" is not.
CI wipes `.terraform` before handing the module to Terraform.

## What this does NOT do

- **It does not run the engine.** Delivery from the bucket to the pod lives in
  the Helm chart (`catalog.source=s3`): an init container downloads the object
  into a shared volume before the engine starts, because the engine reads a file
  path and has no S3 client. The plain manifests still mount a ConfigMap and
  therefore still cannot carry a real catalog.
- **It has never run against real AWS.** Every resource here was applied to
  LocalStack only. LocalStack emulates the API, not the billing, the quotas or
  the IAM evaluation engine.
- **It has never held real state.** The remote backend works — CI creates the
  bucket, keeps state in it and proves a planted lock stops a second run — but
  every byte of that state described LocalStack resources.
