package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the application.
type Metrics struct {
	requestsTotal    *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	routingDecisions *prometheus.CounterVec
	nodeHealth       prometheus.GaugeVec
	nodeLatency      prometheus.GaugeVec
	nodeLoadedModels prometheus.GaugeVec
}

// Global metrics instance
var metrics *Metrics

// newMetrics initializes and registers the Prometheus metrics.
func newMetrics() *Metrics {
	return &Metrics{
		requestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests.",
			},
			[]string{"method", "path", "code"},
		),
		requestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "Histogram of HTTP request latencies.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		routingDecisions: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ollama_router_routing_decisions_total",
				Help: "Total number of routing decisions made.",
			},
			[]string{"decision", "model"},
		),
		nodeHealth: *promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ollama_router_node_health_status",
				Help: "Health status of backend nodes (1 for healthy, 0 for unhealthy).",
			},
			[]string{"node"},
		),
		nodeLatency: *promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ollama_router_node_latency_ms",
				Help: "Latency of backend nodes in milliseconds.",
			},
			[]string{"node"},
		),
		nodeLoadedModels: *promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ollama_router_node_loaded_models",
				Help: "Number of models currently loaded in VRAM on each node.",
			},
			[]string{"node"},
		),
	}
}

// middleware is a Chi middleware that records HTTP request metrics.
func (m *Metrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		duration := time.Since(start)

		path := r.URL.Path
		// Simple path grouping for better cardinality in metrics
		if strings.HasPrefix(path, "/api/") {
			parts := strings.SplitN(path, "/", 4)
			if len(parts) > 2 {
				path = "/" + parts[1] + "/" + parts[2]
			}
		}

		m.requestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(ww.Status())).Inc()
		m.requestDuration.WithLabelValues(r.Method, path).Observe(duration.Seconds())
	})
}

