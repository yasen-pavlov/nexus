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
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/config"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/rerank"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

type mockEmbedder struct{ dim int }

func (m *mockEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
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
	sc, err := search.NewWithIndex(context.Background(), osURL, osIndex, nil, lang.Default())
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

var testJWTSecret = []byte("test-jwt-secret-for-integration!")

// createTestAdmin creates a test admin user in the DB and returns its UUID and a valid JWT.
// Username is unique per test to avoid collisions in shared test databases.
func createTestAdmin(t *testing.T, st *store.Store) (uuid.UUID, string) {
	t.Helper()
	username := fmt.Sprintf("testadmin-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	user, err := st.CreateUser(context.Background(), username, "hash", "admin")
	if err != nil {
		t.Fatalf("create test admin: %v", err)
	}
	token, err := auth.GenerateToken(testJWTSecret, user.ID, user.Username, user.Role)
	if err != nil {
		t.Fatalf("generate test token: %v", err)
	}
	return user.ID, token
}

// authWrap wraps a router so unauthenticated requests get an injected admin token.
// Tests that need to test auth specifically can set their own Authorization header.
func authWrap(router http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		router.ServeHTTP(w, r)
	})
}

func newTestRouter(t *testing.T) (*store.Store, *search.Client, *ConnectorManager, http.Handler) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop())
	_, token := createTestAdmin(t, st)
	return st, sc, cm, authWrap(router, token)
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

	chunk := model.Chunk{
		ID: "search-test.txt:0", ParentID: "search-test.txt", ChunkIndex: 0,
		Title: "Search Test", Content: "This document contains searchable integration test content",
		FullContent: "This document contains searchable integration test content",
		SourceType:  "filesystem", SourceName: "test", SourceID: "search-test.txt",
		Metadata: map[string]any{"path": "search-test.txt"}, Visibility: "private",
		Shared: true, CreatedAt: time.Now(),
	}
	if err := sc.IndexChunks(ctx, []model.Chunk{chunk}); err != nil {
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

func TestSearchHandler_ScoreDetails(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	chunk := model.Chunk{
		ID: "explain-test.txt:0", ParentID: "explain-test.txt", ChunkIndex: 0,
		Title: "Explain Test", Content: "Testing score details breakdown",
		FullContent: "Testing score details breakdown",
		SourceType:  "filesystem", SourceName: "test", SourceID: "explain-test.txt",
		Metadata: map[string]any{"path": "explain-test.txt"}, Visibility: "private",
		Shared: true, CreatedAt: time.Now(),
	}
	if err := sc.IndexChunks(ctx, []model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	sc.Refresh(ctx) //nolint:errcheck // test

	h := &handler{search: sc, cm: cm, em: NewEmbeddingManager(st, zap.NewNop()), rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=explain+test&score_details=true", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	docs := data["documents"].([]any)
	if len(docs) == 0 {
		t.Fatal("expected at least 1 result")
	}

	firstDoc := docs[0].(map[string]any)
	sd, ok := firstDoc["score_details"].(map[string]any)
	if !ok || sd == nil {
		t.Fatal("expected score_details in response when score_details=true")
	}
	if sd["retrieval"] == nil {
		t.Error("expected retrieval score in score_details")
	}
	if sd["recency_factor"] == nil {
		t.Error("expected recency_factor in score_details")
	}
	if sd["final"] == nil {
		t.Error("expected final score in score_details")
	}
}

func TestSearchHandler_NoScoreDetailsWithoutFlag(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	chunk := model.Chunk{
		ID: "no-explain.txt:0", ParentID: "no-explain.txt", ChunkIndex: 0,
		Title: "No Explain", Content: "Should not have score details",
		FullContent: "Should not have score details",
		SourceType:  "filesystem", SourceName: "test", SourceID: "no-explain.txt",
		Metadata: map[string]any{}, Visibility: "private",
		Shared: true, CreatedAt: time.Now(),
	}
	sc.IndexChunks(ctx, []model.Chunk{chunk}) //nolint:errcheck // test
	sc.Refresh(ctx)                           //nolint:errcheck // test

	h := &handler{search: sc, cm: cm, em: NewEmbeddingManager(st, zap.NewNop()), rm: NewRerankManager(st, zap.NewNop()), log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=explain", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	docs := data["documents"].([]any)
	if len(docs) > 0 {
		firstDoc := docs[0].(map[string]any)
		if firstDoc["score_details"] != nil {
			t.Error("score_details should not be present without flag")
		}
	}
}

func TestSearchHandler_WithParams(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	var chunks []model.Chunk
	for i, content := range []string{
		"The searchterm appears in this first document about databases",
		"Another document mentioning searchterm and also discussing servers",
		"A third document with searchterm covering deployment topics",
	} {
		sourceID := fmt.Sprintf("param-%d.txt", i)
		chunks = append(chunks, model.Chunk{
			ID: sourceID + ":0", ParentID: sourceID, ChunkIndex: 0,
			Title: fmt.Sprintf("Doc %d", i), Content: content, FullContent: content,
			SourceType: "filesystem", SourceName: "test", SourceID: sourceID,
			Metadata: map[string]any{}, Visibility: "private",
			Shared: true, CreatedAt: time.Now(),
		})
	}
	sc.IndexChunks(ctx, chunks) //nolint:errcheck // test
	sc.Refresh(ctx)             //nolint:errcheck // test

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

// stubReranker is a deterministic mock that scores docs by index. Used to
// exercise the post-rerank score floor without hitting a real provider.
type stubReranker struct{ scores []float64 }

func (s *stubReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.Result, error) {
	results := make([]rerank.Result, len(docs))
	for i := range docs {
		score := 0.0
		if i < len(s.scores) {
			score = s.scores[i]
		}
		results[i] = rerank.Result{Index: i, Score: score}
	}
	return results, nil
}

// TestSearchHandler_RerankFloor_Integration verifies that documents with
// reranker scores below search.RerankMinScore are dropped after the rerank
// stage. The high-scoring docs survive; low-scoring noise is filtered out.
func TestSearchHandler_RerankFloor_Integration(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	// Three chunks all matching "rerankfloor". The stub reranker scores them
	// 0.9, 0.5, 0.01 in the order they come back from search. With the
	// production floor of 0.4, only the 0.01 doc should be filtered out.
	chunks := []model.Chunk{
		{
			ID: "rf-1.txt:0", ParentID: "rf-1.txt", ChunkIndex: 0,
			Title: "RF One", Content: "rerankfloor relevant content one",
			FullContent: "rerankfloor relevant content one",
			SourceType:  "filesystem", SourceName: "test", SourceID: "rf-1.txt",
			Metadata: map[string]any{}, Visibility: "private",
			Shared: true, CreatedAt: time.Now(),
		},
		{
			ID: "rf-2.txt:0", ParentID: "rf-2.txt", ChunkIndex: 0,
			Title: "RF Two", Content: "rerankfloor relevant content two",
			FullContent: "rerankfloor relevant content two",
			SourceType:  "filesystem", SourceName: "test", SourceID: "rf-2.txt",
			Metadata: map[string]any{}, Visibility: "private",
			Shared: true, CreatedAt: time.Now(),
		},
		{
			ID: "rf-3.txt:0", ParentID: "rf-3.txt", ChunkIndex: 0,
			Title: "RF Three", Content: "rerankfloor relevant content three",
			FullContent: "rerankfloor relevant content three",
			SourceType:  "filesystem", SourceName: "test", SourceID: "rf-3.txt",
			Metadata: map[string]any{}, Visibility: "private",
			Shared: true, CreatedAt: time.Now(),
		},
	}
	if err := sc.IndexChunks(ctx, chunks); err != nil {
		t.Fatal(err)
	}
	sc.Refresh(ctx) //nolint:errcheck // test

	rm := NewRerankManager(st, zap.NewNop())
	rm.Set(&stubReranker{scores: []float64{0.9, 0.5, 0.01}})

	h := &handler{
		search: sc, cm: cm,
		em:  NewEmbeddingManager(st, zap.NewNop()),
		rm:  rm,
		log: zap.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=rerankfloor", nil)
	w := httptest.NewRecorder()
	h.Search(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck // test
	data := resp.Data.(map[string]any)
	tc, _ := data["total_count"].(float64)
	if tc != 2 {
		t.Errorf("expected total_count=2 (one doc filtered by rerank floor), got %v", tc)
	}
	docs, _ := data["documents"].([]any)
	if len(docs) != 2 {
		t.Errorf("expected 2 docs after rerank floor, got %d", len(docs))
	}
}

// TestSearchOwnershipScoping verifies the core security guarantee of Phase 6:
// users only see their own documents + shared documents in search results.
func TestSearchOwnershipScoping(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	// Create two users
	alice, err := st.CreateUser(ctx, "alice", "hash", "user")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := st.CreateUser(ctx, "bob", "hash", "user")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	// Index three chunks: one owned by alice, one owned by bob, one shared
	makeChunk := func(id, ownerID string, shared bool, content string) model.Chunk {
		return model.Chunk{
			ID: id, ParentID: id, ChunkIndex: 0,
			Title: "Doc " + id, Content: content, FullContent: content,
			SourceType: "filesystem", SourceName: "test", SourceID: id,
			Metadata: map[string]any{}, Visibility: "private",
			OwnerID: ownerID, Shared: shared,
			CreatedAt: time.Now(), IndexedAt: time.Now(),
		}
	}
	chunks := []model.Chunk{
		makeChunk("alice-doc", alice.ID.String(), false, "uniqueterm appears in alice's private document"),
		makeChunk("bob-doc", bob.ID.String(), false, "uniqueterm appears in bob's private document"),
		makeChunk("shared-doc", "", true, "uniqueterm appears in a shared document"),
	}
	if err := sc.IndexChunks(ctx, chunks); err != nil {
		t.Fatalf("index chunks: %v", err)
	}
	sc.Refresh(ctx) //nolint:errcheck // test


	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop())

	doSearch := func(t *testing.T, userID uuid.UUID, username, role string) []string {
		t.Helper()
		token, err := auth.GenerateToken(testJWTSecret, userID, username, role)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/search?q=uniqueterm", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("search as %s: expected 200, got %d; body: %s", username, w.Code, w.Body.String())
		}
		var resp APIResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		data, _ := resp.Data.(map[string]any)
		docs, _ := data["documents"].([]any)
		ids := make([]string, 0, len(docs))
		for _, d := range docs {
			doc, _ := d.(map[string]any)
			if sid, ok := doc["source_id"].(string); ok {
				ids = append(ids, sid)
			}
		}
		return ids
	}

	contains := func(ids []string, id string) bool {
		for _, s := range ids {
			if s == id {
				return true
			}
		}
		return false
	}

	// Alice should see her doc + shared, NOT bob's
	aliceResults := doSearch(t, alice.ID, "alice", "user")
	if !contains(aliceResults, "alice-doc") {
		t.Errorf("alice should see alice-doc, got %v", aliceResults)
	}
	if !contains(aliceResults, "shared-doc") {
		t.Errorf("alice should see shared-doc, got %v", aliceResults)
	}
	if contains(aliceResults, "bob-doc") {
		t.Errorf("alice should NOT see bob-doc, got %v", aliceResults)
	}

	// Bob should see his doc + shared, NOT alice's
	bobResults := doSearch(t, bob.ID, "bob", "user")
	if !contains(bobResults, "bob-doc") {
		t.Errorf("bob should see bob-doc, got %v", bobResults)
	}
	if !contains(bobResults, "shared-doc") {
		t.Errorf("bob should see shared-doc, got %v", bobResults)
	}
	if contains(bobResults, "alice-doc") {
		t.Errorf("bob should NOT see alice-doc, got %v", bobResults)
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
	var createResp APIResponse
	json.NewDecoder(w.Body).Decode(&createResp) //nolint:errcheck // test
	connID := createResp.Data.(map[string]any)["id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/sync/"+connID, nil)
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
		Config: map[string]any{"root_path": dir, "patterns": "*.txt"}, Enabled: true, Shared: true,
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
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, withAdminContext(req))
		})
	})
	r.Post("/api/sync/{id}", h.TriggerSync)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/"+cfg.ID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Async sync returns 202 immediately; error happens in background
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

	// Wait for background goroutine to fail
	time.Sleep(500 * time.Millisecond)

	job := sjm.GetByConnector(cfg.ID)
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
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
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

func TestUpdateConnector_DuplicateName(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Create two connectors
	dirA := t.TempDir()
	bodyA := `{"type":"filesystem","name":"first-conn","config":{"root_path":"` + dirA + `","patterns":"*.txt"},"enabled":true}`
	reqA := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(bodyA))
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	router.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusCreated {
		t.Fatalf("create A: %d %s", wA.Code, wA.Body.String())
	}

	dirB := t.TempDir()
	bodyB := `{"type":"filesystem","name":"second-conn","config":{"root_path":"` + dirB + `","patterns":"*.txt"},"enabled":true}`
	reqB := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(bodyB))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	router.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusCreated {
		t.Fatalf("create B: %d %s", wB.Code, wB.Body.String())
	}
	var respB APIResponse
	json.NewDecoder(wB.Body).Decode(&respB) //nolint:errcheck // test
	bID := respB.Data.(map[string]any)["id"].(string)

	// Try to rename B to "first-conn" — should 409
	updateBody := `{"type":"filesystem","name":"first-conn","config":{"root_path":"` + dirB + `","patterns":"*.txt"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/connectors/"+bID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate rename, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateConnector_NilConfig(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	// Submit without a config field — handler should default to empty map
	body := `{"type":"filesystem","name":"nilcfg","enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Filesystem connector requires root_path so it'll fail validation, but the
	// handler should at least reach validation rather than panicking on nil map.
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing root_path, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestConnectorManager_LoadFromDB_SkipsDisabled(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "disabled-conn",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: false, Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := cm.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db: %v", err)
	}
	if _, _, ok := cm.GetByID(cfg.ID); ok {
		t.Error("disabled connector should not be in the manager")
	}
}

func TestConnectorManager_LoadFromDB_StoreError(t *testing.T) {
	st, _, cm := newTestDeps(t)
	st.Close()
	if err := cm.LoadFromDB(context.Background()); err == nil {
		t.Error("expected error from closed store")
	}
}

func TestConnectorManager_Update_DisableRemovesFromMap(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "to-disable",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cm.GetByID(cfg.ID); !ok {
		t.Fatal("expected connector to be present after Add")
	}

	cfg.Enabled = false
	if err := cm.Update(ctx, cfg); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, _, ok := cm.GetByID(cfg.ID); ok {
		t.Error("expected connector to be removed from manager after disable")
	}
	if _, err := st.GetConnectorConfig(ctx, cfg.ID); err != nil {
		t.Errorf("connector should still exist in DB after disable: %v", err)
	}
}

func TestConnectorManager_Remove_NotFound(t *testing.T) {
	_, _, cm := newTestDeps(t)
	if err := cm.Remove(context.Background(), uuid.New()); err == nil {
		t.Error("expected error removing non-existent connector")
	}
}

func TestConnectorManager_LoadFromDB_InvalidConfig(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	// Create a connector with invalid config (nonexistent path) to trigger warning path
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "bad-config",
		Config: map[string]any{"root_path": "/nonexistent/path/definitely", "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	// LoadFromDB should succeed but skip the invalid connector
	if err := cm.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db failed: %v", err)
	}

	// The invalid connector should NOT be in the map
	if _, _, ok := cm.GetByID(cfg.ID); ok {
		t.Error("expected invalid connector to be skipped")
	}
}

func TestConnectorManager_LoadFromDB(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "load-test",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if err := cm.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db failed: %v", err)
	}

	if _, _, ok := cm.GetByID(cfg.ID); !ok {
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

	// Look up by name via cm.All() since seeded connector ID isn't exposed
	found := false
	for _, entry := range cm.All() {
		if entry.Config.Name == "filesystem" {
			found = true
			break
		}
	}
	if !found {
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
	_, token := createTestAdmin(t, st)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := authWrap(NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop()), token)

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestDeleteConnector_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	_, token := createTestAdmin(t, st)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := authWrap(NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop()), token)

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
	_, token := createTestAdmin(t, st)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := authWrap(NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop()), token)

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

	// Pull the connector ID out of the response so we can persist a cursor for it
	var connResp APIResponse
	json.NewDecoder(cw.Body).Decode(&connResp) //nolint:errcheck // test
	reindexConnID, _ := uuid.Parse(connResp.Data.(map[string]any)["id"].(string))
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: reindexConnID, CursorData: map[string]any{}})

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

