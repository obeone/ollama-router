package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fakePSServer starts a test HTTP server that records requests and serves GET /api/ps.
func fakePSServer(t *testing.T, models []OllamaPSModel) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ps" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaPSResponse{Models: models})
			return
		}
		// Return empty models list for wrong-method calls (simulates real ollama behavior).
		if r.URL.Path == "/api/ps" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaPSResponse{Models: nil})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildAppStateForPS builds a minimal AppState backed by the given healthy fake nodes.
// Each NodeState has a Proxy initialized so proxyRequest does not nil-deref when the
// catch-all handler is exercised.
func buildAppStateForPS(t *testing.T, nodes []*httptest.Server) *AppState {
	t.Helper()
	nodeStates := make(map[string]*NodeState, len(nodes))
	for i, srv := range nodes {
		baseURL, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("failed to parse fake server URL: %v", err)
		}
		name := string(rune('A' + i))
		nodeStates[name] = &NodeState{
			Name:         name,
			BaseURL:      baseURL,
			Proxy:        httputil.NewSingleHostReverseProxy(baseURL),
			OK:           true,
			LoadedModels: map[string]struct{}{},
			LocalModels:  map[string]struct{}{},
		}
	}
	return &AppState{
		NodeStates:      nodeStates,
		ModelOwnerCache: &sync.Map{},
		Client:          &http.Client{Timeout: 5 * time.Second},
		Config:          &Config{ModelCacheTTL: time.Minute, ReadTimeout: 30 * time.Second},
	}
}

