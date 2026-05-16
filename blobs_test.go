package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestStableHealthyNode(t *testing.T) {
	t.Run("no nodes at all", func(t *testing.T) {
		appState := &AppState{
			NodeStates:      map[string]*NodeState{},
			ModelOwnerCache: &sync.Map{},
			Config:          &Config{},
		}
		got := appState.stableHealthyNode()
		if got != nil {
			t.Errorf("want nil, got %s", got.Name)
		}
	})

	t.Run("all nodes unhealthy", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"a": {Name: "a", OK: false, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
				"b": {Name: "b", OK: false, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
			},
			ModelOwnerCache: &sync.Map{},
			Config:          &Config{},
		}
		got := appState.stableHealthyNode()
		if got != nil {
			t.Errorf("want nil, got %s", got.Name)
		}
	})

	t.Run("picks first by name among healthy, skips unhealthy", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"zeta":  {Name: "zeta", OK: true, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
				"alpha": {Name: "alpha", OK: true, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
				"beta":  {Name: "beta", OK: false, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
			},
			ModelOwnerCache: &sync.Map{},
			Config:          &Config{},
		}
		got := appState.stableHealthyNode()
		if got == nil {
			t.Fatal("want a node, got nil")
		}
		if got.Name != "alpha" {
			t.Errorf("want alpha (first healthy by name), got %s", got.Name)
		}
	})

	t.Run("deterministic across repeated calls", func(t *testing.T) {
		appState := &AppState{
			NodeStates: map[string]*NodeState{
				"zeta":  {Name: "zeta", OK: true, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
				"alpha": {Name: "alpha", OK: true, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
				"gamma": {Name: "gamma", OK: true, LoadedModels: map[string]struct{}{}, LocalModels: map[string]struct{}{}},
			},
			ModelOwnerCache: &sync.Map{},
			Config:          &Config{},
		}
		var first string
		for i := 0; i < 5; i++ {
			got := appState.stableHealthyNode()
			if got == nil {
				t.Fatal("want a node, got nil")
			}
			if i == 0 {
				first = got.Name
			} else if got.Name != first {
				t.Errorf("call %d: want %s (stable), got %s", i+1, first, got.Name)
			}
		}
		if first != "alpha" {
			t.Errorf("want alpha as stable node, got %s", first)
		}
	})
}

// requestRecord holds a single captured (method, path) pair from a backend.
type requestRecord struct {
	method string
	path   string
}

func TestBlobCreateColocation(t *testing.T) {
	// Build two recording backends.
	var muAlpha sync.Mutex
	var recordsAlpha []requestRecord

	var muZeta sync.Mutex
	var recordsZeta []requestRecord

	alphaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muAlpha.Lock()
		recordsAlpha = append(recordsAlpha, requestRecord{method: r.Method, path: r.URL.Path})
		muAlpha.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{}`)
	}))
	defer alphaServer.Close()

	zetaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muZeta.Lock()
		recordsZeta = append(recordsZeta, requestRecord{method: r.Method, path: r.URL.Path})
		muZeta.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{}`)
	}))
	defer zetaServer.Close()

	alphaURL, _ := url.Parse(alphaServer.URL)
	zetaURL, _ := url.Parse(zetaServer.URL)

	alphaNode := &NodeState{
		Name:         "alpha",
		BaseURL:      alphaURL,
		Proxy:        httputil.NewSingleHostReverseProxy(alphaURL),
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}
	zetaNode := &NodeState{
		Name:         "zeta",
		BaseURL:      zetaURL,
		Proxy:        httputil.NewSingleHostReverseProxy(zetaURL),
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}

	makeAppState := func() *AppState {
		return &AppState{
			NodeStates: map[string]*NodeState{
				"alpha": alphaNode,
				"zeta":  zetaNode,
			},
			ModelOwnerCache: &sync.Map{},
			Config:          &Config{ReadTimeout: 5 * time.Second},
		}
	}

	t.Run("all blob and create steps hit the same node", func(t *testing.T) {
		// Reset records.
		muAlpha.Lock()
		recordsAlpha = nil
		muAlpha.Unlock()
		muZeta.Lock()
		recordsZeta = nil
		muZeta.Unlock()

		// Reset node health.
		alphaNode.mu.Lock()
		alphaNode.OK = true
		alphaNode.mu.Unlock()
		zetaNode.mu.Lock()
		zetaNode.OK = true
		zetaNode.mu.Unlock()

		appState := makeAppState()
		r := chi.NewRouter()
		setupRoutes(r, appState)
		ts := httptest.NewServer(r)
		defer ts.Close()

		client := &http.Client{Timeout: 10 * time.Second}

		// HEAD /api/blobs/sha256:abc123
		req, _ := http.NewRequest(http.MethodHead, ts.URL+"/api/blobs/sha256:abc123", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HEAD blob: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("HEAD blob: want 200, got %d", resp.StatusCode)
		}

		// POST /api/blobs/sha256:abc123
		req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/blobs/sha256:abc123", strings.NewReader("blobdata"))
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST blob: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("POST blob: want 200, got %d", resp.StatusCode)
		}

		// POST /api/create
		req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/create", strings.NewReader(`{"model":"m","modelfile":"FROM ./x.gguf"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("POST create: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("POST create: want 200, got %d", resp.StatusCode)
		}

		// Verify all three requests landed on alpha (first healthy by name).
		muAlpha.Lock()
		gotAlpha := make([]requestRecord, len(recordsAlpha))
		copy(gotAlpha, recordsAlpha)
		muAlpha.Unlock()

		muZeta.Lock()
		gotZeta := make([]requestRecord, len(recordsZeta))
		copy(gotZeta, recordsZeta)
		muZeta.Unlock()

		expected := []requestRecord{
			{method: http.MethodHead, path: "/api/blobs/sha256:abc123"},
			{method: http.MethodPost, path: "/api/blobs/sha256:abc123"},
			{method: http.MethodPost, path: "/api/create"},
		}

		if len(gotAlpha) != len(expected) {
			t.Errorf("alpha received %d requests, want %d; got %v", len(gotAlpha), len(expected), gotAlpha)
		} else {
			for i, want := range expected {
				if gotAlpha[i] != want {
					t.Errorf("alpha request[%d]: want %+v, got %+v", i, want, gotAlpha[i])
				}
			}
		}

		if len(gotZeta) != 0 {
			t.Errorf("zeta received %d requests, want 0; got %v", len(gotZeta), gotZeta)
		}
	})

	t.Run("no healthy node returns 503", func(t *testing.T) {
		alphaNode.mu.Lock()
		alphaNode.OK = false
		alphaNode.mu.Unlock()
		zetaNode.mu.Lock()
		zetaNode.OK = false
		zetaNode.mu.Unlock()

		appState := makeAppState()
		r := chi.NewRouter()
		setupRoutes(r, appState)
		ts := httptest.NewServer(r)
		defer ts.Close()

		client := &http.Client{Timeout: 10 * time.Second}

		req, _ := http.NewRequest(http.MethodHead, ts.URL+"/api/blobs/sha256:x", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HEAD blob with no healthy nodes: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("want 503, got %d", resp.StatusCode)
		}
	})
}
