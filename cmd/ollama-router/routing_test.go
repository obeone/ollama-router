package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lmittmann/tint"
)

func TestMain(m *testing.M) {
	logger = slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level: slog.LevelDebug,
	}))
	metrics = newMetrics()
	os.Exit(m.Run())
}

func TestChooseBestNode(t *testing.T) {
	tests := []struct {
		name      string
		nodes     []*NodeState
		wantName  string
		wantNil   bool
	}{
		{
			name:     "nil candidates",
			nodes:    nil,
			wantNil:  true,
		},
		{
			name: "single node",
			nodes: []*NodeState{
				{Name: "n1", LoadedModels: map[string]struct{}{"a": {}}},
			},
			wantName: "n1",
		},
		{
			name: "prefer fewer loaded models",
			nodes: []*NodeState{
				{Name: "busy", LoadedModels: map[string]struct{}{"a": {}, "b": {}, "c": {}}},
				{Name: "idle", LoadedModels: map[string]struct{}{"a": {}}},
			},
			wantName: "idle",
		},
		{
			name: "tie-break by latency",
			nodes: []*NodeState{
				{Name: "slow", LoadedModels: map[string]struct{}{"a": {}}, LatencyMs: 100},
				{Name: "fast", LoadedModels: map[string]struct{}{"a": {}}, LatencyMs: 10},
			},
			wantName: "fast",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := _chooseBestNode(tt.nodes)
			if tt.wantNil {
				if got != nil {
					t.Errorf("want nil, got %v", got)
				}
			} else {
				if got == nil || got.Name != tt.wantName {
					t.Errorf("want %s, got %v", tt.wantName, got)
				}
			}
		})
	}
}

func TestCachePutGet(t *testing.T) {
	cfg := &Config{
		ModelCacheTTL: 100 * time.Millisecond,
	}
	appState := &AppState{
		NodeStates:      map[string]*NodeState{},
		ModelOwnerCache: &sync.Map{},
		Config:          cfg,
	}

	t.Run("cache miss", func(t *testing.T) {
		_, ok := appState.cacheGet("nonexistent")
		if ok {
			t.Error("want miss")
		}
	})

	t.Run("cache hit", func(t *testing.T) {
		appState.cachePut("llama2", "node1")
		node, ok := appState.cacheGet("llama2")
		if !ok || node != "node1" {
			t.Errorf("want hit on node1, got %s (ok=%v)", node, ok)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		appState.cachePut("LLaMA2", "node2")
		node, ok := appState.cacheGet("llama2") // lowercase query
		if !ok || node != "node2" {
			t.Errorf("want case-insensitive hit, got %s", node)
		}
	})

	t.Run("cache expiry", func(t *testing.T) {
		appState.cachePut("expiring", "node3")
		time.Sleep(150 * time.Millisecond) // > TTL
		_, ok := appState.cacheGet("expiring")
		if ok {
			t.Error("want expired entry to be miss")
		}
	})
}

func TestChooseNodeForModel(t *testing.T) {
	baseURL, _ := url.Parse("http://localhost:11434")
	cfg := &Config{
		ModelCacheTTL: 1 * time.Second,
	}

	healthy := &NodeState{
		Name:         "healthy",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}
	unhealthy := &NodeState{
		Name:         "unhealthy",
		BaseURL:      baseURL,
		OK:           false,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}
	withLoaded := &NodeState{
		Name:         "loaded",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{"llama2": {}},
		LocalModels:  map[string]struct{}{},
	}
	withLocal := &NodeState{
		Name:         "local",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{"llama2": {}},
	}

	t.Run("cache hit takes priority", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"healthy": healthy,
				"loaded":  withLoaded,
			},
			ModelOwnerCache: &sync.Map{},
			Config:          cfg,
		}
		appState.cachePut("llama2", "healthy") // prefer stale cache over loaded
		node := appState.chooseNodeForModel("llama2")
		if node.Name != "healthy" {
			t.Errorf("want cache hit, got %s", node.Name)
		}
	})

	t.Run("loaded model", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"loaded": withLoaded,
			},
			ModelOwnerCache: &sync.Map{},
			Config:          cfg,
		}
		node := appState.chooseNodeForModel("llama2")
		if node.Name != "loaded" {
			t.Errorf("want loaded, got %s", node.Name)
		}
	})

	t.Run("local model", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"local": withLocal,
			},
			ModelOwnerCache: &sync.Map{},
			Config:          cfg,
		}
		node := appState.chooseNodeForModel("llama2")
		if node.Name != "local" {
			t.Errorf("want local, got %s", node.Name)
		}
	})

	t.Run("no healthy node", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"unhealthy": unhealthy,
			},
			ModelOwnerCache: &sync.Map{},
			Config:          cfg,
		}
		node := appState.chooseNodeForModel("llama2")
		if node != nil {
			t.Error("want nil when no healthy nodes")
		}
	})

	t.Run("fallback to least busy", func(t *testing.T) {
		busy := &NodeState{
			Name:         "busy",
			BaseURL:      baseURL,
			OK:           true,
			LoadedModels: map[string]struct{}{"a": {}, "b": {}},
			LocalModels:  map[string]struct{}{},
		}
		idle := &NodeState{
			Name:         "idle",
			BaseURL:      baseURL,
			OK:           true,
			LoadedModels: map[string]struct{}{},
			LocalModels:  map[string]struct{}{},
		}
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"busy": busy,
				"idle": idle,
			},
			ModelOwnerCache: &sync.Map{},
			Config:          cfg,
		}
		node := appState.chooseNodeForModel("unknown")
		if node.Name != "idle" {
			t.Errorf("want idle (least busy), got %s", node.Name)
		}
	})
}

