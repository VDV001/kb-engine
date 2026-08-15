# Observability: Prometheus and Grafana

A minimal stack for looking at what the engine exports. Production clusters
usually run `kube-prometheus-stack`, which brings Alertmanager, node-exporter,
kube-state-metrics and a pile of custom resources — the right answer for a
cluster, the wrong one for showing that one service exports usable numbers.

```sh
kubectl apply -k deploy/k8s/overlays/ci          # the engine
kubectl apply -f deploy/k8s/observability/       # Prometheus + Grafana

kubectl port-forward svc/grafana 3000:3000
kubectl port-forward svc/prometheus 9090:9090
```

Then open <http://localhost:3000/d/kbengine/kb-engine>.

## ⚠️ Grafana here has no password

Anonymous access with the Admin role, login form disabled. Safe **only** under
the conditions this directory assumes: a throwaway cluster, no Ingress, reachable
solely through `kubectl port-forward`, holding nothing that outlives the check.

Anywhere else it is a hole. `grafana.yaml` says so at the point where the three
variables are set, with what to replace them with.

## The dashboard is code

`kbengine.json` lives in a ConfigMap. Dashboards drawn by hand in the UI live in
one installation's database: nobody can review them, and recreating one means
remembering what it looked like. `allowUiUpdates: false` keeps the copy on screen
from quietly diverging from the copy in git — edit the file, not the browser.

Seven panels: catalog size, unreadable entries, whether the catalog reads at all,
build version, and the RED trio — rate, errors, duration.

## Prometheus finds the engine itself

No addresses are written down anywhere. `kubernetes_sd_configs` asks the API
server which endpoints exist, and two relabel rules keep only the ones belonging
to the kbengine Service. Restart the pod and the target reappears at a new
address on its own.

The pod name is copied into a label deliberately: `instance` is an address, which
tells you where the scrape went, while `pod` is a name you can carry to
`kubectl logs`.

## What this does NOT do

- **No alerting.** Alertmanager is absent; these are graphs to look at, not pages
  that wake anyone up.
- **No persistence.** Prometheus keeps two hours in an `emptyDir`; the cluster
  dies with more history than this stack keeps.
- **No logs.** Metrics answer "what and when", logs answer "why this one
  request". Loki would be the counterpart — cheaper than Elasticsearch for one
  service with nobody to maintain it — and it is not here yet.
- **Nothing about load.** A single-node kind cluster with a generated trickle of
  requests says nothing about production traffic.
