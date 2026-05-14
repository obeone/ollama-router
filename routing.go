// Package main provides routing logic for directing Ollama API requests to appropriate backend nodes.
package main

import (
	"sort"
	"strings"
	"time"
)

// cachePut stores a model-to-node mapping in the cache.
// The model name is converted to lowercase for consistent caching.
func (appState *AppState) cachePut(model, nodeName string) {
	lowerModel := strings.ToLower(model)
	appState.ModelOwnerCache.Store(lowerModel, modelCacheEntry{
		NodeName:  nodeName,
		Timestamp: time.Now(),
	})
	logger.Debug("Cache PUT", "model", model, "node", nodeName)
}

// cacheGet retrieves a model-to-node mapping from the cache if it's valid (not expired).
// If the entry is expired, it is eagerly deleted from the cache.
func (appState *AppState) cacheGet(model string) (string, bool) {
	lowerModel := strings.ToLower(model)
	entry, ok := appState.ModelOwnerCache.Load(lowerModel)
	if !ok {
		logger.Debug("Cache MISS", "model", model)
		return "", false
	}
	cached := entry.(modelCacheEntry)
	if time.Since(cached.Timestamp) > appState.Config.ModelCacheTTL {
		logger.Debug("Cache EXPIRED", "model", model, "owner", cached.NodeName)
		appState.ModelOwnerCache.Delete(lowerModel) // Eagerly delete
		return "", false
	}
	logger.Debug("Cache HIT", "model", model, "node", cached.NodeName)
	return cached.NodeName, true
}

// _chooseBestNode selects the best node from a list of candidates.
// The primary sorting criteria is the number of loaded models (fewer is better),
// followed by lower latency as a secondary tie-breaker.
func _chooseBestNode(candidates []*NodeState) *NodeState {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		candidates[i].mu.RLock()
		candidates[j].mu.RLock()
		defer candidates[i].mu.RUnlock()
		defer candidates[j].mu.RUnlock()
		// Primary sort: fewer loaded models is better
		if len(candidates[i].LoadedModels) != len(candidates[j].LoadedModels) {
			return len(candidates[i].LoadedModels) < len(candidates[j].LoadedModels)
		}
		// Secondary sort: lower latency is better
		return candidates[i].LatencyMs < candidates[j].LatencyMs
	})
	return candidates[0]
}

// leastBusyHealthyNode finds the best-performing healthy node.
// It considers all currently healthy nodes and applies the best node selection logic.
func (appState *AppState) leastBusyHealthyNode() *NodeState {
	healthy := []*NodeState{}
	for _, ns := range appState.NodeStates {
		ns.mu.RLock()
		if ns.OK {
			healthy = append(healthy, ns)
		}
		ns.mu.RUnlock()
	}
	return _chooseBestNode(healthy)
}

