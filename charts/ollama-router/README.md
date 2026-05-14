# ⚓ Ollama Router — Helm Chart

![Helm](https://img.shields.io/badge/Helm-3.18+-0F1689?logo=helm&logoColor=white)
![Chart Version](https://img.shields.io/badge/chart-0.3.0-blue)
![App Version](https://img.shields.io/badge/app-1.1.0-brightgreen)
![bjw-s common](https://img.shields.io/badge/bjw--s%20common-5.0.0-purple)
![License](https://img.shields.io/badge/License-MIT-green)

Kubernetes deployment for [`ollama-router`](../../README.md) — a
model-aware reverse proxy in front of multiple Ollama backends.

---

## 🧱 Built on bjw-s `common`

This chart is intentionally **thin**. It declares the
[`bjw-s common`](https://github.com/bjw-s-labs/helm-charts/tree/main/charts/library/common)
library as its only dependency and delegates rendering of every
Kubernetes resource (Deployment, Service, Ingress, ServiceMonitor,
PVC, …) to it.

```yaml
# Chart.yaml
dependencies:
  - name: common
    version: 5.0.0
    repository: https://bjw-s-labs.github.io/helm-charts
```

```yaml
# templates/common.yaml
{{- include "bjw-s.common.loader.all" . }}
```

That means **everything you can set on a `bjw-s common`-based chart you
can set here**. The local [`values.yaml`](values.yaml) only documents
the keys most relevant to this app — for anything else (resources,
nodeSelector, tolerations, affinity, PVCs, init containers, sidecars,
extra env, extra Services, NetworkPolicies, HPAs, RBAC, …) refer to
the upstream documentation:

| Resource | Link |
| --- | --- |
| 📖 Full values reference | [`bjw-s-labs/helm-charts/.../common/values.yaml`](https://github.com/bjw-s-labs/helm-charts/blob/main/charts/library/common/values.yaml) |
| 📚 Documentation site | [`bjw-s.dev/helm-charts/docs/common-library/introduction`](https://bjw-s.dev/helm-charts/docs/common-library/introduction/) |
| 🧪 Examples | [`bjw-s-labs/helm-charts/.../common/examples`](https://github.com/bjw-s-labs/helm-charts/tree/main/charts/library/common/examples) |
| 📝 Release notes | [`bjw-s-labs/helm-charts releases`](https://github.com/bjw-s-labs/helm-charts/releases?q=common) |

> **5.0.0 breaking changes** — minimum Kubernetes `1.31`, minimum Helm
> `3.18`, `automountServiceAccountToken` defaults to `false`, and a
> non-privileged ServiceAccount is created by default. See the
> [release notes](https://github.com/bjw-s-labs/helm-charts/releases?q=common)
> for the full list.

---

## 📋 Prerequisites

| Requirement | Version |
| --- | --- |
| Kubernetes | `1.31+` *(bjw-s common 5.x)* |
| Helm | `3.18+` *(bjw-s common 5.x)* |
| Prometheus Operator *(optional)* | needed for `serviceMonitor.main.enabled=true` |

---

## 🚀 Installation

```bash
helm repo add obeone https://obeone.org/helm-charts
helm repo update
helm install ollama-router obeone/ollama-router
```

With a values file:

```bash
helm install ollama-router obeone/ollama-router -f my-values.yaml
```

Inline override of the backend list:

```bash
helm install ollama-router obeone/ollama-router \
  --set-string controllers.main.containers.main.env.OLLAMA_NODES_JSON='[{"name":"n1","baseURL":"http://ollama:11434"}]'
```

---

## 🐳 Image Sources

Public images are mirrored to two registries:

| Registry | Repository |
| --- | --- |
| Docker Hub | `obeoneorg/ollama-router` |
| GHCR | `ghcr.io/obeone/ollama-router` |

The chart defaults to `ghcr.io/obeone/ollama-router:latest`.

---

## ⚙️ App-specific Values

This is the **short list** — everything else (resources, nodeSelector,
tolerations, affinity, persistence, PVCs, sidecars, init containers,
HPAs, NetworkPolicies, RBAC, RawResources, …) is inherited from
[`bjw-s common`](https://github.com/bjw-s-labs/helm-charts/blob/main/charts/library/common/values.yaml)
— refer to the upstream values file for the full surface.

### Image

| Key | Default |
| --- | --- |
| `controllers.main.containers.main.image.repository` | `ghcr.io/obeone/ollama-router` |
| `controllers.main.containers.main.image.tag` | `latest` *(empty → `chart.appVersion`)* |
| `controllers.main.containers.main.image.pullPolicy` | `IfNotPresent` |

### Application environment

Passed straight through to the binary — see the root
[README](../../README.md#%EF%B8%8F-configuration) for semantics.

| Key | Default |
| --- | --- |
| `…env.OLLAMA_NODES_JSON` | example two-node list — **override before deploying** |
| `…env.LOG_LEVEL` | `"info"` |
| `…env.POLL_INTERVAL_SECONDS` | `"5"` |
| `…env.MODEL_CACHE_TTL_SECONDS` | `"120"` |

> Scaling `controllers.main.replicas` past `1` trades cache locality
> (sessions are pinned in-memory per pod) for HA. Pick your battle.

### Probes & ports

Liveness and readiness both hit `GET /healthz` on port `8080`. The
metrics Service exposes `:9090/metrics` on a separate Service so it
can stay cluster-internal even when the main one is exposed.

### Ingress & ServiceMonitor

Both disabled by default in the example values. The chart wires them
up via the bjw-s `ingress` and `serviceMonitor` blocks — see the
upstream docs for the full schema.

---

## 🧪 Example `values.yaml`

```yaml
controllers:
  main:
    replicas: 2
    containers:
      main:
        image:
          repository: ghcr.io/obeone/ollama-router
          tag: latest
        env:
          LOG_LEVEL: "info"
          OLLAMA_NODES_JSON: |
            [
              {"name":"gpu-1","baseURL":"http://ollama-gpu-1:11434"},
              {"name":"gpu-2","baseURL":"http://ollama-gpu-2:11434"}
            ]

ingress:
  main:
    enabled: true
    ingressClassName: nginx
    hosts:
      - host: ollama.example.com
        paths:
          - path: /
            pathType: Prefix
            service: { identifier: main, port: http }
          - path: /v1
            pathType: Prefix
            service: { identifier: main, port: http }
    tls:
      - secretName: ollama-example-tls
        hosts: [ ollama.example.com ]

serviceMonitor:
  main:
    enabled: true
```

---

## ⬆️ Upgrading

```bash
helm upgrade ollama-router obeone/ollama-router --install --atomic
```

## 🗑️ Uninstalling

```bash
helm uninstall ollama-router
```

---

## 📝 License

MIT — same as the parent project.
