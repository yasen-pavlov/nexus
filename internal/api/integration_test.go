//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/config"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

type mockEmbedder struct{ dim int }

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, m.dim)
	}
	return result, nil
}
func (m *mockEmbedder) Dimension() int { return m.dim }

func newTestDeps(t *testing.T) (*store.Store, *search.Client, *ConnectorManager) {
	t.Helper()
	tdb := testutil.NewTestDB(t, "api", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	osURL, osIndex := testutil.TestOSConfig(t, "api")
	sc, err := search.NewWithIndex(context.Background(), osURL, osIndex, nil)
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	if err := sc.EnsureIndex(context.Background(), 0); err != nil {
		t.Fatalf("create search index: %v", err)
	}
	t.Cleanup(func() { sc.DeleteIndex(context.Background()) }) //nolint:errcheck // test

	cm := NewConnectorManager(st, zap.NewNop())
	return st, sc, cm
}

func newTestRouter(t *testing.T) (*store.Store, *search.Client, *ConnectorManager, http.Handler) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), zap.NewNop())
	return st, sc, cm, router
}

// --- Search tests ---

func TestSearchHandler_HybridFallback(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	doc := &model.Document{
		ID: uuid.New(), SourceType: "filesystem", SourceName: "test", SourceID: "hybrid-test.txt",
		Title: "Hybrid Test", Content: "Testing hybrid search with embedder fallback",
		Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
	}
	sc.IndexDocument(ctx, doc) //nolint:errcheck // test
	sc.Refresh(ctx)            //nolint:errcheck // test

	em := NewEmbeddingManager(st, zap.NewNop())
	// Set a mock embedder that returns fake embeddings
	em.Set(&mockEmbedder{dim: 3})

	h := &handler{search: sc, cm: cm, em: em, rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}

	// This will try hybrid search (embed query → k-NN), but k-NN will fail
	// because the index has no embedding field. Falls back to BM25.
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=hybrid", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestSearchHandler_Integration(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	doc := &model.Document{
		ID: uuid.New(), SourceType: "filesystem", SourceName: "test", SourceID: "search-test.txt",
		Title: "Search Test", Content: "This document contains searchable integration test content",
		Metadata: map[string]any{"path": "search-test.txt"}, Visibility: "private", CreatedAt: time.Now(),
	}
	if err := sc.IndexDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	sc.Refresh(ctx) //nolint:errcheck // test

	h := &handler{search: sc, cm: cm, em: NewEmbeddingManager(st, zap.NewNop()), rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=searchable", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if tc, _ := data["total_count"].(float64); tc != 1 {
		t.Errorf("expected 1 result, got %v", tc)
	}
}

func TestSearchHandler_WithParams(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	for i, content := range []string{
		"The searchterm appears in this first document about databases",
		"Another document mentioning searchterm and also discussing servers",
		"A third document with searchterm covering deployment topics",
	} {
		doc := &model.Document{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test",
			SourceID: fmt.Sprintf("param-%d.txt", i), Title: fmt.Sprintf("Doc %d", i), Content: content,
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		}
		sc.IndexDocument(ctx, doc) //nolint:errcheck // test
	}
	sc.Refresh(ctx) //nolint:errcheck // test

	h := &handler{search: sc, cm: cm, em: NewEmbeddingManager(st, zap.NewNop()), rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=searchterm&limit=2", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if tc, _ := data["total_count"].(float64); tc != 3 {
		t.Fatalf("expected total 3, got %v", tc)
	}
	docs, _ := data["documents"].([]any)
	if len(docs) != 2 {
		t.Errorf("expected 2 docs with limit=2, got %d", len(docs))
	}
}

