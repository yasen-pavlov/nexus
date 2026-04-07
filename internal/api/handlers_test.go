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
	"go.uber.org/zap"
)

type mockConnector struct {
	name string
	typ  string
}

func (m *mockConnector) Type() string                       { return m.typ }
func (m *mockConnector) Name() string                       { return m.name }
func (m *mockConnector) Configure(_ connector.Config) error { return nil }
func (m *mockConnector) Validate() error                    { return nil }

func (m *mockConnector) Fetch(_ context.Context, _ *model.SyncCursor) (*model.FetchResult, error) {
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
		cm:  cm,
		log: zap.NewNop(),
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
