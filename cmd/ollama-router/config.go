// Package main provides the core functionality for the Ollama router.
package main

import (
	"encoding/json"
	"os"
	"time"
)

// Config holds all the configuration for the router.
type Config struct {
	OllamaNodes           []OllamaNodeConfig
	PollInterval          time.Duration
	ModelCacheTTL         time.Duration
	ConnectTimeout        time.Duration
	ReadTimeout           time.Duration
	ListenAddr            string
	MetricsAddr           string
	DefaultRequestTimeout time.Duration // Default timeout for requests to Ollama nodes.
}

// OllamaNodeConfig defines the configuration for a single backend node.
type OllamaNodeConfig struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseURL"`
}

// loadConfig loads configuration from environment variables.
// It reads node definitions from OLLAMA_NODES_JSON and other parameters
// from their respective environment variables, with sensible defaults.
func loadConfig() *Config {
	nodesJSON := getEnv("OLLAMA_NODES_JSON", `[{"name": "ollama1", "baseURL": "https://ollama.obeone.cloud"}, {"name": "ollama2", "baseURL": "https://ollama2.obeone.cloud"}]`)
	var nodes []OllamaNodeConfig
	if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil {
		logger.Error("Failed to parse OLLAMA_NODES_JSON", "error", err, "value", nodesJSON)
		os.Exit(1)
	}
	if len(nodes) == 0 {
		logger.Error("No Ollama nodes configured. Please set OLLAMA_NODES_JSON.")
		os.Exit(1)
	}

	return &Config{
		OllamaNodes:           nodes,
		PollInterval:          getEnvDuration("POLL_INTERVAL_SECONDS", 5*time.Second),
		ModelCacheTTL:         getEnvDuration("MODEL_CACHE_TTL_SECONDS", 120*time.Second),
		ConnectTimeout:        getEnvDuration("CONNECT_TIMEOUT_SECONDS", 5*time.Second),
		ReadTimeout:           getEnvDuration("READ_TIMEOUT_SECONDS", 600*time.Second),
		ListenAddr:            getEnv("LISTEN_ADDR", ":8080"),
		MetricsAddr:           getEnv("METRICS_ADDR", ":9090"),
		DefaultRequestTimeout: 10 * time.Second,
	}
}