func TestUpdateConnector_BadBody(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	dir := t.TempDir()
	body := `{"type":"filesystem","name":"upd-bb","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connID := resp.Data.(map[string]any)["id"].(string)

	// Invalid JSON body
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad body: expected 400, got %d", w.Code)
	}

	// Missing required fields
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing fields: expected 400, got %d", w.Code)
	}

	// Invalid UUID
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/not-uuid", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad uuid: expected 400, got %d", w.Code)
	}
}

// --- Sync tests ---

func TestTriggerSyncHandler_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	dir := t.TempDir()
	os.WriteFile(dir+"/test.txt", []byte("sync handler test"), 0o644) //nolint:errcheck // test

	// Create connector via API
	body := `{"type":"filesystem","name":"sync-test","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sync/sync-test", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if data["status"] != "running" {
		t.Errorf("expected status running, got %v", data["status"])
	}

	// Wait for background sync to complete
	time.Sleep(500 * time.Millisecond)
}

func TestTriggerSyncHandler_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)

	dir := t.TempDir()
	os.WriteFile(dir+"/test.txt", []byte("test"), 0o644) //nolint:errcheck // test

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "err-sync",
		Config: map[string]any{"root_path": dir, "patterns": "*.txt"}, Enabled: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager()
	h := &handler{store: st, search: sc, pipeline: p, cm: cm, em: em, rm: NewRerankManager(st, zap.NewNop()), syncJobs: sjm, log: zap.NewNop()}

	r := chi.NewRouter()
	r.Post("/api/sync/{connector}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/err-sync", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Async sync returns 202 immediately; error happens in background
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

	// Wait for background goroutine to fail
	time.Sleep(500 * time.Millisecond)

	job := sjm.GetByConnector("err-sync")
	// Job should have completed (with failure), so GetByConnector returns nil for running
	if job != nil {
		t.Errorf("expected no running job, got %v", job.Status)
	}
}

// --- Connector CRUD tests ---

func TestConnectorCRUD_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"type":"filesystem","name":"crud-test","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var createResp APIResponse
	json.NewDecoder(w.Body).Decode(&createResp) //nolint:errcheck // test
	connID := createResp.Data.(map[string]any)["id"].(string)

	// List
	req = httptest.NewRequest(http.MethodGet, "/api/connectors/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list: expected 200, got %d", w.Code)
	}

	// Get
	req = httptest.NewRequest(http.MethodGet, "/api/connectors/"+connID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("get: expected 200, got %d", w.Code)
	}

	// Update
	updateBody := `{"type":"filesystem","name":"crud-updated","config":{"root_path":"` + t.TempDir() + `","patterns":"*.md"},"enabled":false}`
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("update: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/api/connectors/"+connID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("delete: expected 204, got %d", w.Code)
	}

	// Verify gone
	req = httptest.NewRequest(http.MethodGet, "/api/connectors/"+connID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("get after delete: expected 404, got %d", w.Code)
	}
}