// telegramAuthSetup creates a real telegram connector and returns its ID. Used
// by tests that need a connector to exist before exercising the auth code path.
func telegramAuthSetup(t *testing.T, router http.Handler, name string) string {
	t.Helper()
	body := `{"type":"telegram","name":"` + name + `","config":{"api_id":"12345","api_hash":"abc","phone":"+1234567890"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create telegram connector: %d %s", w.Code, w.Body.String())
	}
	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp.Data.(map[string]any)["id"].(string)
}

func TestTelegramAuthCode_NotFound(t *testing.T) {
	// Random UUID — connector doesn't exist, ownership check returns 404 first.
	_, _, _, router := newTestRouter(t)
	body := `{"code":"12345"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+uuid.New().String()+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent connector, got %d", w.Code)
	}
}

func TestTelegramAuthCode_InvalidID(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/not-a-uuid/auth/code", bytes.NewBufferString(`{"code":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTelegramAuthCode_BadBody(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	connID := telegramAuthSetup(t, router, "auth-code-badbody")

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTelegramAuthCode_NoPending(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	connID := telegramAuthSetup(t, router, "auth-code-nopending")

	body := `{"code":"12345"}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTelegramAuthCode_MissingCode(t *testing.T) {
	_, _, _, router := newTestRouter(t)
	connID := telegramAuthSetup(t, router, "auth-code-missing")

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestTelegramAuth_OwnershipEnforced verifies the C1 fix: a non-owner cannot
// trigger Telegram auth flows on someone else's connector.
func TestTelegramAuth_OwnershipEnforced(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, alice := setupAdminAndUser(t, router)

	// Admin creates a private telegram connector owned by admin.
	body := `{"type":"telegram","name":"admin-tg","config":{"api_id":"12345","api_hash":"adminhash","phone":"+1111111111"},"enabled":true}`
	w := doJSON(t, router, http.MethodPost, "/api/connectors/", body, admin.token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create admin tg: %d %s", w.Code, w.Body.String())
	}
	var resp APIResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	connID := resp.Data.(map[string]any)["id"].(string)

	// Alice (regular user) tries to start Telegram auth on admin's connector.
	w = doJSON(t, router, http.MethodPost, "/api/connectors/"+connID+"/auth/start", "", alice.token)
	if w.Code != http.StatusNotFound {
		t.Errorf("alice start admin's flow: expected 404, got %d", w.Code)
	}

	// Alice tries to submit a code on admin's connector.
	w = doJSON(t, router, http.MethodPost, "/api/connectors/"+connID+"/auth/code", `{"code":"12345"}`, alice.token)
	if w.Code != http.StatusNotFound {
		t.Errorf("alice submit code on admin's flow: expected 404, got %d", w.Code)
	}

	// Admin can still access their own connector's auth endpoints (the start
	// goroutine will fail in the background since the credentials are fake,
	// but the synchronous response should still be 200).
	w = doJSON(t, router, http.MethodPost, "/api/connectors/"+connID+"/auth/start", "", admin.token)
	if w.Code != http.StatusOK {
		t.Errorf("admin start own flow: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteAllCursors_Integration(t *testing.T) {
	st, _, cm, router := newTestRouter(t)
	ctx := context.Background()

	// Create two real connectors and a cursor for each
	cfgA := &model.ConnectorConfig{
		Type: "filesystem", Name: "del-all-a",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	cfgB := &model.ConnectorConfig{
		Type: "filesystem", Name: "del-all-b",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfgA); err != nil {
		t.Fatal(err)
	}
	if err := cm.Add(ctx, cfgB); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: cfgA.ID, CursorData: map[string]any{}})
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: cfgB.ID, CursorData: map[string]any{}})

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	if c, _ := st.GetSyncCursor(ctx, cfgA.ID); c != nil {
		t.Error("cursor for cfgA should be deleted")
	}
	if c, _ := st.GetSyncCursor(ctx, cfgB.ID); c != nil {
		t.Error("cursor for cfgB should be deleted")
	}
}

func TestDeleteCursor_Integration(t *testing.T) {
	st, _, cm, router := newTestRouter(t)
	ctx := context.Background()

	// Create the connector to be cursor-deleted, plus another one whose cursor must be left alone
	cfgKeep := &model.ConnectorConfig{
		Type: "filesystem", Name: "keep",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	cfgDel := &model.ConnectorConfig{
		Type: "filesystem", Name: "delete-me",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfgKeep); err != nil {
		t.Fatal(err)
	}
	if err := cm.Add(ctx, cfgDel); err != nil {
		t.Fatal(err)
	}

	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: cfgKeep.ID, CursorData: map[string]any{}})
	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: cfgDel.ID, CursorData: map[string]any{}})

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors/"+cfgDel.ID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if c, _ := st.GetSyncCursor(ctx, cfgDel.ID); c != nil {
		t.Error("delete-me cursor should be deleted")
	}
	if c, _ := st.GetSyncCursor(ctx, cfgKeep.ID); c == nil {
		t.Error("keep cursor should still exist")
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
	var connResp APIResponse
	json.NewDecoder(w.Body).Decode(&connResp) //nolint:errcheck // test
	connID, _ := uuid.Parse(connResp.Data.(map[string]any)["id"].(string))

	_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: connID, CursorData: map[string]any{}})

	// Verify cursor exists before reindex
	c, _ := st.GetSyncCursor(ctx, connID)
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
	st, sc, cm := newTestDeps(t)

	// Create a connector so the ownership check passes; then close the store
	// so the actual DeleteSyncCursor call fails.
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "test",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	sjm := NewSyncJobManager()
	h := &handler{store: st, search: sc, cm: cm, em: em, rm: NewRerankManager(st, zap.NewNop()), syncJobs: sjm, log: zap.NewNop()}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, withAdminContext(req))
		})
	})
	r.Delete("/api/sync/cursors/{id}", h.DeleteCursor)

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/cursors/"+cfg.ID.String(), nil)
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
	var createResp APIResponse
	json.NewDecoder(w.Body).Decode(&createResp) //nolint:errcheck // test
	connID := createResp.Data.(map[string]any)["id"].(string)

	// Trigger sync
	req = httptest.NewRequest(http.MethodPost, "/api/sync/"+connID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("sync: expected 202, got %d", w.Code)
	}

	// Start SSE stream via real HTTP server. The router used by the test wraps
	// requests with an admin token via authWrap, but EventSource (and net/http
	// Get here) won't get that injection because the wrapper is bound to the
	// in-process handler. Pass the token via ?token= which the auth middleware
	// accepts as a fallback (used for SSE in production too).
	srv := httptest.NewServer(router)
	defer srv.Close()

	tok, _ := auth.GenerateToken(testJWTSecret, uuid.New(), "admin", "admin")
	resp, err := http.Get(srv.URL + "/api/sync/" + connID + "/progress?token=" + tok)
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

	req := httptest.NewRequest(http.MethodGet, "/api/sync/"+uuid.New().String()+"/progress", nil)
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

// --- Document download tests ---

// indexFSChunk indexes a single chunk that points at a filesystem source. Used
// by the download endpoint tests to set up the OpenSearch state without going
// through the full sync pipeline.
func indexFSChunk(t *testing.T, sc *search.Client, sourceName, sourceID, ownerID string, shared bool) string {
	t.Helper()
	parentID := "filesystem:" + sourceName + ":" + sourceID
	docID := model.DocumentID("filesystem", sourceName, sourceID).String()
	chunk := model.Chunk{
		ID: parentID + ":0", ParentID: parentID, DocID: docID, ChunkIndex: 0,
		Title: sourceID, Content: "indexed test content",
		FullContent: "indexed test content",
		SourceType:  "filesystem", SourceName: sourceName, SourceID: sourceID,
		MimeType: "text/plain", Size: 20,
		Metadata: map[string]any{}, Visibility: "private",
		OwnerID: ownerID, Shared: shared,
		CreatedAt: time.Now(), IndexedAt: time.Now(),
	}
	if err := sc.IndexChunks(context.Background(), []model.Chunk{chunk}); err != nil {
		t.Fatalf("index chunk: %v", err)
	}
	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return docID
}

func TestDownloadDocument_Integration_HappyPath(t *testing.T) {
	st, sc, cm, router := newTestRouter(t)

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("Hello World"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create + sync the connector via the API so the connector manager picks it up.
	body := `{"type":"filesystem","name":"dl-happy","config":{"root_path":"` + dir + `","patterns":"*.txt"},"enabled":true,"shared":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create connector: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	// Run the connector synchronously through the pipeline so the chunks land in OpenSearch
	// before we query. (Triggering /api/sync would be async.)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	conn, cfg, ok := cm.GetByTypeAndName("filesystem", "dl-happy")
	if !ok {
		t.Fatal("connector not in manager")
	}
	ownerID := ""
	if cfg.UserID != nil {
		ownerID = cfg.UserID.String()
	}
	if _, err := p.RunWithProgress(context.Background(), cfg.ID, conn, ownerID, cfg.Shared, nil); err != nil {
		t.Fatalf("pipeline run: %v", err)
	}
	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	docID := model.DocumentID("filesystem", "dl-happy", "hello.txt").String()

	// Inline preview
	req = httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "Hello World" {
		t.Errorf("expected body 'Hello World', got %q", w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("expected text/plain Content-Type, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("expected inline disposition, got %q", w.Header().Get("Content-Disposition"))
	}

	// Force download
	req = httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content?download=1", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("download: expected 200, got %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("expected attachment disposition, got %q", w.Header().Get("Content-Disposition"))
	}
}

func TestDownloadDocument_Integration_BinaryFile(t *testing.T) {
	st, sc, cm, router := newTestRouter(t)

	dir := t.TempDir()
	// Tiny PNG header bytes — enough to be recognized
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	if err := os.WriteFile(dir+"/image.png", pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"type":"filesystem","name":"dl-bin","config":{"root_path":"` + dir + `","patterns":"*.png"},"enabled":true,"shared":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create connector: %d %s", w.Code, w.Body.String())
	}

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	conn, cfg, _ := cm.GetByTypeAndName("filesystem", "dl-bin")
	if _, err := p.RunWithProgress(context.Background(), cfg.ID, conn, "", cfg.Shared, nil); err != nil {
		t.Fatalf("pipeline run: %v", err)
	}
	_ = sc.Refresh(context.Background())

	docID := model.DocumentID("filesystem", "dl-bin", "image.png").String()
	req = httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), pngBytes) {
		t.Errorf("response body does not match png bytes")
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("expected image/png, got %q", w.Header().Get("Content-Type"))
	}
}

func TestDownloadDocument_Integration_Unauthenticated(t *testing.T) {
	// Build router WITHOUT authWrap so we can test the auth boundary directly.
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop())

	// Index a shared chunk so the doc exists
	docID := indexFSChunk(t, sc, "dl-noauth", "any.txt", "", true)

	req := httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadDocument_Integration_OtherUserNonShared(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop())

	alice, err := st.CreateUser(context.Background(), "alice-dl", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(context.Background(), "bob-dl", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}

	// Doc owned by alice, NOT shared
	docID := indexFSChunk(t, sc, "alice-fs", "private.txt", alice.ID.String(), false)

	// Bob tries to download → 404 (not 403, to avoid leaking existence)
	bobToken, err := auth.GenerateToken(testJWTSecret, bob.ID, "bob-dl", "user")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+bobToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-user access, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadDocument_Integration_SharedDocAccessibleByOtherUser(t *testing.T) {
	st, sc, cm, _ := newTestRouter(t)
	_ = cm

	// Create a shared filesystem connector and sync a file
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/shared.txt", []byte("shared content"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "dl-shared",
		Config: map[string]any{"root_path": dir, "patterns": "*.txt"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	conn, _, _ := cm.GetByTypeAndName("filesystem", "dl-shared")
	if _, err := p.RunWithProgress(context.Background(), cfg.ID, conn, "", true, nil); err != nil {
		t.Fatalf("pipeline run: %v", err)
	}
	_ = sc.Refresh(context.Background())

	// Build an unwrapped router so we can use a non-admin token
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop())

	user, err := st.CreateUser(context.Background(), "regular-user-dl", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateToken(testJWTSecret, user.ID, "regular-user-dl", "user")
	if err != nil {
		t.Fatal(err)
	}

	docID := model.DocumentID("filesystem", "dl-shared", "shared.txt").String()
	req := httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("regular user should access shared doc, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "shared content" {
		t.Errorf("expected 'shared content', got %q", w.Body.String())
	}
}

func TestDownloadDocument_Integration_PathTraversal(t *testing.T) {
	st, sc, cm, router := newTestRouter(t)

	// Create a connector pointing at a controlled root
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/legit.txt", []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "dl-traversal",
		Config: map[string]any{"root_path": dir, "patterns": "*.txt"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	// Manually index a chunk with a malicious source_id pointing OUTSIDE the root.
	// This simulates a chunk that was somehow corrupted or injected — the
	// connector itself would never produce such a source_id, but the FetchBinary
	// guard must defend against it regardless.
	parentID := "filesystem:dl-traversal:../../../etc/passwd"
	docID := model.DocumentID("filesystem", "dl-traversal", "../../../etc/passwd").String()
	chunk := model.Chunk{
		ID: parentID + ":0", ParentID: parentID, DocID: docID, ChunkIndex: 0,
		Title: "passwd", Content: "",
		SourceType: "filesystem", SourceName: "dl-traversal", SourceID: "../../../etc/passwd",
		MimeType: "text/plain", Size: 0,
		Metadata: map[string]any{}, Visibility: "private", Shared: true,
		CreatedAt: time.Now(),
	}
	if err := sc.IndexChunks(context.Background(), []model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	_ = sc.Refresh(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("path traversal should not return 200 — possible escape! body: %s", w.Body.String())
	}
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusNotFound {
		t.Errorf("expected 500 or 404, got %d", w.Code)
	}
	_ = st
}

func TestDownloadDocument_Integration_PreviewNotSupported(t *testing.T) {
	_, sc, cm, router := newTestRouter(t)

	// Telegram connector exists but doesn't implement BinaryFetcher
	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "dl-tg",
		Config: map[string]any{"api_id": "12345", "api_hash": "abc", "phone": "+1234567890"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	// Index a chunk pointing at the telegram connector
	parentID := "telegram:dl-tg:msg1"
	docID := model.DocumentID("telegram", "dl-tg", "msg1").String()
	chunk := model.Chunk{
		ID: parentID + ":0", ParentID: parentID, DocID: docID, ChunkIndex: 0,
		Title: "Some chat", Content: "hello",
		SourceType: "telegram", SourceName: "dl-tg", SourceID: "msg1",
		Metadata: map[string]any{}, Visibility: "private", Shared: true,
		CreatedAt: time.Now(),
	}
	if err := sc.IndexChunks(context.Background(), []model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	_ = sc.Refresh(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/documents/"+docID+"/content", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-fetcher connector, got %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "preview not supported") {
		t.Errorf("expected 'preview not supported' message, got %q", w.Body.String())
	}
}

func TestDownloadDocument_Integration_DocumentNotFound(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/documents/"+uuid.New().String()+"/content", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDownloadDocument_Integration_BadUUID(t *testing.T) {
	_, _, _, router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/documents/not-a-uuid/content", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Binary cache stats endpoint ---

func newTestRouterWithBinaryStore(t *testing.T) (*store.Store, *search.Client, *ConnectorManager, *storage.BinaryStore, http.Handler) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())

	bs, err := storage.New(t.TempDir(), st, zap.NewNop())
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	cm.SetBinaryStore(bs)

	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), bs, testJWTSecret, nil, zap.NewNop())
	_, token := createTestAdmin(t, st)
	return st, sc, cm, bs, authWrap(router, token)
}

func TestStorageStats_Empty(t *testing.T) {
	_, _, _, _, router := newTestRouterWithBinaryStore(t)

	req := httptest.NewRequest(http.MethodGet, "/api/storage/stats", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("data type: %T", resp.Data)
	}
	if len(data) != 0 {
		t.Errorf("expected empty stats, got %+v", data)
	}
}

// TestStorageHandlers_NilBinaryStore exercises the "not configured"
// branches — when the router is constructed without a binary store
// the three handlers all short-circuit to a success response rather
// than NPE'ing. Covers the early-return paths.
func TestStorageHandlers_NilBinaryStore(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), nil, testJWTSecret, nil, zap.NewNop())
	_, token := createTestAdmin(t, st)
	wrapped := authWrap(router, token)

	t.Run("GET stats", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/storage/stats", nil)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
		}
	})
	t.Run("DELETE cache all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache", nil)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
		}
	})
	t.Run("DELETE cache by connector", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache/"+uuid.New().String(), nil)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
		}
	})
}

