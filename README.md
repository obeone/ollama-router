# 🧠 Ollama Router 🚀

The Ollama Router is an intelligent, model-aware proxy designed to efficiently manage and route inference requests to multiple Ollama backend nodes. It acts as a central entry point for your applications, providing advanced load balancing, health checking, and model ownership caching to ensure optimal performance, reliability, and scalability for your Ollama deployments.

## ✨ Features

* **Model-Aware Routing**: Routes inference requests to the appropriate Ollama backend based on model availability and node health.
* **Load Balancing**: Distributes requests across multiple Ollama nodes to prevent overload and ensure efficient resource utilization.
* **Health Checks**: Continuously monitors the health and responsiveness of backend Ollama nodes, automatically removing unhealthy nodes from rotation.
* **Model Ownership Caching**: Caches information about which models are loaded on which nodes, reducing redundant requests and improving response times.
* **Scalability**: Easily scale your Ollama infrastructure by adding more backend nodes without reconfiguring your client applications.
* **Fault Tolerance**: Automatically handles node failures by redirecting requests to healthy nodes, ensuring high availability.
* **Prometheus Metrics**: Exposes detailed metrics for monitoring router performance and backend node status.

## 🚀 Getting Started

To get the Ollama Router up and running, follow these steps:

### Prerequisites

* Go (version 1.22 or newer)
* Docker (for building and running containerized)
* Access to Ollama backend nodes

### Installation

1. **Clone the repository**:

    ```bash
    git clone https://github.com/obeone/ollama-router.git
    cd ollama-router
    ```

2. **Build the application**:

    ```bash
    go build -o ollama-router .
    ```

3. **Run the application**:
    You need to configure the Ollama backend nodes using the `OLLAMA_NODES_JSON` environment variable.

    ```bash
    OLLAMA_NODES_JSON='[{"name":"ollama1","baseURL":"http://localhost:11434"}]' ./ollama-router
    ```

    Replace the `baseURL` with the actual URLs of your Ollama instances.

### Docker

You can also build and run the Ollama Router using Docker:

1. **Build the Docker image**:

    ```bash
    docker build -t ollama-router .
    ```

2. **Run the Docker container**:

    ```bash
    docker run -p 8080:8080 -e OLLAMA_NODES_JSON='[{"name":"ollama1","baseURL":"http://host.docker.internal:11434"}]' ollama-router
    ```

    * `host.docker.internal` is used to access the host machine's localhost from within the Docker container. Ensure your Ollama backend is accessible at this address.
    * Adjust the port mapping (`-p 8080:8080`) if needed.

## 🐳 Helm Chart Deployment

For Kubernetes deployments, a Helm chart is available:

Navigate to the [`charts/ollama-router/`](charts/ollama-router/) directory for detailed instructions on how to deploy the Ollama Router using Helm.

## ⚙️ Configuration

The Ollama Router can be configured via environment variables:

* `LOG_LEVEL`: Sets the logging level (e.g., `debug`, `info`, `warn`, `error`). Default is `info`.
* `OLLAMA_NODES_JSON`: A JSON array specifying the Ollama backend nodes. Example: `[{"name":"ollama1","baseURL":"http://ollama1.example.com"},{"name":"ollama2","baseURL":"http://ollama2.example.com"}]`.
* `POLL_INTERVAL_SECONDS`: The interval (in seconds) for health checks of Ollama nodes. Default is `5`.
* `MODEL_CACHE_TTL_SECONDS`: Time-to-live (in seconds) for the model ownership cache. Default is `120`.

## 🤝 Contributing

Contributions are welcome! Please feel free to open issues or submit pull requests.

## 📄 License

This project is licensed under the MIT License.