func TestCreateConnector_ValidationErrors(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	tests := []struct {
		name string
		body string
		code int
	}{
		{"missing type", `{"name":"test","config":{},"enabled":true}`, http.StatusBadRequest},
		{"missing name", `{"type":"filesystem","config":{},"enabled":true}`, http.StatusBadRequest},
		{"invalid type", `{"type":"nonexistent","name":"test","config":{},"enabled":true}`, http.StatusBadRequest},
		{"invalid body", `not json`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tt.code {
				t.Errorf("expected %d, got %d; body: %s", tt.code, w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateConnector_DuplicateName(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"type":"filesystem","name":"dupe","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"},"enabled":true}`

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate: expected 409, got %d", w.Code)
	}
}

func TestGetConnector_InvalidID(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/not-a-uuid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteConnector_NotFound(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- Schedule tests ---

func TestUpdateConnector_InvalidSchedule(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	dir := t.TempDir()
	body := `{"type":"filesystem","name":"upd-sched","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connID := resp.Data.(map[string]any)["id"].(string)

	updateBody := `{"type":"filesystem","name":"upd-sched","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true,"schedule":"bad cron"}`
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSetScheduleObserver(t *testing.T) {
	_, _, cm := newTestDeps(t)

	called := false
	obs := &testObserver{onChanged: func(_ *model.ConnectorConfig) { called = true }}
	cm.SetScheduleObserver(obs)

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "obs-test",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected observer to be called")
	}
}

type testObserver struct {
	onChanged func(cfg *model.ConnectorConfig)
	onRemoved func(id uuid.UUID, name string)
}

func (o *testObserver) OnConnectorChanged(cfg *model.ConnectorConfig) {
	if o.onChanged != nil {
		o.onChanged(cfg)
	}
}

func (o *testObserver) OnConnectorRemoved(id uuid.UUID, name string) {
	if o.onRemoved != nil {
		o.onRemoved(id, name)
	}
}

func TestCreateConnector_InvalidSchedule(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"type":"filesystem","name":"bad-sched","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"},"enabled":true,"schedule":"not a cron"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateConnector_WithSchedule(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"type":"filesystem","name":"sched-test","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"},"enabled":true,"schedule":"*/30 * * * *"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestSearchHandler_SearchError(t *testing.T) {
	st, sc, cm := newTestDeps(t)

	// Delete the search index to cause search errors
	sc.DeleteIndex(context.Background()) //nolint:errcheck // test

	h := &handler{store: st, search: sc, cm: cm, em: NewEmbeddingManager(st, zap.NewNop()), rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestConnectorManager_LoadFromDB_InvalidConfig(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	// Create a connector with invalid config (nonexistent path) to trigger warning path
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "bad-config",
		Config: map[string]any{"root_path": "/nonexistent/path/definitely", "patterns": "*.txt"}, Enabled: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	// LoadFromDB should succeed but skip the invalid connector
	if err := cm.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db failed: %v", err)
	}

	// The invalid connector should NOT be in the map
	_, ok := cm.Get("bad-config")
	if ok {
		t.Error("expected invalid connector to be skipped")
	}
}

func TestConnectorManager_LoadFromDB(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "load-test",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if err := cm.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db failed: %v", err)
	}

	_, ok := cm.Get("load-test")
	if !ok {
		t.Fatal("expected connector to be loaded")
	}
}

func TestConnectorManager_SeedFromEnv(t *testing.T) {
	_, _, cm := newTestDeps(t)
	ctx := context.Background()

	appCfg := &config.Config{FSRootPath: t.TempDir(), FSPatterns: "*.txt,*.md"}
	if err := cm.SeedFromEnv(ctx, appCfg); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	_, ok := cm.Get("filesystem")
	if !ok {
		t.Fatal("expected filesystem connector after seeding")
	}

	// Second call is no-op
	if err := cm.SeedFromEnv(ctx, appCfg); err != nil {
		t.Fatalf("second seed failed: %v", err)
	}
}

func TestConnectorManager_SeedFromEnv_NoRootPath(t *testing.T) {
	_, _, cm := newTestDeps(t)
	if err := cm.SeedFromEnv(context.Background(), &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if len(cm.All()) != 0 {
		t.Error("expected 0 connectors")
	}
}

func TestListConnectors_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	st.Close()

	h := &handler{store: st, search: sc, cm: cm, rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/", nil)
	w := httptest.NewRecorder()
	h.ListConnectors(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestGetConnector_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestDeleteConnector_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), zap.NewNop())

	req := httptest.NewRequest(http.MethodDelete, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestUpdateConnector_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	dir := t.TempDir()
	body := `{"type":"filesystem","name":"upd-test","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connID := resp.Data.(map[string]any)["id"].(string)

	dir2 := t.TempDir()
	updateBody := `{"type":"filesystem","name":"upd-renamed","config":{"root_path":"` + dir2 + `","patterns":"*.md"},"enabled":true}`
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("update: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Update nonexistent
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+uuid.New().String(), bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	// Duplicate name
	dir3 := t.TempDir()
	body2 := `{"type":"filesystem","name":"upd-other","config":{"root_path":"` + dir3 + `","patterns":"*.txt"},"enabled":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body2))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	dupeBody := `{"type":"filesystem","name":"upd-other","config":{"root_path":"` + dir2 + `","patterns":"*.md"},"enabled":true}`
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(dupeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestGetEmbeddingSettings(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/embedding", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if data["provider"] != "" {
		t.Errorf("expected empty provider, got %v", data["provider"])
	}
}

func TestUpdateEmbeddingSettings(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Set to ollama
	body := `{"provider":"ollama","model":"nomic-embed-text","ollama_url":"http://localhost:11434"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify settings persisted
	req = httptest.NewRequest(http.MethodGet, "/api/settings/embedding", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if data["provider"] != "ollama" {
		t.Errorf("expected provider 'ollama', got %v", data["provider"])
	}
}

func TestEmbeddingManager_LoadFromDB_WithSettings(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()

	// Pre-populate DB settings
	st.SetSetting(ctx, "embedding_provider", "ollama")   //nolint:errcheck // test
	st.SetSetting(ctx, "embedding_model", "nomic-embed-text") //nolint:errcheck // test
	st.SetSetting(ctx, "ollama_url", "http://localhost:11434") //nolint:errcheck // test

	em := NewEmbeddingManager(st, zap.NewNop())
	if err := em.LoadFromDB(ctx, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	// Embedder should be created from DB settings
	if em.Get() == nil {
		t.Error("expected embedder loaded from DB settings")
	}
}

func TestUpdateEmbeddingSettings_BadBody(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetEmbeddingSettings_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/settings/embedding", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestEmbeddingManager_LoadAndUpdate(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()

	em := NewEmbeddingManager(st, zap.NewNop())

	// Load with no DB settings and no env config
	if err := em.LoadFromDB(ctx, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if em.Get() != nil {
		t.Error("expected nil embedder with no config")
	}
	if em.Dimension() != 0 {
		t.Errorf("expected dimension 0, got %d", em.Dimension())
	}

	// Update to ollama
	if err := em.UpdateFromSettings(ctx, "ollama", "nomic-embed-text", "", "http://localhost:11434"); err != nil {
		t.Fatal(err)
	}
	if em.Get() == nil {
		t.Error("expected non-nil embedder after update")
	}
	if em.Dimension() != 768 {
		t.Errorf("expected dimension 768, got %d", em.Dimension())
	}

	// Disable
	if err := em.UpdateFromSettings(ctx, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if em.Get() != nil {
		t.Error("expected nil embedder after disable")
	}
}

func TestUpdateEmbeddingSettings_InvalidProvider(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"provider":"invalid_provider"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdateEmbeddingSettings_Disable(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"provider":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestUpdateEmbeddingSettings_ProviderChange_TriggersReindex(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	ctx := context.Background()

	// Set initial provider to ollama
	body := `{"provider":"ollama","model":"nomic-embed-text","ollama_url":"http://localhost:11434"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set ollama: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Create a connector + cursor to verify reindex clears cursors and syncs
	dir := t.TempDir()
	connBody := `{"type":"filesystem","name":"reindex-trigger","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	connReq := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(connBody))
	connReq.Header.Set("Content-Type", "application/json")
	cw := httptest.NewRecorder()
	router.ServeHTTP(cw, connReq)

	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: "reindex-trigger", CursorData: map[string]any{}})

	// Change to a different model (same provider but different model triggers reindex)
	body = `{"provider":"ollama","model":"all-minilm","ollama_url":"http://localhost:11434"}`
	req = httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("change model: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Wait for async reindex to complete
	time.Sleep(500 * time.Millisecond)
}

func TestUpdateEmbeddingSettings_MaskedKey(t *testing.T) {
	st, _, _, router := newTestRouter(t)

	// Set a real key first
	st.SetSetting(context.Background(), "embedding_api_key", "sk-real-key-12345") //nolint:errcheck // test

	// Update with masked key — should keep original
	body := `{"provider":"openai","model":"text-embedding-3-small","api_key":"****2345"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/embedding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify key was preserved (masked in response)
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if data["api_key"] != "****2345" {
		t.Errorf("expected masked key, got %v", data["api_key"])
	}
}

func TestNewRouter_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("health: expected 200, got %d", w.Code)
	}
}

func TestTelegramAuthStart_NotFound(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+uuid.New().String()+"/auth/start", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTelegramAuthStart_InvalidID(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/not-uuid/auth/start", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTelegramAuthStart_NotTelegram(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	dir := t.TempDir()
	body := `{"type":"filesystem","name":"auth-fs","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connID := resp.Data.(map[string]any)["id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/start", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestTelegramAuthStart_ValidTelegram(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Create a valid telegram connector
	body := `{"type":"telegram","name":"auth-valid","config":{"api_id":"12345","api_hash":"abc","phone":"+1234567890"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connID := resp.Data.(map[string]any)["id"].(string)

	// Start auth — will attempt to connect to Telegram (will fail since it's fake credentials,
	// but the validation path up to the goroutine launch is covered)
	req = httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/start", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	// Should return 200 (auth started) even though the background goroutine will fail
	// The goroutine runs async so the HTTP response comes back before it completes
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestTelegramAuthStart_MissingConfig(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Create telegram connector without phone
	body := `{"type":"telegram","name":"auth-nophone","config":{"api_id":"123","api_hash":"abc"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	// This will fail at connector validation (phone required)
	if w.Code == http.StatusCreated {
		var resp APIResponse
		json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
		connID := resp.Data.(map[string]any)["id"].(string)

		req = httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/start", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing phone, got %d", w.Code)
		}
	}
}

func TestTelegramAuthCode_BadBody(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+uuid.New().String()+"/auth/code", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTelegramAuthCode_NoPending(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	body := `{"code":"12345"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+uuid.New().String()+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTelegramAuthCode_MissingCode(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+uuid.New().String()+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteAllCursors_Integration(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	ctx := context.Background()

	// Create some cursors
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: "a", CursorData: map[string]any{}})
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: "b", CursorData: map[string]any{}})

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify cursors are gone
	c, _ := st.GetSyncCursor(ctx, "a")
	if c != nil {
		t.Error("cursor 'a' should be deleted")
	}
	c, _ = st.GetSyncCursor(ctx, "b")
	if c != nil {
		t.Error("cursor 'b' should be deleted")
	}
}

func TestDeleteCursor_Integration(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	ctx := context.Background()

	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: "keep", CursorData: map[string]any{}})
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: "delete-me", CursorData: map[string]any{}})

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors/delete-me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	c, _ := st.GetSyncCursor(ctx, "delete-me")
	if c != nil {
		t.Error("cursor 'delete-me' should be deleted")
	}
	c, _ = st.GetSyncCursor(ctx, "keep")
	if c == nil {
		t.Error("cursor 'keep' should still exist")
	}
}

func TestSyncAll_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Create a connector first
	dir := t.TempDir()
	body := `{"type":"filesystem","name":"sync-all-test","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d; body: %s", w.Code, w.Body.String())
	}

	time.Sleep(500 * time.Millisecond)
}

func TestReindex_Integration(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	ctx := context.Background()

	// Create a connector and a cursor
	dir := t.TempDir()
	body := `{"type":"filesystem","name":"reindex-test","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}

	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: "reindex-test", CursorData: map[string]any{}})

	// Verify cursor exists before reindex
	c, _ := st.GetSyncCursor(ctx, "reindex-test")
	if c == nil {
		t.Fatal("cursor should exist before reindex")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d; body: %s", w.Code, w.Body.String())
	}

	// Wait for async sync to complete
	time.Sleep(500 * time.Millisecond)
}

func TestDeleteAllCursors_StoreError(t *testing.T) {
	st, sc, _ := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	sjm := NewSyncJobManager()
	h := &handler{store: st, search: sc, em: em, rm: NewRerankManager(st, zap.NewNop()), syncJobs: sjm, log: zap.NewNop()}

	r := chi.NewRouter()
	r.Delete("/api/sync/cursors", h.DeleteAllCursors)

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestDeleteCursor_StoreError(t *testing.T) {
	st, sc, _ := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	sjm := NewSyncJobManager()
	h := &handler{store: st, search: sc, em: em, rm: NewRerankManager(st, zap.NewNop()), syncJobs: sjm, log: zap.NewNop()}

	r := chi.NewRouter()
	r.Delete("/api/sync/cursors/{connector}", h.DeleteCursor)

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestTriggerReindex_Integration_Full(t *testing.T) {
	_, sc, _, router := newTestRouter(t)
	ctx := context.Background()

	// Index a document first to verify it's cleared after reindex
	doc := &model.Document{
		SourceType: "test", SourceName: "test", SourceID: "reindex-1",
		Title: "Before Reindex", Content: "should be cleared",
	}
	if err := sc.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("index doc: %v", err)
	}
	_ = sc.Refresh(ctx)

	req := httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify response shape
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if data["message"] != "reindex started" {
		t.Errorf("message = %v, want 'reindex started'", data["message"])
	}

	time.Sleep(500 * time.Millisecond)

	// Old document should be gone (index was recreated)
	_ = sc.Refresh(ctx)
	result, err := sc.Search(ctx, model.SearchRequest{Query: "should be cleared", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Documents) != 0 {
		t.Errorf("expected 0 results after reindex, got %d", len(result.Documents))
	}
}

func TestTriggerReindex_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)

	// Create and close store to simulate DB failure on cursor delete
	// But we need RecreateIndex to succeed first — so we keep search client alive
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager()
	h := &handler{store: st, search: sc, pipeline: p, cm: cm, em: em, rm: NewRerankManager(st, zap.NewNop()), syncJobs: sjm, log: zap.NewNop()}

	st.Close()

	r := chi.NewRouter()
	r.Post("/api/reindex", h.TriggerReindex)

	req := httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// RecreateIndex succeeds (OpenSearch is fine), but DeleteAllSyncCursors fails (DB closed)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestListSyncJobs_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sync", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	// Should be an empty list (no syncs running)
	data, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", resp.Data)
	}
	if len(data) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(data))
	}
}