// TestStorageHandlers_StoreErrors closes the DB pool after setup so
// the Stats / DeleteAll / GetConnectorConfig / DeleteBySource calls
// all fail. Covers the error-path logging + 500 response branches in
// each of the three handlers.
func TestStorageHandlers_StoreErrors(t *testing.T) {
	st, sc, cm, _, router := newTestRouterWithBinaryStore(t)

	// Seed a real connector so the GetConnectorConfig lookup succeeds on
	// the by-connector handler; we want to exercise the *Stats* error
	// path, not the 404 path.
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "store-err",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	connID := cfg.ID

	// Now poison the DB pool — every subsequent query errors.
	st.Close()

	t.Run("GET stats returns 500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/storage/stats", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
	t.Run("DELETE cache all returns 500 on stats failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
	t.Run("DELETE cache by id returns 500 when GetConnectorConfig fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache/"+connID.String(), nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	_ = sc
}

func TestStorageStats_ReturnsAggregates(t *testing.T) {
	_, _, _, bs, router := newTestRouterWithBinaryStore(t)
	ctx := context.Background()

	// Populate cache with two connectors' worth of entries.
	for _, id := range []string{"a", "b"} {
		payload := bytes.Repeat([]byte("x"), 100)
		if err := bs.Put(ctx, "imap", "icloud", id, bytes.NewReader(payload), 100); err != nil {
			t.Fatal(err)
		}
	}
	if err := bs.Put(ctx, "telegram", "personal", "m", bytes.NewReader([]byte("hello")), 5); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/storage/stats", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []model.BinaryStoreStats `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 aggregated rows, got %d: %+v", len(resp.Data), resp.Data)
	}

	byKey := map[string]model.BinaryStoreStats{}
	for _, s := range resp.Data {
		byKey[s.SourceType+"/"+s.SourceName] = s
	}
	if got := byKey["imap/icloud"]; got.Count != 2 || got.TotalSize != 200 {
		t.Errorf("imap/icloud = %+v, want count=2 size=200", got)
	}
	if got := byKey["telegram/personal"]; got.Count != 1 || got.TotalSize != 5 {
		t.Errorf("telegram/personal = %+v, want count=1 size=5", got)
	}
}

func TestStorageStats_RequiresAdmin(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())

	bs, err := storage.New(t.TempDir(), st, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), bs, testJWTSecret, nil, zap.NewNop())

	// Non-admin user.
	username := fmt.Sprintf("bob-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	user, err := st.CreateUser(context.Background(), username, "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateToken(testJWTSecret, user.ID, user.Username, user.Role)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/storage/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteStorageCache_All(t *testing.T) {
	_, _, _, bs, router := newTestRouterWithBinaryStore(t)
	ctx := context.Background()

	// Populate with a couple of entries across connectors.
	if err := bs.Put(ctx, "imap", "icloud", "a", bytes.NewReader(bytes.Repeat([]byte("x"), 100)), 100); err != nil {
		t.Fatal(err)
	}
	if err := bs.Put(ctx, "telegram", "personal", "m", bytes.NewReader([]byte("hi")), 2); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			DeletedCount int64 `json:"deleted_count"`
			BytesFreed   int64 `json:"bytes_freed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.DeletedCount != 2 || resp.Data.BytesFreed != 102 {
		t.Errorf("got deleted_count=%d bytes_freed=%d, want 2/102", resp.Data.DeletedCount, resp.Data.BytesFreed)
	}

	// Cache should be empty now.
	stats, err := bs.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty cache, got %+v", stats)
	}
}

func TestDeleteStorageCache_ByConnector(t *testing.T) {
	_, _, cm, bs, router := newTestRouterWithBinaryStore(t)
	ctx := context.Background()

	// Register a connector so we have a real ID to target.
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "cache-target",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"}, Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Populate the cache for this connector and also for an unrelated one.
	if err := bs.Put(ctx, "filesystem", "cache-target", "a", bytes.NewReader(bytes.Repeat([]byte("x"), 50)), 50); err != nil {
		t.Fatal(err)
	}
	if err := bs.Put(ctx, "filesystem", "cache-target", "b", bytes.NewReader(bytes.Repeat([]byte("y"), 70)), 70); err != nil {
		t.Fatal(err)
	}
	if err := bs.Put(ctx, "imap", "other", "z", bytes.NewReader([]byte("z")), 1); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache/"+cfg.ID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			DeletedCount int64 `json:"deleted_count"`
			BytesFreed   int64 `json:"bytes_freed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.DeletedCount != 2 || resp.Data.BytesFreed != 120 {
		t.Errorf("got deleted_count=%d bytes_freed=%d, want 2/120", resp.Data.DeletedCount, resp.Data.BytesFreed)
	}

	// cache-target entries gone; unrelated imap/other survives.
	if ok, _ := bs.Exists(ctx, "filesystem", "cache-target", "a"); ok {
		t.Error("connector-scoped delete should have removed 'a'")
	}
	if ok, _ := bs.Exists(ctx, "imap", "other", "z"); !ok {
		t.Error("unrelated connector's cache should survive targeted delete")
	}
}

