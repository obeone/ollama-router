# syntax=docker/dockerfile:1

# ---- Build Stage ----
# Build on the native BUILDPLATFORM and cross-compile to the requested
# target. For a static Go binary this is far faster than running the
# whole toolchain under QEMU emulation on multi-arch builds.
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder

# Injected automatically by BuildKit for the requested target platform.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

# Resolve modules first so this layer stays cached until go.mod/go.sum change.
COPY --link go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download

# Build a static, cross-compiled, reproducible binary.
COPY --link . .
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-w -s" -o /ollama-router .

# ---- Final Stage ----
# distroless static + nonroot: no shell, no package manager, runs as
# uid 65532. No HEALTHCHECK: the image has no shell/curl/wget, so health
# is checked by the orchestrator (Helm probes hit /healthz).
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/obeone/ollama-router" \
      org.opencontainers.image.description="Model-aware reverse proxy in front of N Ollama backends" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

# Copy the static binary from the builder stage.
COPY --from=builder /ollama-router /ollama-router

# Main router and the separate Prometheus metrics server.
EXPOSE 8080
EXPOSE 9090

USER nonroot
ENTRYPOINT ["/ollama-router"]