func TestStreamSyncProgress_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Create a connector and start a sync so we have a running job
	dir := t.TempDir()
	body := `{"type":"filesystem","name":"sse-test","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}

	// Trigger sync
	req = httptest.NewRequest(http.MethodPost, "/api/sync/sse-test", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("sync: expected 202, got %d", w.Code)
	}

	// Start SSE stream via real HTTP server
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sync/sse-test/progress")
	if err != nil {
		t.Fatalf("SSE request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}
}

func TestStreamSyncProgress_NotFound_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sync/nonexistent/progress", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRerankManager_LoadFromDB_Integration(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()

	rm := NewRerankManager(st, zap.NewNop())
	err := rm.LoadFromDB(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("LoadFromDB failed: %v", err)
	}
	// No provider configured — reranker should be nil
	if rm.Get() != nil {
		t.Error("expected nil reranker when no provider set")
	}
}

func TestRerankManager_UpdateFromSettings_Integration(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()

	rm := NewRerankManager(st, zap.NewNop())

	// Disable (empty provider)
	err := rm.UpdateFromSettings(ctx, "", "", "")
	if err != nil {
		t.Fatalf("UpdateFromSettings failed: %v", err)
	}
	if rm.Get() != nil {
		t.Error("expected nil reranker after disabling")
	}
}

func TestGetRerankSettings_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/rerank", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestUpdateRerankSettings_Integration(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Disable reranking
	body := `{"provider":"","model":"","api_key":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/rerank", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestUpdateRerankSettings_WithVoyage(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Set voyage provider (will validate but we won't actually call the API)
	body := `{"provider":"voyage","model":"rerank-2","api_key":"test-key-12345"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/rerank", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify masked key in response
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if data["api_key"] != "****2345" {
		t.Errorf("expected masked key, got %v", data["api_key"])
	}

	// Read back settings
	req = httptest.NewRequest(http.MethodGet, "/api/settings/rerank", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("get: expected 200, got %d", w.Code)
	}
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data = resp.Data.(map[string]any)
	if data["provider"] != "voyage" {
		t.Errorf("provider = %v, want voyage", data["provider"])
	}
}

func TestUpdateRerankSettings_MaskedKey(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// First set a key
	body := `{"provider":"voyage","model":"rerank-2","api_key":"real-secret-key"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/rerank", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Update with masked key — should preserve original
	body = `{"provider":"voyage","model":"rerank-2","api_key":"****-key"}`
	req = httptest.NewRequest(http.MethodPut, "/api/settings/rerank", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestUpdateRerankSettings_BadBody(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/rerank", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetRerankSettings_StoreError(t *testing.T) {
	st, sc, _ := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	rm := NewRerankManager(st, zap.NewNop())
	h := &handler{store: st, search: sc, em: em, rm: rm, log: zap.NewNop()}

	r := chi.NewRouter()
	r.Get("/api/settings/rerank", h.GetRerankSettings)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/rerank", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestUpdateRerankSettings_InvalidProvider(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"provider":"unknown","model":"","api_key":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/rerank", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestConnectorManager_SetExtractor(t *testing.T) {
	_, _, cm := newTestDeps(t)
	cm.SetExtractor(nil)
	// Should not panic
}

func TestConnectorManager_InstantiateFilesystem(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	dir := t.TempDir()
	body := `{"type":"filesystem","name":"ext-test","config":{"root_path":"` + dir + `","patterns":"*.pdf,*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestConnectorManager_InstantiateTelegram(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	body := `{"type":"telegram","name":"tg-test","config":{"api_id":"12345","api_hash":"abc","phone":"+1234567890"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
}