// TestAggregatePS_GET verifies that GET /api/ps aggregates running models from all nodes.
func TestAggregatePS_GET(t *testing.T) {
	srv1 := fakePSServer(t, []OllamaPSModel{{Name: "llama3:8b", Size: 100}})
	srv2 := fakePSServer(t, []OllamaPSModel{{Name: "mistral:7b", Size: 200}})

	appState := buildAppStateForPS(t, []*httptest.Server{srv1, srv2})

	r := chi.NewRouter()
	setupRoutes(r, appState)

	req := httptest.NewRequest(http.MethodGet, "/api/ps", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var got OllamaPSResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Both models from both nodes must be present (aggregation).
	found := make(map[string]bool)
	for _, m := range got.Models {
		found[m.Name] = true
	}
	if !found["llama3:8b"] {
		t.Errorf("want llama3:8b in aggregated response, got %v", got.Models)
	}
	if !found["mistral:7b"] {
		t.Errorf("want mistral:7b in aggregated response, got %v", got.Models)
	}
}

// TestAggregatePS_WrongMethod verifies that POST /api/ps does NOT return the full aggregated
// 2-model body — it falls through to the catch-all (single-node proxy), not the aggregator.
func TestAggregatePS_WrongMethod(t *testing.T) {
	srv1 := fakePSServer(t, []OllamaPSModel{{Name: "llama3:8b", Size: 100}})
	srv2 := fakePSServer(t, []OllamaPSModel{{Name: "mistral:7b", Size: 200}})

	appState := buildAppStateForPS(t, []*httptest.Server{srv1, srv2})

	r := chi.NewRouter()
	setupRoutes(r, appState)

	req := httptest.NewRequest(http.MethodPost, "/api/ps", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// The catch-all proxies to a single node, so we cannot get both models.
	// We accept any response here — the key assertion is it does NOT have both models.
	var got OllamaPSResponse
	_ = json.NewDecoder(w.Body).Decode(&got)

	found := make(map[string]bool)
	for _, m := range got.Models {
		found[m.Name] = true
	}
	if found["llama3:8b"] && found["mistral:7b"] {
		t.Errorf("POST /api/ps must NOT return aggregated 2-model body; got both models, proving the aggregator was incorrectly triggered")
	}
}

// deleteRecorder is a thread-safe recorder for inbound DELETE requests to a fake backend.
type deleteRecorder struct {
	mu      sync.Mutex
	methods []string
}

func (dr *deleteRecorder) record(method string) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	dr.methods = append(dr.methods, method)
}

func (dr *deleteRecorder) all() []string {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	out := make([]string, len(dr.methods))
	copy(out, dr.methods)
	return out
}

// fakeDeleteServer starts a test HTTP server that records the inbound method for /api/delete
// and returns 200 OK.
func fakeDeleteServer(t *testing.T, rec *deleteRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/delete" {
			rec.record(r.Method)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDelete_Broadcast verifies that DELETE /api/delete broadcasts to ALL nodes with method DELETE.
func TestDelete_Broadcast(t *testing.T) {
	rec1 := &deleteRecorder{}
	rec2 := &deleteRecorder{}
	srv1 := fakeDeleteServer(t, rec1)
	srv2 := fakeDeleteServer(t, rec2)

	appState := buildAppStateForPS(t, []*httptest.Server{srv1, srv2})

	r := chi.NewRouter()
	setupRoutes(r, appState)

	body := bytes.NewBufferString(`{"name":"x:1"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/delete", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code >= 400 {
		t.Fatalf("want success status, got %d; body: %s", w.Code, w.Body.String())
	}

	// Both nodes must have received exactly one DELETE request.
	methods1 := rec1.all()
	methods2 := rec2.all()

	if len(methods1) != 1 || methods1[0] != http.MethodDelete {
		t.Errorf("node1: want [DELETE], got %v", methods1)
	}
	if len(methods2) != 1 || methods2[0] != http.MethodDelete {
		t.Errorf("node2: want [DELETE], got %v", methods2)
	}
}

// TestDelete_WrongInboundMethod verifies that POST /api/delete does NOT trigger the broadcast handler.
// The catch-all handles POST /api/delete as a single-node proxy, so backends should not all
// receive a DELETE — at most one backend sees a POST (catch-all proxy), not a broadcast DELETE.
func TestDelete_WrongInboundMethod(t *testing.T) {
	rec1 := &deleteRecorder{}
	rec2 := &deleteRecorder{}
	srv1 := fakeDeleteServer(t, rec1)
	srv2 := fakeDeleteServer(t, rec2)

	appState := buildAppStateForPS(t, []*httptest.Server{srv1, srv2})

	r := chi.NewRouter()
	setupRoutes(r, appState)

	body := bytes.NewBufferString(`{"name":"x:1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/delete", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// The broadcast handler is DELETE-only. POST falls to catch-all (single-node proxy).
	// Neither node should have received a DELETE from the dedicated broadcast path.
	methods1 := rec1.all()
	methods2 := rec2.all()

	// If both nodes received a DELETE, the dedicated broadcast handler was wrongly triggered.
	if len(methods1) > 0 && methods1[len(methods1)-1] == http.MethodDelete &&
		len(methods2) > 0 && methods2[len(methods2)-1] == http.MethodDelete {
		t.Errorf("POST /api/delete must NOT trigger the broadcast DELETE handler; both nodes received DELETE")
	}
}

// TestEmbed_ExplicitRoute verifies that POST /api/embed is routed model-aware to a backend.
func TestEmbed_ExplicitRoute(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"embeddings":[]}`))
	}))
	defer srv.Close()

	baseURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("failed to parse server URL: %v", err)
	}

	ns := &NodeState{
		Name:         "embed-node",
		BaseURL:      baseURL,
		Proxy:        httputil.NewSingleHostReverseProxy(baseURL),
		OK:           true,
		LoadedModels: map[string]struct{}{"nomic-embed-text": {}},
		LocalModels:  map[string]struct{}{"nomic-embed-text": {}},
	}
	appState := &AppState{
		NodeStates:      map[string]*NodeState{"embed-node": ns},
		ModelOwnerCache: &sync.Map{},
		Client:          &http.Client{Timeout: 5 * time.Second},
		Config:          &Config{ModelCacheTTL: time.Minute, ReadTimeout: 30 * time.Second},
	}

	r := chi.NewRouter()
	setupRoutes(r, appState)

	body := bytes.NewBufferString(`{"model":"nomic-embed-text","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/embed", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code >= 400 {
		t.Fatalf("want success from POST /api/embed, got %d; body: %s", w.Code, w.Body.String())
	}
	if receivedPath != "/api/embed" {
		t.Errorf("want backend to receive /api/embed, got %q", receivedPath)
	}
}
