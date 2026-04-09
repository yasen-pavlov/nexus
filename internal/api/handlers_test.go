package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rerank"
	"go.uber.org/zap"
)

type mockConnector struct {
	name     string
	typ      string
	fetchErr error
}

func (m *mockConnector) Type() string                       { return m.typ }
func (m *mockConnector) Name() string                       { return m.name }
func (m *mockConnector) Configure(_ connector.Config) error { return nil }
func (m *mockConnector) Validate() error                    { return nil }

func (m *mockConnector) Fetch(_ context.Context, _ *model.SyncCursor) (*model.FetchResult, error) {
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}
	return &model.FetchResult{}, nil
}

func newTestHandler() *handler {
	cm := &ConnectorManager{
		connectors: map[string]connector.Connector{
			"test-fs": &mockConnector{name: "test-fs", typ: "filesystem"},
		},
		log: zap.NewNop(),
	}
	return &handler{
		cm:       cm,
		rm:       NewRerankManager(nil, zap.NewNop()),
		syncJobs: NewSyncJobManager(),
		pipeline: pipeline.New(nil, nil, nil, zap.NewNop()),
		log:      zap.NewNop(),
	}
}

func TestHealthHandler(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	h.Health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestSearchHandler_MissingQuery(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	w := httptest.NewRecorder()

	h.Search(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected error message for missing query")
	}
}

func TestTriggerSyncHandler_NotFound(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Post("/api/sync/{connector}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/nonexistent", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestTriggerSyncHandler_Accepted(t *testing.T) {
	h := newTestHandler()
	// Need a pipeline for async sync to work — but since it runs in a goroutine,
	// we just verify the 202 response. Pipeline is nil so the goroutine will fail
	// silently in the background.

	r := chi.NewRouter()
	r.Post("/api/sync/{connector}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/test-fs", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", w.Code)
	}

	var resp struct {
		Data *SyncJob `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected sync job in response")
	}
	if resp.Data.Status != "running" {
		t.Errorf("status = %q, want running", resp.Data.Status)
	}
	if resp.Data.ConnectorName != "test-fs" {
		t.Errorf("connector_name = %q, want test-fs", resp.Data.ConnectorName)
	}
}

func TestTriggerSyncHandler_Conflict(t *testing.T) {
	h := newTestHandler()

	// Start a job for test-fs
	h.syncJobs.Start("test-fs", "filesystem")

	r := chi.NewRouter()
	r.Post("/api/sync/{connector}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/test-fs", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", w.Code)
	}
}

func TestListSyncJobsHandler(t *testing.T) {
	h := newTestHandler()
	h.syncJobs.Start("a", "filesystem")
	h.syncJobs.Start("b", "imap")

	req := httptest.NewRequest(http.MethodGet, "/api/sync", nil)
	w := httptest.NewRecorder()

	h.ListSyncJobs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Data []*SyncJob `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("got %d jobs, want 2", len(resp.Data))
	}
}

func TestSyncAllHandler(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	w := httptest.NewRecorder()

	h.SyncAll(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

type mockReranker struct {
	results []rerank.Result
}

func (m *mockReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.Result, error) {
	if m.results != nil {
		return m.results, nil
	}
	// Reverse order by default
	results := make([]rerank.Result, len(docs))
	for i := range docs {
		results[i] = rerank.Result{Index: len(docs) - 1 - i, Score: float64(len(docs)-i) * 0.1}
	}
	return results, nil
}

func TestRerankResults(t *testing.T) {
	rm := NewRerankManager(nil, zap.NewNop())
	rm.Set(&mockReranker{
		results: []rerank.Result{
			{Index: 2, Score: 0.9},
			{Index: 0, Score: 0.5},
			{Index: 1, Score: 0.3},
		},
	})

	h := &handler{rm: rm, log: zap.NewNop()}

	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "A"}, Rank: 1.0},
			{Document: model.Document{Title: "B"}, Rank: 0.8},
			{Document: model.Document{Title: "C"}, Rank: 0.6},
		},
	}

	reranked := h.rerankResults(context.Background(), "test", result)

	if len(reranked.Documents) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(reranked.Documents))
	}
	if reranked.Documents[0].Title != "C" {
		t.Errorf("first result should be C (highest rerank score), got %q", reranked.Documents[0].Title)
	}
	if reranked.Documents[0].Rank != 0.9 {
		t.Errorf("first result score = %f, want 0.9", reranked.Documents[0].Rank)
	}
}

func TestRerankResults_NoReranker(t *testing.T) {
	rm := NewRerankManager(nil, zap.NewNop())
	h := &handler{rm: rm, log: zap.NewNop()}

	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "A"}},
		},
	}

	reranked := h.rerankResults(context.Background(), "test", result)
	if reranked.Documents[0].Title != "A" {
		t.Error("should return original order when no reranker")
	}
}

func TestReorderByRerankScores(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "First"}},
			{Document: model.Document{Title: "Second"}},
		},
	}

	ranked := []rerank.Result{
		{Index: 1, Score: 0.9},
		{Index: 0, Score: 0.1},
	}

	reordered := reorderByRerankScores(result, ranked)
	if reordered.Documents[0].Title != "Second" {
		t.Errorf("expected Second first, got %q", reordered.Documents[0].Title)
	}
}

func TestParseCSV(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"paperless", 1},
		{"paperless,filesystem", 2},
		{"paperless, filesystem , ", 2},
	}
	for _, tt := range tests {
		result := parseCSV(tt.input)
		if len(result) != tt.want {
			t.Errorf("parseCSV(%q) = %d items, want %d", tt.input, len(result), tt.want)
		}
	}
}
