# Kubernetes manifests

Plain manifests, applied with `kubectl apply -k`. No Helm required.

```sh
kubectl apply -k deploy/k8s/base
kubectl rollout status deployment/kbengine --timeout=180s
kubectl port-forward svc/kbengine 8080:80
curl localhost:8080/readyz
```

## What is in here

```
base/               what a cluster can serve on its own
overlays/ci/        the image built from the working tree, one replica
overlays/ci-negative/  the same, with a catalog the engine cannot parse
```

| File in `base/` | Applied by default | Why |
|---|---|---|
| `configmap-catalog.yaml` | yes | a minimal invented catalog, so the pod has data to serve |
| `deployment.yaml` | yes | two replicas, both probes, non-root, read-only root filesystem |
| `service.yaml` | yes | ClusterIP on port 80 → container port 8080 |
| `ingress.yaml` | **no** | needs an ingress controller |
| `hpa.yaml` | **no** | needs metrics-server |

The two optional files are excluded from `base/kustomization.yaml` rather than deleted:
each needs a cluster component that may not be installed, and an object nobody
serves would let `kubectl apply` succeed while nothing actually works. Both files
carry the one-line change that switches them on.

## The two probes are not interchangeable

`/healthz` reports that the process answers. `/readyz` additionally parses the
catalog. They fail separately, and the cluster must react differently:

| State | `/healthz` | `/readyz` | What should happen |
|---|---|---|---|
| healthy | 200 `ok` | 200 `ready` | pod serves traffic |
| catalog malformed | 200 `ok` | 503 + decode error | pod leaves the Service, **keeps running** |
| process wedged | no answer | no answer | pod restarts |

Restarting a pod with a broken catalog does not help — it comes back broken. So
the broken-data case drives readiness only, which leaves the pod up and the
reason readable at the endpoint instead of hidden in a crash loop.

This table was measured against the binary, not assumed: a catalog whose
`meta.categories` is a list instead of an object produces exactly row two.

## What CI checks, and what it does not

The `k8s` job in `.github/workflows/ci.yml` builds the image from the working
tree, loads it into a kind cluster, applies these manifests, waits for rollout
and then reads a real API route **by body**, not by status code — a 200 alone
proves nothing when a SPA fallback answers every path. The same run asks for an
invented path and requires a 404.

It also runs a negative control: readiness is broken on purpose in a scratch copy
and the job must fail. A green run that has never been shown to go red is not
evidence.

Not checked by CI, and not claimed anywhere:

- **Ingress and HPA** — no controller, no metrics-server (see above).
- **Behaviour under load.** kind on a CI runner says nothing about production
  traffic, and neither do these manifests.
- **Multi-node scheduling, PodDisruptionBudget, network policy.** Single-node
  cluster; none of it is exercised.
- **Real cloud storage.** The catalog comes from a ConfigMap. A ConfigMap has a
  1 MiB ceiling, so a real catalog needs a PersistentVolume or an object store —
  this repository does not yet show either.
