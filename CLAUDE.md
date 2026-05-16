# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

Build, run, and test (Go 1.22, flat `package main` layout — no subpackages):

```bash
go build -o ollama-router .
go vet ./...
go test ./...                              # routing_test.go covers cache, _chooseBestNode, choose* helpers
go test -run TestChooseNodeForModel ./...  # single test
go run .                                   # uses OLLAMA_NODES_JSON default (obeone.cloud hosts)
OLLAMA_NODES_JSON='[{"name":"n1","baseURL":"http://localhost:11434"}]' go run .
docker build -t ollama-router .
```

Tests rely on the package globals `logger` and `metrics`. Any new `_test.go` file must either piggyback on the existing `TestMain` in `routing_test.go` (which initializes both) or initialize them itself — otherwise tests panic on first metric increment.

## Runtime Configuration

All config is environment-driven (see `config.go`):

- `OLLAMA_NODES_JSON` (required in prod): `[{"name":"...","baseURL":"..."}]`. Empty list → fatal.
- `POLL_INTERVAL_SECONDS` (5), `MODEL_CACHE_TTL_SECONDS` (120)
- `CONNECT_TIMEOUT_SECONDS` (5), `READ_TIMEOUT_SECONDS` (600)
- `LISTEN_ADDR` (`:8080`), `METRICS_ADDR` (`:9090` — separate server)
- `LOG_LEVEL` (`debug` default; parsed by `slog.Level.UnmarshalText`)

## Architecture

The router is a stateful model-aware reverse proxy in front of N Ollama backends. Everything lives in `package main`; each file owns one concern:

- `main.go` — wires Chi router, metrics server (separate mux on `METRICS_ADDR`), background refresher goroutine, graceful shutdown via SIGINT/SIGTERM + `context.WithCancel`.
- `config.go` — env loading.
- `state.go` — `AppState` (global) and per-backend `NodeState` (`OK`, `LoadedModels`, `LocalModels`, `LatencyMs`, guarded by `sync.RWMutex`). `backgroundRefresher` ticks every `PollInterval`, concurrently hitting each node's `/api/ps` and `/api/tags`; both failing marks the node unhealthy. Also calls `pruneModelCache`.
- `routing.go` — the core decision tree in `chooseNodeForModel`:
  1. cache hit (TTL-gated, `sync.Map`)
  2. model loaded in VRAM (`HIT_LOADED`)
  3. model present on disk (`HIT_LOCAL`)
  4. least-busy healthy fallback (`FALLBACK_LEAST_BUSY`)

  "Best" = fewest loaded models, tie-broken by lower latency (`_chooseBestNode`). Variants: `chooseNodeForPull`, `chooseNodeWithModel` (push/copy — must have model locally), `chooseNodeWithModelOrLoaded` (show).
- `handlers.go` — Chi routes. Handler shapes:
  - Model-aware proxy (`/api/generate|chat|embeddings|embed`, `/v1/chat/completions`, `/v1/embeddings`) reads+restores body, extracts model, picks node, proxies.
  - Aggregators (`/api/tags` GET, `/api/ps` GET, `/v1/models`) fan out across nodes and dedupe/union; `/v1/models` reuses `aggregateTags` and reshapes to OpenAI format.
  - Specific routing (`pull`/`push`/`copy`/`create`/`show`) uses the matching `choose*` helper.
  - `DELETE /api/delete` broadcasts to all healthy nodes using DELETE and forwards the first 2xx/3xx response (closes losing response bodies).
  - `/api/version` proxies to the least-busy healthy node.
  - `/api/*` catch-all tries model-aware for POST, else least-busy.
  - `GET|HEAD /` returns `Ollama is running` for `ollama` CLI compatibility; `/healthz` exposes per-node state + cache size.
- `utils.go` — `proxyRequest` wraps the per-node `httputil.ReverseProxy` with a `http.TimeoutHandler(ReadTimeout)` and a custom Director (strips `Accept-Encoding`, rewrites Host). `fetchAPI[T]` is the generic typed JSON client. `extractModelFromBody` looks for `model` → `name` → `source`.
- `models.go` — wire types for Ollama and OpenAI-compat responses.
- `metrics.go` — Prometheus metrics via `promauto`; the middleware groups paths to `/a/b` to keep cardinality bounded. `nodeHealth`/`nodeLatency`/`nodeLoadedModels` gauges are updated inside `refreshNodeState`.

## Invariants to Preserve

- Model names are always lowercased before cache/map lookups, and the `"foo:tag"` → `"foo"` base alias is inserted into both `LoadedModels` and `LocalModels` (see `refreshNodeState`). Any new map read must match this normalization.
- `NodeState.mu` protects every field on `NodeState`. Never read `LoadedModels`/`LocalModels`/`OK`/`LatencyMs` without the lock.
- Model-aware POST handlers must `io.ReadAll` + restore `r.Body` with `io.NopCloser(bytes.NewBuffer(...))` before proxying; `extractModelFromBody` consumes the body.
- `proxyRequest` mutates `node.Proxy.Director` on every call — it is effectively single-tenant per node, which is fine because the director is deterministic from `node.BaseURL` + incoming `r.URL.Path`, but don't parallelize modifications to `Proxy`.
- The metrics server runs on a different address/mux from the main router; don't add `/metrics` to the main Chi router.

## Deployment

`Dockerfile` does a static build (`CGO_ENABLED=0`, `-ldflags="-w -s"`) into `gcr.io/distroless/static-debian12`. Helm chart lives in `charts/ollama-router/`.
