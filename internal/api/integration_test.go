//go:build integration

package api

import (
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
	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func newTestStoreAndPipeline(t *testing.T) (*store.Store, *pipeline.Pipeline) {
	t.Helper()
	tdb := testutil.NewTestDB(t, "api", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	p := pipeline.New(st, zap.NewNop())
	return st, p
}

func TestSearchHandler_Integration(t *testing.T) {
	st, _ := newTestStoreAndPipeline(t)
	ctx := context.Background()

	doc := &model.Document{
		ID:         uuid.New(),
		SourceType: "filesystem",
		SourceName: "test",
		SourceID:   "search-test.txt",
		Title:      "Search Test",
		Content:    "This document contains searchable integration test content",
		Metadata:   map[string]any{"path": "search-test.txt"},
		Visibility: "private",
		CreatedAt:  time.Now(),
	}
	if err := st.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	h := &handler{store: st, log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=searchable", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be object, got %T", resp.Data)
	}
	totalCount, _ := data["total_count"].(float64)
	if totalCount != 1 {
		t.Errorf("expected 1 result, got %v", totalCount)
	}
}

func TestSearchHandler_WithParams(t *testing.T) {
	st, _ := newTestStoreAndPipeline(t)
	ctx := context.Background()

	for i, content := range []string{
		"The searchterm appears in this first document about databases",
		"Another document mentioning searchterm and also discussing servers",
		"A third document with searchterm covering deployment topics",
	} {
		doc := &model.Document{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test",
			SourceID: fmt.Sprintf("param-test-%d.txt", i),
			Title:    fmt.Sprintf("Doc %d", i), Content: content,
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		}
		if err := st.UpsertDocument(ctx, doc); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}
	}

	h := &handler{store: st, log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=searchterm&limit=2", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be map, got %T", resp.Data)
	}
	totalCount, _ := data["total_count"].(float64)
	if totalCount != 3 {
		t.Fatalf("expected total 3, got %v", totalCount)
	}
	docs, _ := data["documents"].([]any)
	if len(docs) != 2 {
		t.Errorf("expected 2 docs with limit=2, got %d", len(docs))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/search?q=searchterm&limit=2&offset=2", nil)
	w = httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp2 APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	data2, ok := resp2.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be map, got %T", resp2.Data)
	}
	docs2, _ := data2["documents"].([]any)
	if len(docs2) != 1 {
		t.Errorf("expected 1 doc with offset=2, got %d", len(docs2))
	}
}

func TestTriggerSyncHandler_Integration(t *testing.T) {
	st, p := newTestStoreAndPipeline(t)

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/test.txt", []byte("sync handler test"), 0o644); err != nil {
		t.Fatal(err)
	}

	fsConn, err := connector.Create("filesystem")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsConn.Configure(connector.Config{
		"name": "handler-test", "root_path": dir, "patterns": "*.txt",
	}); err != nil {
		t.Fatal(err)
	}

	connectors := map[string]connector.Connector{"handler-test": fsConn}
	router := NewRouter(st, p, connectors, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/api/sync/handler-test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be object, got %T", resp.Data)
	}
	docsProcessed, _ := data["docs_processed"].(float64)
	if docsProcessed != 1 {
		t.Errorf("expected 1 doc processed, got %v", docsProcessed)
	}
}

func TestSearchHandler_StoreError(t *testing.T) {
	st, _ := newTestStoreAndPipeline(t)
	st.Close() // close to trigger error path

	h := &handler{store: st, log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestTriggerSyncHandler_StoreError(t *testing.T) {
	st, p := newTestStoreAndPipeline(t)

	dir := t.TempDir()
	os.WriteFile(dir+"/test.txt", []byte("test"), 0o644) //nolint:errcheck // test file

	fsConn, _ := connector.Create("filesystem")
	_ = fsConn.Configure(connector.Config{
		"name": "err-test", "root_path": dir, "patterns": "*.txt",
	})

	st.Close() // close to trigger error in pipeline

	h := &handler{
		store:      st,
		pipeline:   p,
		connectors: map[string]connector.Connector{"err-test": fsConn},
		log:        zap.NewNop(),
	}

	r := chi.NewRouter()
	r.Post("/api/sync/{connector}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/err-test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestNewRouter_Integration(t *testing.T) {
	st, p := newTestStoreAndPipeline(t)
	connectors := map[string]connector.Connector{}
	router := NewRouter(st, p, connectors, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("health: expected 200, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/connectors", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("connectors: expected 200, got %d", w.Code)
	}
}
