# syntax=docker/dockerfile:1

# ---- Build Stage ----
# Use the official Go image as a builder.
FROM golang:1.22-alpine AS builder

# Set the working directory inside the container.
WORKDIR /app

# Copy Go module and source files.
COPY --link go.mod go.sum ./
# Download Go module dependencies.
RUN go mod download

# Copy the rest of the application's source code.
COPY --link . .

# Build the application statically.
# CGO_ENABLED=0 disables Cgo to create a static binary.
# -ldflags="-w -s" removes debug information to reduce binary size.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /ollama-router .

# ---- Final Stage ----
# Use a minimal, non-root base image for the final container.
# "distroless" images contain only the application and its runtime dependencies.
FROM gcr.io/distroless/static-debian12

# Copy the static binary from the builder stage.
COPY --from=builder /ollama-router /ollama-router

# Expose the port the app runs on.
EXPOSE 8080
# Expose the metrics port.
EXPOSE 9090

# Set the binary as the entrypoint.
ENTRYPOINT ["/ollama-router"]