func TestChooseNodeForPull(t *testing.T) {
	baseURL, _ := url.Parse("http://localhost:11434")
	cfg := &Config{}

	hasModel := &NodeState{
		Name:         "has",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{"llama2": {}},
	}
	noModel := &NodeState{
		Name:         "empty",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}

	t.Run("pull with model hint prefers node that has it", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"has":   hasModel,
				"empty": noModel,
			},
			Config: cfg,
		}
		node := appState.chooseNodeForPull("llama2")
		if node.Name != "has" {
			t.Errorf("want node with model, got %s", node.Name)
		}
	})

	t.Run("pull without model falls back to least busy", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"has":   hasModel,
				"empty": noModel,
			},
			Config: cfg,
		}
		node := appState.chooseNodeForPull("")
		if node == nil {
			t.Error("want a node, got nil")
		}
	})
}

func TestNodesWithModelLocal(t *testing.T) {
	baseURL, _ := url.Parse("http://localhost:11434")

	healthyWithModel := &NodeState{
		Name:         "has",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{"llama2:latest": {}},
	}
	healthyWithoutModel := &NodeState{
		Name:         "empty",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}
	unhealthyWithModel := &NodeState{
		Name:         "sick",
		BaseURL:      baseURL,
		OK:           false,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{"llama2:latest": {}},
	}
	secondHealthyWithModel := &NodeState{
		Name:         "has2",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{"llama2:latest": {}},
	}

	t.Run("empty model returns nil", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{"has": healthyWithModel},
			Config:     &Config{},
		}
		got := appState.nodesWithModelLocal("")
		if got != nil {
			t.Errorf("want nil for empty model, got %v", got)
		}
	})

	t.Run("unhealthy node not returned", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{"sick": unhealthyWithModel},
			Config:     &Config{},
		}
		got := appState.nodesWithModelLocal("llama2:latest")
		if len(got) != 0 {
			t.Errorf("want 0 nodes (unhealthy), got %d", len(got))
		}
	})

	t.Run("model on one healthy node", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"has":   healthyWithModel,
				"empty": healthyWithoutModel,
			},
			Config: &Config{},
		}
		got := appState.nodesWithModelLocal("llama2:latest")
		if len(got) != 1 {
			t.Errorf("want 1 node, got %d", len(got))
		}
		if got[0].Name != "has" {
			t.Errorf("want node 'has', got %s", got[0].Name)
		}
	})

	t.Run("model on multiple healthy nodes", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"has":  healthyWithModel,
				"has2": secondHealthyWithModel,
			},
			Config: &Config{},
		}
		got := appState.nodesWithModelLocal("llama2:latest")
		if len(got) != 2 {
			t.Errorf("want 2 nodes, got %d", len(got))
		}
	})

	t.Run("lowercase normalization matches mixed-case query", func(t *testing.T) {
		// LocalModels key is lowercase (as stored by refreshNodeState)
		nodeLC := &NodeState{
			Name:         "lc",
			BaseURL:      baseURL,
			OK:           true,
			LoadedModels: map[string]struct{}{},
			LocalModels:  map[string]struct{}{"llama2:latest": {}}, // stored lowercase
		}
		appState := &AppState{
			NodeStates: map[string]*NodeState{"lc": nodeLC},
			Config:     &Config{},
		}
		// Query with mixed case — must still match
		got := appState.nodesWithModelLocal("LLaMa2:Latest")
		if len(got) != 1 {
			t.Errorf("want 1 node with mixed-case query, got %d", len(got))
		}
	})
}

