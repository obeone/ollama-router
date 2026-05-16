package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fakeTagsServer starts a test HTTP server that returns a minimal /api/tags response.
func fakeTagsServer(t *testing.T, models []OllamaTagModel) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(OllamaTagsResponse{Models: models})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildAppStateForTags builds a minimal AppState backed by a single healthy fake node.
func buildAppStateForTags(t *testing.T, srv *httptest.Server) *AppState {
	t.Helper()
	baseURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("failed to parse fake server URL: %v", err)
	}
	ns := &NodeState{
		Name:         "fake",
		BaseURL:      baseURL,
		OK:           true,
		LoadedModels: map[string]struct{}{},
		LocalModels:  map[string]struct{}{},
	}
	return &AppState{
		NodeStates:      map[string]*NodeState{"fake": ns},
		ModelOwnerCache: &sync.Map{},
		Client:          &http.Client{Timeout: 5 * time.Second},
		Config:          &Config{ModelCacheTTL: time.Minute},
	}
}

// routerForModel builds a chi router with only the wildcard model route registered.
func routerForModel(appState *AppState) http.Handler {
	r := chi.NewRouter()
	r.Get("/v1/models/*", appState.handleOpenAIModel)
	return r
}

func TestHandleOpenAIModel_HappyPath(t *testing.T) {
	modifiedAt := time.Unix(1700000000, 0)
	models := []OllamaTagModel{
		{Name: "gpt-oss:20b", ModifiedAt: modifiedAt, Size: 1024},
	}
	srv := fakeTagsServer(t, models)
	appState := buildAppStateForTags(t, srv)
	router := routerForModel(appState)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-oss:20b", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var got OpenAIModel
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.ID != "gpt-oss:20b" {
		t.Errorf("want ID=%q, got %q", "gpt-oss:20b", got.ID)
	}
	if got.Object != "model" {
		t.Errorf("want Object=%q, got %q", "model", got.Object)
	}
	if got.OwnedBy != "ollama" {
		t.Errorf("want OwnedBy=%q, got %q", "ollama", got.OwnedBy)
	}
	if got.Created != modifiedAt.Unix() {
		t.Errorf("want Created=%d, got %d", modifiedAt.Unix(), got.Created)
	}
}

func TestHandleOpenAIModel_NotFound(t *testing.T) {
	models := []OllamaTagModel{
		{Name: "gpt-oss:20b", ModifiedAt: time.Now()},
	}
	srv := fakeTagsServer(t, models)
	appState := buildAppStateForTags(t, srv)
	router := routerForModel(appState)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/does-not-exist", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d; body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if body.Error.Code != "model_not_found" {
		t.Errorf("want code=%q, got %q", "model_not_found", body.Error.Code)
	}
	if body.Error.Type != "invalid_request_error" {
		t.Errorf("want type=%q, got %q", "invalid_request_error", body.Error.Type)
	}
}

func TestHandleOpenAIModel_SlashInName(t *testing.T) {
	modelName := "foo/bar:latest"
	models := []OllamaTagModel{
		{Name: modelName, ModifiedAt: time.Now()},
	}
	srv := fakeTagsServer(t, models)
	appState := buildAppStateForTags(t, srv)
	router := routerForModel(appState)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/models/%s", modelName), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for slash-containing name, got %d; body: %s", w.Code, w.Body.String())
	}

	var got OpenAIModel
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.ID != modelName {
		t.Errorf("want ID=%q, got %q", modelName, got.ID)
	}
}
