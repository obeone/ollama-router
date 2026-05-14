# 🤖 Ollama Router Helm Chart 🚀

A Helm chart for deploying the Ollama model-aware router.

## Introduction ✨

This Helm chart deploys the Ollama Router, a smart proxy designed to route requests to multiple Ollama backend nodes. It intelligently distributes model inference requests, performs health checks on backend nodes, and manages model ownership caching for efficient load balancing and fault tolerance.

## Prerequisites 🛠️

* **Kubernetes**: A running Kubernetes cluster (version 1.19+ recommended).
* **Helm**: Helm 3.0+ installed.

## Installation 🚀

To install the `ollama-router` chart:

1. **Add the Helm repository**:

    ```bash
    helm repo add obeone https://obeone.org/helm-charts
    helm repo update
    ```

2. **Install the chart**:

    ```bash
    helm install ollama-router obeone/ollama-router
    ```

    This command deploys the Ollama Router on your Kubernetes cluster with the default configuration.

## Configuration ⚙️

You can customize the deployment by specifying values in a `values.yaml` file or by using the `--set` flag during installation.

Here are some key configurable parameters:

| Key                                          | Type    | Default                                                                                               | Description                                                               |
| :------------------------------------------- | :------ | :---------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------ |
| `controllers.main.enabled`                   | boolean | `true`                                                                                                | Enable or disable the main controller.                                    |
| `controllers.main.type`                      | string  | `deployment`                                                                                          | The type of controller to deploy.                                         |
| `controllers.main.replicas`                  | int     | `1`                                                                                                   | Number of replicas for the Ollama Router.                                 |
| `controllers.main.strategy`                  | string  | `RollingUpdate`                                                                                       | The strategy to use for deployments.                                      |
| `controllers.main.containers.main.image.repository` | string | `obeoneorg/ollama-router`                                                                             | Docker image repository for the Ollama Router.                            |
| `controllers.main.containers.main.image.pullPolicy` | string | `Always`                                                                                              | Image pull policy.                                                        |
| `controllers.main.containers.main.image.tag`        | string | `latest`                                                                                              | Docker image tag. Leave empty to use `chart.appVersion`.                  |
| `controllers.main.containers.main.env.LOG_LEVEL` | string | `"info"`                                                                                              | Log level for the application (e.g., `debug`, `info`, `warn`, `error`).   |
| `controllers.main.containers.main.env.OLLAMA_NODES_JSON` | string | `'[{"name":"ollama1","baseURL":"http://ollama:11434"},{"name":"ollama2","baseURL":"https://ollama2:11434"}]'` | JSON array of Ollama backend nodes to connect to.                         |
| `controllers.main.containers.main.env.POLL_INTERVAL_SECONDS` | string | `"5"`                                                                                                 | Health check polling interval in seconds.                                 |
| `controllers.main.containers.main.env.MODEL_CACHE_TTL_SECONDS` | string | `"120"`                                                                                               | Model ownership cache TTL (Time To Live) in seconds.                      |
| `controllers.main.containers.main.probes.liveness.enabled` | boolean | `true`                                                                                                | Enable the liveness probe.                                                |
| `controllers.main.containers.main.probes.readiness.enabled` | boolean | `true`                                                                                                | Enable the readiness probe.                                               |
| `service.main.controller`                    | string  | `main`                                                                                                | The controller that the main service should target.                       |
| `service.main.ports.http.port`               | int     | `8080`                                                                                                | HTTP port for the main service.                                           |
| `service.metrics.enabled`                    | boolean | `true`                                                                                                | Enable or disable the metrics service.                                    |
| `service.metrics.controller`                 | string  | `main`                                                                                                | The controller that the metrics service should target.                    |
| `service.metrics.ports.metrics.port`         | int     | `9090`                                                                                                | Port for Prometheus metrics.                                              |
| `ingress.main.enabled`                       | boolean | `true`                                                                                                | Enable or disable the main Ingress resource.                              |
| `ingress.main.hosts[0].host`                 | string  | `"ollama-router.obeone.cloud"`                                                                        | Hostname for the Ingress.                                                 |
| `ingress.main.tls[0].secretName`             | string  | `obeone-cloud-tls`                                                                                    | Kubernetes secret name for TLS certificate.                               |
| `serviceMonitor.main.enabled`                | boolean | `false`                                                                                               | Enables or disables the serviceMonitor.                                   |
| `serviceMonitor.main.serviceName`            | string  | `{{ include "bjw-s.common.lib.chart.names.fullname" $ }}-metrics` | Configures the target Service for the serviceMonitor. Helm templates can be used. |
| `serviceMonitor.main.endpoints[0].port`      | string  | `metrics`                                                                                             | The port for the serviceMonitor endpoint.                                 |
| `serviceMonitor.main.endpoints[0].scheme`    | string  | `http`                                                                                                | The scheme for the serviceMonitor endpoint.                               |
| `serviceMonitor.main.endpoints[0].path`      | string  | `/metrics`                                                                                            | The path for the serviceMonitor endpoint.                                 |
| `serviceMonitor.main.endpoints[0].interval`  | string  | `30s`                                                                                                 | The interval at which metrics are scraped.                                |
| `serviceMonitor.main.endpoints[0].scrapeTimeout` | string | `10s`                                                                                                 | The scrape timeout for the serviceMonitor endpoint.                       |

A comprehensive list of configurable options can be found in the `values.yaml` file and the upstream `common` library chart's `values.yaml` documentation.

## Upgrading ⬆️

To upgrade the chart to a newer version:

```bash
helm upgrade ollama-router obeone/ollama-router --install --atomic
```

## Uninstalling 🗑️

To uninstall the `ollama-router` chart:

```bash
helm uninstall ollama-router
```

This command removes all the Kubernetes components associated with the chart and deletes the release.