// chooseNodeForModel implements the main routing logic for a given model.
// It prioritizes nodes with the model already loaded, then nodes with the model on disk,
// and finally falls back to the least busy healthy node. It also utilizes a cache for efficiency.
func (appState *AppState) chooseNodeForModel(model string) *NodeState {
	modelLower := strings.ToLower(model)

	// 1. Check cache for an active session
	if nodeName, ok := appState.cacheGet(modelLower); ok {
		if ns, exists := appState.NodeStates[nodeName]; exists {
			ns.mu.RLock()
			isOK := ns.OK
			ns.mu.RUnlock()
			if isOK {
				metrics.routingDecisions.WithLabelValues("HIT_CACHE", model).Inc()
				logger.Info("Decision: HIT_CACHE", "model", model, "node", nodeName)
				return ns
			}
		}
	}

	allNodes := make([]*NodeState, 0, len(appState.NodeStates))
	for _, ns := range appState.NodeStates {
		allNodes = append(allNodes, ns)
	}

	// 2. Find a node where the model is already loaded in VRAM
	for _, ns := range allNodes {
		ns.mu.RLock()
		_, isLoaded := ns.LoadedModels[modelLower]
		isOK := ns.OK
		ns.mu.RUnlock()
		if isOK && isLoaded {
			metrics.routingDecisions.WithLabelValues("HIT_LOADED", model).Inc()
			logger.Info("Decision: HIT_LOADED", "model", model, "node", ns.Name)
			appState.cachePut(model, ns.Name)
			return ns
		}
	}

	// 3. Find a node where the model is present on disk (faster warm-up)
	var localCandidates []*NodeState
	for _, ns := range allNodes {
		ns.mu.RLock()
		_, isLocal := ns.LocalModels[modelLower]
		isOK := ns.OK
		ns.mu.RUnlock()
		if isOK && isLocal {
			localCandidates = append(localCandidates, ns)
		}
	}
	if len(localCandidates) > 0 {
		chosen := _chooseBestNode(localCandidates)
		metrics.routingDecisions.WithLabelValues("HIT_LOCAL", model).Inc()
		logger.Info("Decision: HIT_LOCAL", "model", model, "node", chosen.Name)
		appState.cachePut(model, chosen.Name)
		return chosen
	}

	// 4. Fallback to the overall least busy healthy node
	chosen := appState.leastBusyHealthyNode()
	if chosen == nil {
		metrics.routingDecisions.WithLabelValues("NO_HEALTHY_NODE", model).Inc()
		logger.Error("Decision: NO_HEALTHY_NODE")
		return nil
	}
	metrics.routingDecisions.WithLabelValues("FALLBACK_LEAST_BUSY", model).Inc()
	logger.Warn("Decision: FALLBACK_LEAST_BUSY", "model", model, "node", chosen.Name)
	appState.cachePut(model, chosen.Name)
	return chosen
}

// chooseNodeForPull finds the best node for a pull request.
// If a specific model is requested, it prioritizes nodes that already have it locally.
// Otherwise, it defaults to the least busy healthy node.
func (appState *AppState) chooseNodeForPull(model string) *NodeState {
	if model != "" {
		var candidates []*NodeState
		for _, ns := range appState.NodeStates {
			ns.mu.RLock()
			_, hasLocal := ns.LocalModels[strings.ToLower(model)]
			isOK := ns.OK
			ns.mu.RUnlock()
			if isOK && hasLocal {
				candidates = append(candidates, ns)
			}
		}
		if len(candidates) > 0 {
			return _chooseBestNode(candidates)
		}
	}
	return appState.leastBusyHealthyNode()
}

// chooseNodeWithModel finds the best node that has a model locally.
// If multiple nodes have the model, the best node is selected based on load and latency.
func (appState *AppState) chooseNodeWithModel(model string) *NodeState {
	var candidates []*NodeState
	if model != "" {
		for _, ns := range appState.NodeStates {
			ns.mu.RLock()
			_, hasLocal := ns.LocalModels[strings.ToLower(model)]
			isOK := ns.OK
			ns.mu.RUnlock()
			if isOK && hasLocal {
				candidates = append(candidates, ns)
			}
		}
	}
	if len(candidates) > 0 {
		return _chooseBestNode(candidates)
	}
	return appState.leastBusyHealthyNode() // Fallback
}

// chooseNodeWithModelOrLoaded finds the best node that has a model locally or loaded into VRAM.
// It prioritizes nodes where the model is ready for immediate use.
func (appState *AppState) chooseNodeWithModelOrLoaded(model string) *NodeState {
	var candidates []*NodeState
	if model != "" {
		for _, ns := range appState.NodeStates {
			ns.mu.RLock()
			_, hasLocal := ns.LocalModels[strings.ToLower(model)]
			_, hasLoaded := ns.LoadedModels[strings.ToLower(model)]
			isOK := ns.OK
			ns.mu.RUnlock()
			if isOK && (hasLocal || hasLoaded) {
				candidates = append(candidates, ns)
			}
		}
	}
	if len(candidates) > 0 {
		return _chooseBestNode(candidates)
	}
	return appState.leastBusyHealthyNode()
}