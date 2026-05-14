package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// main is the entry point of the application.
// It initializes the configuration, logger, metrics, application state,
// and starts the HTTP servers for the application and for metrics.
// It also handles graceful shutdown.
func main() {
	setupLogger()
	cfg := loadConfig()
	metrics = newMetrics() // Assign to the global metrics variable

	appState := newAppState(cfg)

	// Setup background refresher
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go appState.backgroundRefresher(ctx, &wg)

	// Setup main router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogMiddleware(logger)) // Custom logger middleware
	r.Use(middleware.Recoverer)
	r.Use(metrics.middleware) // Add metrics middleware

	setupRoutes(r, appState)

	// Start main server
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: cfg.ReadTimeout + (5 * time.Second), // Should be longer than proxy timeout
		IdleTimeout:  120 * time.Second,
	}

	// Start metrics server in a separate goroutine
	metricsRouter := chi.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:    cfg.MetricsAddr,
		Handler: metricsRouter,
	}

	go func() {
		logger.Info("Metrics server starting up...", "address", cfg.MetricsAddr)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Could not start metrics server", "error", err)
		}
	}()

	go func() {
		logger.Info("Router starting up...", "address", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Could not start main server", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down servers...")

	cancel() // Signal background tasks to stop
	wg.Wait()  // Wait for background tasks to finish

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Main server forced to shutdown", "error", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Metrics server forced to shutdown", "error", err)
	}

	logger.Info("Server exiting")
}

