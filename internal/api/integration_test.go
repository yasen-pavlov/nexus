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
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func newTestStoreAndManager(t *testing.T) (*store.Store, *ConnectorManager) {
	t.Helper()
	tdb := testutil.NewTestDB(t, "api", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cm := NewConnectorManager(st, zap.NewNop())
	return st, cm
}

func newTestRouter(t *testing.T) (*store.Store, *ConnectorManager, http.Handler) {
	t.Helper()
	st, cm := newTestStoreAndManager(t)
	p := pipeline.New(st, zap.NewNop())
	router := NewRouter(st, p, cm, zap.NewNop())
	return st, cm, router
}

// --- Search tests ---

func TestSearchHandler_Integration(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	ctx := context.Background()

	doc := &model.Document{
		ID: uuid.New(), SourceType: "filesystem", SourceName: "test", SourceID: "search-test.txt",
		Title: "Search Test", Content: "This document contains searchable integration test content",
		Metadata: map[string]any{"path": "search-test.txt"}, Visibility: "private", CreatedAt: time.Now(),
	}
	if err := st.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	h := &handler{store: st, cm: cm, log: zap.NewNop()}

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
	st, cm := newTestStoreAndManager(t)
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
		if err := st.UpsertDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	h := &handler{store: st, cm: cm, log: zap.NewNop()}

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

func TestSearchHandler_StoreError(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	st.Close()

	h := &handler{store: st, cm: cm, log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- Sync tests ---

func TestTriggerSyncHandler_Integration(t *testing.T) {
	st, cm, router := newTestRouter(t)

	dir := t.TempDir()
	os.WriteFile(dir+"/test.txt", []byte("sync handler test"), 0o644) //nolint:errcheck // test

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "sync-test",
		Config: map[string]any{"root_path": dir, "patterns": "*.txt"}, Enabled: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sync/sync-test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	if dp, _ := data["docs_processed"].(float64); dp != 1 {
		t.Errorf("expected 1 doc, got %v", dp)
	}
	_ = st // keep reference
}

func TestTriggerSyncHandler_StoreError(t *testing.T) {
	st, cm := newTestStoreAndManager(t)

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

	p := pipeline.New(st, zap.NewNop())
	h := &handler{store: st, pipeline: p, cm: cm, log: zap.NewNop()}

	r := newChiRouter()
	r.Post("/api/sync/{connector}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/err-sync", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func newChiRouter() *chi.Mux {
	return chi.NewRouter()
}

// --- Connector CRUD tests ---

func TestConnectorCRUD_Integration(t *testing.T) {
	_, _, router := newTestRouter(t)

	// Create
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
	created := createResp.Data.(map[string]any)
	connID := created["id"].(string)

	// List
	req = httptest.NewRequest(http.MethodGet, "/api/connectors/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("list: expected 200, got %d", w.Code)
	}
	var listResp APIResponse
	json.NewDecoder(w.Body).Decode(&listResp) //nolint:errcheck // test
	connectors := listResp.Data.([]any)
	if len(connectors) != 1 {
		t.Errorf("list: expected 1, got %d", len(connectors))
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
	_, _, router := newTestRouter(t)

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
	_, _, router := newTestRouter(t)

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
	_, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/not-a-uuid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteConnector_NotFound(t *testing.T) {
	_, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestConnectorManager_LoadFromDB(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	ctx := context.Background()

	// Create a config directly in DB
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

	conn, ok := cm.Get("load-test")
	if !ok {
		t.Fatal("expected connector to be loaded")
	}
	if conn.Type() != "filesystem" {
		t.Errorf("expected type filesystem, got %q", conn.Type())
	}
}

func TestConnectorManager_LoadFromDB_SkipsDisabled(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "disabled-test",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: false,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if err := cm.LoadFromDB(ctx); err != nil {
		t.Fatal(err)
	}

	_, ok := cm.Get("disabled-test")
	if ok {
		t.Error("disabled connector should not be loaded")
	}
}

func TestConnectorManager_SeedFromEnv(t *testing.T) {
	_, cm := newTestStoreAndManager(t)
	ctx := context.Background()

	appCfg := &config.Config{
		FSRootPath: t.TempDir(),
		FSPatterns: "*.txt,*.md",
	}

	if err := cm.SeedFromEnv(ctx, appCfg); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	conn, ok := cm.Get("filesystem")
	if !ok {
		t.Fatal("expected filesystem connector after seeding")
	}
	if conn.Type() != "filesystem" {
		t.Errorf("expected type filesystem, got %q", conn.Type())
	}

	// Second call should be a no-op
	if err := cm.SeedFromEnv(ctx, appCfg); err != nil {
		t.Fatalf("second seed failed: %v", err)
	}
}

func TestConnectorManager_SeedFromEnv_NoRootPath(t *testing.T) {
	_, cm := newTestStoreAndManager(t)
	ctx := context.Background()

	if err := cm.SeedFromEnv(ctx, &config.Config{}); err != nil {
		t.Fatalf("seed with empty config failed: %v", err)
	}

	all := cm.All()
	if len(all) != 0 {
		t.Errorf("expected 0 connectors, got %d", len(all))
	}
}

func TestUpdateConnector_Integration(t *testing.T) {
	_, _, router := newTestRouter(t)

	dir := t.TempDir()

	// Create first
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

	// Update with valid data
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
		t.Errorf("update nonexistent: expected 404, got %d", w.Code)
	}

	// Update with missing fields
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("update missing fields: expected 400, got %d", w.Code)
	}

	// Update with invalid ID
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/not-uuid", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("update invalid id: expected 400, got %d", w.Code)
	}

	// Update with invalid JSON body
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("update bad body: expected 400, got %d", w.Code)
	}

	// Create second connector to test duplicate name on update
	dir3 := t.TempDir()
	body2 := `{"type":"filesystem","name":"upd-other","config":{"root_path":"` + dir3 + `","patterns":"*.txt"},"enabled":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body2))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Try to rename first connector to second's name
	dupeBody := `{"type":"filesystem","name":"upd-other","config":{"root_path":"` + dir2 + `","patterns":"*.md"},"enabled":true}`
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(dupeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("update dupe name: expected 409, got %d", w.Code)
	}
}

func TestListConnectors_StoreError(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	st.Close()

	h := &handler{store: st, cm: cm, log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/", nil)
	w := httptest.NewRecorder()
	h.ListConnectors(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestGetConnector_StoreError(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	st.Close()

	p := pipeline.New(st, zap.NewNop())
	router := NewRouter(st, p, cm, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestDeleteConnector_StoreError(t *testing.T) {
	st, cm := newTestStoreAndManager(t)
	st.Close()

	p := pipeline.New(st, zap.NewNop())
	router := NewRouter(st, p, cm, zap.NewNop())

	req := httptest.NewRequest(http.MethodDelete, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestUpdateConnector_InvalidSchedule(t *testing.T) {
	_, _, router := newTestRouter(t)

	// Create first
	dir := t.TempDir()
	body := `{"type":"filesystem","name":"upd-sched","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connID := resp.Data.(map[string]any)["id"].(string)

	// Update with invalid schedule
	updateBody := `{"type":"filesystem","name":"upd-sched","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true,"schedule":"bad cron"}`
	req = httptest.NewRequest(http.MethodPut, "/api/connectors/"+connID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid schedule on update, got %d", w.Code)
	}
}

func TestSetScheduleObserver(t *testing.T) {
	_, cm := newTestStoreAndManager(t)

	// Should not panic with nil observer
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "obs-test",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	// Set observer and verify it's called
	called := false
	obs := &testObserver{onChanged: func(_ *model.ConnectorConfig) { called = true }}
	cm.SetScheduleObserver(obs)

	cfg2 := &model.ConnectorConfig{
		Type: "filesystem", Name: "obs-test2",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true,
	}
	if err := cm.Add(context.Background(), cfg2); err != nil {
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
	_, _, router := newTestRouter(t)

	body := `{"type":"filesystem","name":"bad-sched","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"},"enabled":true,"schedule":"not a cron"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid schedule, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateConnector_WithSchedule(t *testing.T) {
	_, _, router := newTestRouter(t)

	body := `{"type":"filesystem","name":"sched-test","config":{"root_path":"` + t.TempDir() + `","patterns":"*.txt"},"enabled":true,"schedule":"*/30 * * * *"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify schedule is returned in list
	req = httptest.NewRequest(http.MethodGet, "/api/connectors/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	connectors := resp.Data.([]any)
	if len(connectors) != 1 {
		t.Fatalf("expected 1 connector, got %d", len(connectors))
	}
	conn := connectors[0].(map[string]any)
	if conn["schedule"] != "*/30 * * * *" {
		t.Errorf("expected schedule '*/30 * * * *', got %v", conn["schedule"])
	}
}

func TestNewRouter_Integration(t *testing.T) {
	_, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("health: expected 200, got %d", w.Code)
	}
}
