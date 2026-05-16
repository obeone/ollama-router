package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/go-chi/chi/v5"
)

// setupRoutes configures all the HTTP routes for the application.
func setupRoutes(r *chi.Mux, appState *AppState) {
	// Root endpoint for compatibility with `ollama` CLI
	r.Get("/", handleRoot)
	r.Head("/", handleRoot)

	// Health and state
	r.Get("/healthz", appState.handleHealthz)

	// Aggregated endpoints
	r.Get("/api/tags", appState.handleAggregateTags)
	r.Post("/api/ps", appState.handleAggregatePS)
	r.Get("/api/version", appState.handleVersion)

	// Model-aware routing for native Ollama API
	r.Post("/api/generate", appState.handleModelAwareProxy)
	r.Post("/api/chat", appState.handleModelAwareProxy)
	r.Post("/api/embeddings", appState.handleModelAwareProxy)
	r.Post("/api/embed", appState.handleModelAwareProxy)

	// Model-aware routing for OpenAI-compatible API
	r.Post("/v1/chat/completions", appState.handleModelAwareProxy)
	r.Post("/v1/completions", appState.handleModelAwareProxy)
	r.Post("/v1/embeddings", appState.handleModelAwareProxy)
	r.Get("/v1/models", appState.handleOpenAIModels)
	r.Get("/v1/models/*", appState.handleOpenAIModel)

	// Specific routing logic
	r.Post("/api/pull", appState.handlePull)
	r.Post("/api/push", appState.handlePush)
	r.Post("/api/copy", appState.handleCopy)
	r.Post("/api/create", appState.handleCreate)
	r.Post("/api/show", appState.handleShow)

	// Broadcast
	r.Post("/api/delete", appState.handleDelete)

	// Catch-all for other /api/ endpoints
	r.Handle("/api/*", http.HandlerFunc(appState.handleCatchAll))
}

// handleRoot responds to GET and HEAD requests on the root path.
func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// For GET requests, Ollama returns this specific string.
	// For HEAD requests, the body is automatically omitted.
	fmt.Fprintln(w, "Ollama is running")
}

// handleHealthz provides a detailed health check of the router and backend nodes.
func (appState *AppState) handleHealthz(w http.ResponseWriter, r *http.Request) {
	nodesState := make(map[string]any)
	for name, ns := range appState.NodeStates {
		ns.mu.RLock()
		nodesState[name] = map[string]any{
			"ok":                ns.OK,
			"baseURL":           ns.BaseURL.String(),
			"loadedModelsCount": len(ns.LoadedModels),
			"localModelsCount":  len(ns.LocalModels),
			"latencyMs":         ns.LatencyMs,
		}
		ns.mu.RUnlock()
	}

	cacheSize := 0
	appState.ModelOwnerCache.Range(func(_, _ any) bool {
		cacheSize++
		return true
	})

	healthData := map[string]any{
		"nodes":      nodesState,
		"cache_size": cacheSize,
		"config": map[string]any{
			"poll_interval":   appState.Config.PollInterval.String(),
			"model_cache_ttl": appState.Config.ModelCacheTTL.String(),
		},
	}
	respondJSON(w, http.StatusOK, healthData)
}

// aggregateTags is an internal function to collect and union model tags from all healthy nodes.
func (appState *AppState) aggregateTags(ctx context.Context) (*OllamaTagsResponse, error) {
	seenModels := make(map[string]OllamaTagModel)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var nodesErr error

	for _, node := range appState.NodeStates {
		node.mu.RLock()
		isOK := node.OK
		node.mu.RUnlock()
		if !isOK {
			continue
		}

		wg.Add(1)
		go func(ns *NodeState) {
			defer wg.Done()
			tags, err := fetchAPI[OllamaTagsResponse](ctx, appState.Client, http.MethodGet, ns.BaseURL.String()+"/api/tags", nil)
			if err != nil {
				nodesErr = err // Store the last error
				return
			}
			if tags == nil {
				return
			}

			mu.Lock()
			defer mu.Unlock()
			for _, model := range tags.Models {
				// Only add if not seen before, to deduplicate.
				if _, ok := seenModels[model.Name]; !ok {
					seenModels[model.Name] = model
				}
			}
		}(node)
	}
	wg.Wait()

	if len(seenModels) == 0 && nodesErr != nil {
		return nil, fmt.Errorf("failed to fetch tags from any node: %w", nodesErr)
	}

	resultSlice := make([]OllamaTagModel, 0, len(seenModels))
	for _, model := range seenModels {
		resultSlice = append(resultSlice, model)
	}
	sort.Slice(resultSlice, func(i, j int) bool {
		return resultSlice[i].Name < resultSlice[j].Name
	})

	return &OllamaTagsResponse{Models: resultSlice}, nil
}

