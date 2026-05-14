// Package main provides utility functions and common helpers for the Ollama router.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/lmittmann/tint"
)

// logger is the global structured logger instance.
var logger *slog.Logger

// setupLogger initializes the structured, colored logger for the application.
// It reads the log level from the "LOG_LEVEL" environment variable (defaulting to "debug")
// and configures a tint handler for colored output to standard output.
func setupLogger() {
	logLevelStr := getEnv("LOG_LEVEL", "debug")
	var level slog.Level
	if err := level.UnmarshalText([]byte(logLevelStr)); err != nil {
		level = slog.LevelDebug // Default to Debug if parsing fails
	}
	logger = slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level:      level,
		TimeFormat: time.RFC3339,
	}))
}

// slogMiddleware provides a custom structured logging middleware for the Chi router.
// It logs details of each HTTP request, including status, method, path, duration,
// remote address, and a unique request ID.
func slogMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			duration := time.Since(start)

			logger.Info("HTTP Request",
				"status", ww.Status(),
				"method", r.Method,
				"path", r.URL.Path,
				"duration", duration,
				"remote_addr", r.RemoteAddr,
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

// proxyRequest forwards the HTTP request to the selected backend Ollama node.
// It configures a custom director for the reverse proxy to modify request headers and URL,
// and applies a timeout to the entire roundtrip.
func (appState *AppState) proxyRequest(w http.ResponseWriter, r *http.Request, node *NodeState) {
	proxy := node.Proxy

	// Custom director to set headers and URL specific to the target node
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = node.BaseURL.Scheme
		req.URL.Host = node.BaseURL.Host
		req.URL.Path = r.URL.Path
		req.Host = node.BaseURL.Host
		// Clean up headers that shouldn't be forwarded to the backend
		req.Header.Del("Accept-Encoding")
	}

	// Apply a timeout to the entire proxy operation, including reading the response body.
	proxyWithTimeout := http.TimeoutHandler(proxy, appState.Config.ReadTimeout, "backend timeout")

	proxyWithTimeout.ServeHTTP(w, r)
}

// fetchAPI is a generic helper to perform an API call and decode the JSON response into a specified type.
// It handles request creation, execution, error checking for status codes, and JSON decoding.
func fetchAPI[T any](ctx context.Context, client *http.Client, method, url string, body io.Reader) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("api request failed with status %d", resp.StatusCode)
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode json response: %w", err)
	}
	return &result, nil
}

// extractModelFromBody attempts to extract a model name from the request body.
// It checks for "model", "name", or "source" fields in the JSON payload.
func extractModelFromBody(body []byte) (string, error) {
	var req GenericOllamaRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}
	if req.Model != "" {
		return req.Model, nil
	}
	if req.Name != "" {
		return req.Name, nil
	}
	if req.Source != "" {
		return req.Source, nil
	}
	return "", fmt.Errorf("no model, name, or source field found in request body")
}

// respondJSON writes a JSON payload to the HTTP response with the specified status code.
// It sets the Content-Type header to application/json.
func respondJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if payload != nil {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			logger.Error("Failed to encode JSON response", "error", err)
		}
	}
}

// respondError writes a standard JSON error message to the HTTP response.
// The error message is provided in a "error" field within the JSON object.
func respondError(w http.ResponseWriter, code int, message string) {
	respondJSON(w, code, map[string]string{"error": message})
}

// forwardResponse copies headers, status code, and body from a backend HTTP response
// to the client's HTTP response. It ensures the backend response body is closed.
func forwardResponse(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	resp.Body.Close()
}

// getEnv retrieves an environment variable by its key.
// If the environment variable is not set, it returns the provided fallback value.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// getEnvDuration retrieves an environment variable by its key,
// attempts to convert its value to an integer representing seconds, and returns it as a time.Duration.
// If the variable is not set or cannot be parsed, it returns the provided fallback duration.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		if intVal, err := strconv.Atoi(value); err == nil {
			return time.Duration(intVal) * time.Second
		}
	}
	return fallback
}