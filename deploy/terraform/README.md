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

## OpenTofu and Terraform

The HCL targets both. CI runs `fmt`, `validate` and `plan` under **each** tool,
so "works with both" is measured rather than claimed.

⚠️ **The lock file is not shared between them.** OpenTofu writes provider
addresses as `registry.opentofu.org/…`, Terraform expects `registry.terraform.io/…`;
the committed lock is OpenTofu's, and the Terraform job generates its own.
Locking is therefore real only on the OpenTofu path.

## What this does NOT do

- **It does not deliver the catalog to the pod.** The engine reads `--catalog`
  from a file path and has no S3 client. Bridging the two needs an init container
  that downloads the object, or a PersistentVolume — neither exists yet, and the
  manifests still mount a ConfigMap.
- **It has never run against real AWS.** Every resource here was applied to
  LocalStack only. LocalStack emulates the API, not the billing, the quotas or
  the IAM evaluation engine.
- **No remote state.** State stays local, which is fine for one operator and
  wrong for a team; the backend is deliberately absent rather than half-configured.