// handleAggregateTags is the HTTP handler for the /api/tags endpoint.
func (appState *AppState) handleAggregateTags(w http.ResponseWriter, r *http.Request) {
	tags, err := appState.aggregateTags(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to aggregate tags from nodes")
		return
	}
	respondJSON(w, http.StatusOK, tags)
}

// handleOpenAIModels is the HTTP handler for the /v1/models endpoint.
// It transforms the aggregated tags into the OpenAI API format.
func (appState *AppState) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	tagsResponse, err := appState.aggregateTags(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to aggregate models from nodes")
		return
	}

	openAIModels := make([]OpenAIModel, len(tagsResponse.Models))
	for i, ollamaModel := range tagsResponse.Models {
		openAIModels[i] = OpenAIModel{
			ID:      ollamaModel.Name,
			Object:  "model",
			Created: ollamaModel.ModifiedAt.Unix(),
			OwnedBy: "ollama",
		}
	}

	response := OpenAIModelsResponse{
		Object: "list",
		Data:   openAIModels,
	}

	respondJSON(w, http.StatusOK, response)
}

// handleAggregatePS collects and lists all running models from all nodes.
func (appState *AppState) handleAggregatePS(w http.ResponseWriter, r *http.Request) {
	var allModels []OllamaPSModel
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, node := range appState.NodeStates {
		wg.Add(1)
		go func(ns *NodeState) {
			defer wg.Done()
			ps, err := fetchAPI[OllamaPSResponse](r.Context(), appState.Client, http.MethodPost, ns.BaseURL.String()+"/api/ps", nil)
			if err != nil || ps == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			allModels = append(allModels, ps.Models...)
		}(node)
	}
	wg.Wait()

	sort.Slice(allModels, func(i, j int) bool {
		return allModels[i].Name < allModels[j].Name
	})

	respondJSON(w, http.StatusOK, OllamaPSResponse{Models: allModels})
}

// handleVersion returns the version of the least busy backend node.
func (appState *AppState) handleVersion(w http.ResponseWriter, r *http.Request) {
	node := appState.leastBusyHealthyNode()
	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no healthy backend available")
		return
	}
	version, err := fetchAPI[OllamaVersionResponse](r.Context(), appState.Client, http.MethodGet, node.BaseURL.String()+"/api/version", nil)
	if err != nil {
		respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to fetch version from node %s", node.Name))
		return
	}
	respondJSON(w, http.StatusOK, version)
}

// handleModelAwareProxy is a generic handler for endpoints that require model-aware routing.
func (appState *AppState) handleModelAwareProxy(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body

	model, err := extractModelFromBody(bodyBytes)
	if err != nil || model == "" {
		respondError(w, http.StatusBadRequest, "missing or invalid 'model' in request body")
		return
	}

	node := appState.chooseNodeForModel(model)
	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no healthy backend available to handle the model")
		return
	}

	appState.proxyRequest(w, r, node)
}

// handlePull routes a pull request to the most appropriate node.
func (appState *AppState) handlePull(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	model, _ := extractModelFromBody(bodyBytes)
	node := appState.chooseNodeForPull(model)

	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no healthy backend available for pull")
		return
	}
	logger.Info("Routing pull request", "model", model, "node", node.Name)
	appState.proxyRequest(w, r, node)
}

// handlePush routes a push request from a node that has the model locally.
func (appState *AppState) handlePush(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	model, err := extractModelFromBody(bodyBytes)
	if err != nil || model == "" {
		respondError(w, http.StatusBadRequest, "missing 'name' in request body for push")
		return
	}

	node := appState.chooseNodeWithModel(model)
	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no node has the model locally to push")
		return
	}
	logger.Info("Routing push request", "model", model, "node", node.Name)
	appState.proxyRequest(w, r, node)
}

