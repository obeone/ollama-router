// Package main manages the application's state, including the health and loaded models of backend Ollama nodes.
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// NodeState holds the dynamic state of a single Ollama backend node.
// It includes information about the node's URL, its proxy, currently loaded models,
// locally available models, health status, and response latency.
type NodeState struct {
	Name         string
	BaseURL      *url.URL
	Proxy        *httputil.ReverseProxy
	LoadedModels map[string]struct{} // Models currently loaded in VRAM
	LocalModels  map[string]struct{} // Models available on disk
	OK           bool                // Health status of the node
	LatencyMs    int64               // Latency of the last health check in milliseconds
	mu           sync.RWMutex        // Mutex to protect access to node state fields
}

// AppState holds the global state for the application.
// It manages the state of all Ollama nodes, a cache for model ownership,
// a shared HTTP client, and the application's configuration.
type AppState struct {
	NodeStates      map[string]*NodeState
	ModelOwnerCache *sync.Map // concurrent map: string (model name) -> modelCacheEntry
	Client          *http.Client
	Config          *Config
}

// modelCacheEntry represents an entry in the model owner cache.
// It stores the name of the node owning a model and the timestamp of the last update.
type modelCacheEntry struct {
	NodeName  string
	Timestamp time.Time
}

// newAppState initializes the global application state, including the shared HTTP client
// and individual state for each configured backend node.
func newAppState(cfg *Config) *AppState {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	appState := &AppState{
		NodeStates:      make(map[string]*NodeState),
		ModelOwnerCache: &sync.Map{},
		Config:          cfg,
		Client: &http.Client{
			Timeout:   cfg.DefaultRequestTimeout,
			Transport: transport,
		},
	}

	for _, nodeCfg := range cfg.OllamaNodes {
		baseURL, err := url.Parse(nodeCfg.BaseURL)
		if err != nil {
			logger.Error("Invalid base URL for node", "node", nodeCfg.Name, "url", nodeCfg.BaseURL, "error", err)
			continue
		}
		proxy := httputil.NewSingleHostReverseProxy(baseURL)
		proxy.Transport = transport

		appState.NodeStates[nodeCfg.Name] = &NodeState{
			Name:         nodeCfg.Name,
			BaseURL:      baseURL,
			Proxy:        proxy,
			LoadedModels: make(map[string]struct{}),
			LocalModels:  make(map[string]struct{}),
			OK:           false,
		}
	}
	return appState
}

// backgroundRefresher runs a loop to periodically refresh the state of all nodes.
// It stops when the provided context is cancelled and signals the waiting group upon completion.
func (appState *AppState) backgroundRefresher(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	logger.Info("Starting background refresher", "interval", appState.Config.PollInterval)
	ticker := time.NewTicker(appState.Config.PollInterval)
	defer ticker.Stop()

	// Initial refresh on startup
	appState.refreshAllNodes(ctx)

	for {
		select {
		case <-ticker.C:
			appState.refreshAllNodes(ctx)
			appState.pruneModelCache()
		case <-ctx.Done():
			logger.Info("Stopping background refresher")
			return
		}
	}
}

// refreshAllNodes refreshes the state of all configured nodes concurrently.
// It uses a wait group to ensure all node refreshes are completed before returning.
func (appState *AppState) refreshAllNodes(ctx context.Context) {
	var refreshWg sync.WaitGroup
	for _, ns := range appState.NodeStates {
		refreshWg.Add(1)
		go func(state *NodeState) {
			defer refreshWg.Done()
			appState.refreshNodeState(ctx, state)
		}(ns)
	}
	refreshWg.Wait()
}

// refreshNodeState fetches the latest state (/api/ps, /api/tags) for a single node.
// It updates the node's health status, loaded models, local models, and latency metrics.
func (appState *AppState) refreshNodeState(ctx context.Context, state *NodeState) {
	logger.Debug("Refreshing node state", "node", state.Name)
	start := time.Now()

	// Concurrently fetch ps and tags
	var psResp *OllamaPSResponse
	var tagsResp *OllamaTagsResponse
	var fetchWg sync.WaitGroup
	var psErr, tagsErr error

	fetchWg.Add(2)
	go func() {
		defer fetchWg.Done()
		psResp, psErr = fetchAPI[OllamaPSResponse](ctx, appState.Client, http.MethodPost, state.BaseURL.String()+"/api/ps", nil)
	}()
	go func() {
		defer fetchWg.Done()
		tagsResp, tagsErr = fetchAPI[OllamaTagsResponse](ctx, appState.Client, http.MethodGet, state.BaseURL.String()+"/api/tags", nil)
	}()
	fetchWg.Wait()

	latency := time.Since(start).Milliseconds()

	state.mu.Lock()
	defer state.mu.Unlock()

	state.LatencyMs = latency
	if psErr != nil && tagsErr != nil {
		if state.OK {
			logger.Warn("Marking node as unhealthy", "node", state.Name, "ps_error", psErr, "tags_error", tagsErr)
		}
		state.OK = false
		metrics.nodeHealth.WithLabelValues(state.Name).Set(0)
		return
	}
	state.OK = true
	metrics.nodeHealth.WithLabelValues(state.Name).Set(1)
	metrics.nodeLatency.WithLabelValues(state.Name).Set(float64(latency))

	// Update loaded models from /api/ps
	newLoadedModels := make(map[string]struct{})
	if psResp != nil {
		for _, m := range psResp.Models {
			lowerName := strings.ToLower(m.Name)
			newLoadedModels[lowerName] = struct{}{}
			if base, _, found := strings.Cut(lowerName, ":"); found {
				newLoadedModels[base] = struct{}{}
			}
		}
	}
	state.LoadedModels = newLoadedModels
	metrics.nodeLoadedModels.WithLabelValues(state.Name).Set(float64(len(newLoadedModels)))

	// Update local models from /api/tags
	newLocalModels := make(map[string]struct{})
	if tagsResp != nil {
		for _, m := range tagsResp.Models {
			lowerName := strings.ToLower(m.Name)
			newLocalModels[lowerName] = struct{}{}
			if base, _, found := strings.Cut(lowerName, ":"); found {
				newLocalModels[base] = struct{}{}
			}
		}
	}
	state.LocalModels = newLocalModels
}

// pruneModelCache removes expired entries from the model owner cache.
// This helps keep the cache fresh and prevents stale routing decisions.
func (appState *AppState) pruneModelCache() {
	now := time.Now()
	expiredKeys := []string{}
	appState.ModelOwnerCache.Range(func(key, value any) bool {
		entry := value.(modelCacheEntry)
		if now.Sub(entry.Timestamp) > appState.Config.ModelCacheTTL {
			expiredKeys = append(expiredKeys, key.(string))
		}
		return true
	})

	if len(expiredKeys) > 0 {
		for _, key := range expiredKeys {
			appState.ModelOwnerCache.Delete(key)
		}
		logger.Debug("Pruned expired model cache entries", "count", len(expiredKeys))
	}
}