// makeBackend starts a minimal httptest.Server that records hit count and returns status 200.
func makeBackend(t *testing.T, hits *int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))
}

// makeNode builds a NodeState wired to a live httptest.Server URL, with
// its ReverseProxy initialised so proxyRequest does not panic.
func makeNode(name string, srv *httptest.Server, localModels map[string]struct{}) *NodeState {
	u, _ := url.Parse(srv.URL)
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = u.Scheme
			req.URL.Host = u.Host
		},
	}
	return &NodeState{
		Name:         name,
		BaseURL:      u,
		Proxy:        proxy,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  localModels,
	}
}

func TestHandleCreateRouting(t *testing.T) {
	cfg := &Config{
		ModelCacheTTL:        1 * time.Second,
		ReadTimeout:          5 * time.Second,
		DefaultRequestTimeout: 5 * time.Second,
	}

	buildAppState := func(nodes map[string]*NodeState) *AppState {
		return &AppState{
			NodeStates:      nodes,
			ModelOwnerCache: &sync.Map{},
			Config:          cfg,
			Client:          &http.Client{Timeout: 5 * time.Second},
		}
	}

	body := func(model string) io.Reader {
		b, _ := json.Marshal(map[string]string{"model": model, "modelfile": "FROM scratch"})
		return bytes.NewReader(b)
	}

	t.Run("0 owners routes to stable node only", func(t *testing.T) {
		var hitsA, hitsB int
		var mu sync.Mutex

		srvA := makeBackend(t, &hitsA, &mu)
		defer srvA.Close()
		srvB := makeBackend(t, &hitsB, &mu)
		defer srvB.Close()

		// "aaa" sorts before "bbb" → stable node is nodeA
		nodeA := makeNode("aaa", srvA, map[string]struct{}{}) // model NOT present
		nodeB := makeNode("bbb", srvB, map[string]struct{}{}) // model NOT present

		appState := buildAppState(map[string]*NodeState{"aaa": nodeA, "bbb": nodeB})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/create", body("newmodel"))
		req.Header.Set("Content-Type", "application/json")
		appState.handleCreate(rr, req)

		mu.Lock()
		gotA, gotB := hitsA, hitsB
		mu.Unlock()

		if gotA != 1 || gotB != 0 {
			t.Errorf("want hitsA=1 hitsB=0, got hitsA=%d hitsB=%d", gotA, gotB)
		}
	})

	t.Run("1 owner routes to that node only", func(t *testing.T) {
		var hitsA, hitsB int
		var mu sync.Mutex

		srvA := makeBackend(t, &hitsA, &mu)
		defer srvA.Close()
		srvB := makeBackend(t, &hitsB, &mu)
		defer srvB.Close()

		// nodeB has the model locally; nodeA (stable) does not
		nodeA := makeNode("aaa", srvA, map[string]struct{}{})
		nodeB := makeNode("bbb", srvB, map[string]struct{}{"mymodel": {}})

		appState := buildAppState(map[string]*NodeState{"aaa": nodeA, "bbb": nodeB})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/create", body("mymodel"))
		req.Header.Set("Content-Type", "application/json")
		appState.handleCreate(rr, req)

		mu.Lock()
		gotA, gotB := hitsA, hitsB
		mu.Unlock()

		// Must hit nodeB (the owner), NOT nodeA (stable)
		if gotA != 0 || gotB != 1 {
			t.Errorf("want hitsA=0 hitsB=1, got hitsA=%d hitsB=%d", gotA, gotB)
		}
	})

	t.Run("2 owners broadcasts to both nodes", func(t *testing.T) {
		var hitsA, hitsB int
		var mu sync.Mutex

		srvA := makeBackend(t, &hitsA, &mu)
		defer srvA.Close()
		srvB := makeBackend(t, &hitsB, &mu)
		defer srvB.Close()

		// Both nodes have the model locally
		nodeA := makeNode("aaa", srvA, map[string]struct{}{"sharedmodel": {}})
		nodeB := makeNode("bbb", srvB, map[string]struct{}{"sharedmodel": {}})

		appState := buildAppState(map[string]*NodeState{"aaa": nodeA, "bbb": nodeB})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/create", body("sharedmodel"))
		req.Header.Set("Content-Type", "application/json")
		appState.handleCreate(rr, req)

		mu.Lock()
		gotA, gotB := hitsA, hitsB
		mu.Unlock()

		// Both must be hit (broadcast)
		if gotA != 1 || gotB != 1 {
			t.Errorf("want hitsA=1 hitsB=1, got hitsA=%d hitsB=%d", gotA, gotB)
		}
	})
}