// handleCopy routes a copy request to a node that has the source model.
func (appState *AppState) handleCopy(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	sourceModel, err := extractModelFromBody(bodyBytes) // Checks 'source' field
	if err != nil || sourceModel == "" {
		respondError(w, http.StatusBadRequest, "missing 'source' in request body for copy")
		return
	}

	node := appState.chooseNodeWithModel(sourceModel)
	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no node has the source model locally to copy")
		return
	}
	logger.Info("Routing copy request", "source_model", sourceModel, "node", node.Name)
	appState.proxyRequest(w, r, node)
}

// handleCreate routes a create request to the least busy healthy node.
func (appState *AppState) handleCreate(w http.ResponseWriter, r *http.Request) {
	node := appState.leastBusyHealthyNode()
	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no healthy backend available for create")
		return
	}
	logger.Info("Routing create request", "node", node.Name)
	appState.proxyRequest(w, r, node)
}

// handleShow routes a show request to a node that has the model.
func (appState *AppState) handleShow(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	model, _ := extractModelFromBody(bodyBytes)
	node := appState.chooseNodeWithModelOrLoaded(model)

	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no healthy backend available for show")
		return
	}
	logger.Info("Routing show request", "model", model, "node", node.Name)
	appState.proxyRequest(w, r, node)
}

// handleDelete broadcasts the delete request to all healthy nodes.
func (appState *AppState) handleDelete(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)

	var healthyNodes []*NodeState
	for _, ns := range appState.NodeStates {
		ns.mu.RLock()
		if ns.OK {
			healthyNodes = append(healthyNodes, ns)
		}
		ns.mu.RUnlock()
	}
	if len(healthyNodes) == 0 {
		respondError(w, http.StatusServiceUnavailable, "no healthy backends to broadcast delete to")
		return
	}

	type result struct {
		resp *http.Response
		err  error
	}

	// Use a channel to get the first successful response
	resultChan := make(chan result, len(healthyNodes))
	var wg sync.WaitGroup

	for _, node := range healthyNodes {
		wg.Add(1)
		go func(ns *NodeState) {
			defer wg.Done()
			reqURL := ns.BaseURL.JoinPath("/api/delete")
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, reqURL.String(), bytes.NewReader(bodyBytes))
			if err != nil {
				resultChan <- result{err: err}
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := appState.Client.Do(req)
			resultChan <- result{resp: resp, err: err}
		}(node)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var lastError error
	var lastBadResponse *http.Response
	for res := range resultChan {
		if res.err != nil {
			lastError = res.err
			continue
		}
		if res.resp.StatusCode < 400 {
			// Success! Forward the response and we are done.
			forwardResponse(w, res.resp)
			return
		}
		// It was an error response, store it in case all fail
		if lastBadResponse != nil {
			lastBadResponse.Body.Close()
		}
		lastBadResponse = res.resp
	}

	// If we get here, no request was successful
	if lastBadResponse != nil {
		forwardResponse(w, lastBadResponse)
		return
	}
	respondError(w, http.StatusBadGateway, fmt.Sprintf("delete failed on all nodes: %v", lastError))
}

// handleOpenAIModel returns a single OpenAI-compatible model object by id, or a 404 error if the model is not found.
func (appState *AppState) handleOpenAIModel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "*")

	tagsResponse, err := appState.aggregateTags(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to aggregate models from nodes")
		return
	}

	for _, ollamaModel := range tagsResponse.Models {
		if ollamaModel.Name == id {
			respondJSON(w, http.StatusOK, OpenAIModel{
				ID:      ollamaModel.Name,
				Object:  "model",
				Created: ollamaModel.ModifiedAt.Unix(),
				OwnedBy: "ollama",
			})
			return
		}
	}

	respondJSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]string{
			"message": fmt.Sprintf("model '%s' not found", id),
			"type":    "invalid_request_error",
			"code":    "model_not_found",
		},
	})
}

// handleCatchAll proxies any other unhandled /api/ requests.
func (appState *AppState) handleCatchAll(w http.ResponseWriter, r *http.Request) {
	var node *NodeState

	// For POST requests, try to be model-aware
	if r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil {
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if model, _ := extractModelFromBody(bodyBytes); model != "" {
				node = appState.chooseNodeForModel(model)
			}
		}
	}

	// Fallback for GET or if no model found
	if node == nil {
		node = appState.leastBusyHealthyNode()
	}

	if node == nil {
		respondError(w, http.StatusServiceUnavailable, "no healthy backend available for request")
		return
	}
	logger.Debug("Catch-all proxying", "path", r.URL.Path, "node", node.Name)
	appState.proxyRequest(w, r, node)
}