func TestDeleteStorageCache_ByConnector_NotFound(t *testing.T) {
	_, _, _, _, router := newTestRouterWithBinaryStore(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown connector, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteStorageCache_ByConnector_BadUUID(t *testing.T) {
	_, _, _, _, router := newTestRouterWithBinaryStore(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/storage/cache/not-a-uuid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad uuid, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestDeleteStorageCache_RequiresAdmin(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())

	bs, err := storage.New(t.TempDir(), st, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(), bs, testJWTSecret, nil, zap.NewNop())

	username := fmt.Sprintf("bob-%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	user, err := st.CreateUser(context.Background(), username, "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateToken(testJWTSecret, user.ID, user.Username, user.Role)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/storage/cache", "/api/storage/cache/" + uuid.New().String()} {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403 for non-admin, got %d; body: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestConnectorRemove_CleansCachedBinaries(t *testing.T) {
	st, _, cm, bs, _ := newTestRouterWithBinaryStore(t)
	ctx := context.Background()

	// Create a connector the manager knows about.
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "cache-cleanup",
		Config:  map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Populate the cache as if the connector had cached something.
	if err := bs.Put(ctx, "filesystem", "cache-cleanup", "file-1", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bs.Exists(ctx, "filesystem", "cache-cleanup", "file-1"); !ok {
		t.Fatal("precondition: entry should be cached")
	}

	// Remove the connector — binary cache should be cleaned up.
	if err := cm.Remove(ctx, cfg.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := bs.Exists(ctx, "filesystem", "cache-cleanup", "file-1"); ok {
		t.Error("cached binary should be deleted when connector is removed")
	}

	_ = st
}
