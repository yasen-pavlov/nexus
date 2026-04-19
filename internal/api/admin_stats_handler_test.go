//go:build integration

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"go.uber.org/zap"
)

// adminStatsTestRouter spins up a router with a real store + search client
// and seeds a single admin + a single regular user, then returns both tokens
// so the auth boundary tests can run side-by-side.
func adminStatsTestRouter(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)
	_, userToken := createTestUser(t, st)
	return router, adminToken, userToken
}

func TestGetAdminStats_NonAdminForbidden(t *testing.T) {
	router, _, userToken := adminStatsTestRouter(t)

	w := doJSON(t, router, http.MethodGet, "/api/admin/stats", "", userToken)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestGetAdminStats_RequiresAuth(t *testing.T) {
	router, _, _ := adminStatsTestRouter(t)

	w := doJSON(t, router, http.MethodGet, "/api/admin/stats", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no token expected 401, got %d", w.Code)
	}
}

func TestGetAdminStats_EmptyIndex(t *testing.T) {
	router, adminToken, _ := adminStatsTestRouter(t)

	w := doJSON(t, router, http.MethodGet, "/api/admin/stats", "", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if got := data["total_documents"].(float64); got != 0 {
		t.Errorf("empty index total_documents = %v, want 0", got)
	}
	if got := data["total_chunks"].(float64); got != 0 {
		t.Errorf("empty index total_chunks = %v, want 0", got)
	}
	// two users seeded (admin + user); the admin stats handler must reflect the
	// current seeded state — not hardcoded values — so the test stays green
	// whether the helpers change admin/user ratios.
	if got := data["users_count"].(float64); got < 2 {
		t.Errorf("users_count = %v, expected >= 2", got)
	}
	if perSource, ok := data["per_source"].([]any); !ok {
		t.Errorf("per_source missing or wrong type: %T", data["per_source"])
	} else if len(perSource) != 0 {
		t.Errorf("per_source should be empty on empty index, got %d entries", len(perSource))
	}
	// Embedding + rerank blocks always present (even when disabled) so the
	// UI can render a placeholder card rather than guarding for null.
	if _, ok := data["embedding"].(map[string]any); !ok {
		t.Error("embedding block missing")
	}
	if _, ok := data["rerank"].(map[string]any); !ok {
		t.Error("rerank block missing")
	}
}

// TestGetAdminStats_StoreClosedSurfacesError closes the store pool mid-test
// to force the users-count query to fail, so the handler's error-return path
// gets exercised. Lives alongside the happy-path tests because fault
// injection at the DB layer doesn't fit in a pure unit test.
func TestGetAdminStats_StoreClosedSurfacesError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)

	// Close the pool — subsequent queries fail with "closed pool".
	st.Close()

	w := doJSON(t, router, http.MethodGet, "/api/admin/stats", "", adminToken)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when store fails, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestGetAdminStats_PopulatedIndex(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	em.setActive(&mockEmbedder{dim: 1024}, "voyage", "voyage-3-large")
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)

	ctx := context.Background()
	// Two filesystem docs + one imap doc. Each document becomes a distinct
	// source_id bucket; the chunk aggregation should therefore count three
	// chunks while the per-source breakdown shows two distinct source_ids
	// under filesystem/test and one under imap/inbox.
	docs := []*model.Document{
		{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test",
			SourceID: "one.txt", Title: "One", Content: "alpha alpha",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		},
		{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test",
			SourceID: "two.txt", Title: "Two", Content: "beta beta",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		},
		{
			ID: uuid.New(), SourceType: "imap", SourceName: "inbox",
			SourceID: "msg-1", Title: "Email", Content: "hello",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		},
	}
	for _, d := range docs {
		if err := sc.IndexDocument(ctx, d); err != nil {
			t.Fatalf("index: %v", err)
		}
	}
	if err := sc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, router, http.MethodGet, "/api/admin/stats", "", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)

	if got := data["total_documents"].(float64); got != 3 {
		t.Errorf("total_documents = %v, want 3", got)
	}
	if got := data["total_chunks"].(float64); got != 3 {
		t.Errorf("total_chunks = %v, want 3", got)
	}

	perSource := data["per_source"].([]any)
	if len(perSource) != 2 {
		t.Fatalf("expected 2 per-source buckets, got %d: %+v", len(perSource), perSource)
	}
	byKey := map[string]map[string]any{}
	for _, entry := range perSource {
		m := entry.(map[string]any)
		k := m["source_type"].(string) + "/" + m["source_name"].(string)
		byKey[k] = m
	}
	fs, ok := byKey["filesystem/test"]
	if !ok {
		t.Fatalf("missing filesystem/test bucket; got %v", byKey)
	}
	if got := fs["document_count"].(float64); got != 2 {
		t.Errorf("filesystem/test document_count = %v, want 2", got)
	}
	if got := fs["chunk_count"].(float64); got != 2 {
		t.Errorf("filesystem/test chunk_count = %v, want 2", got)
	}
	if fs["latest_indexed_at"] == nil {
		t.Error("filesystem/test latest_indexed_at missing")
	}

	emb := data["embedding"].(map[string]any)
	if emb["enabled"] != true {
		t.Errorf("embedding.enabled = %v, want true", emb["enabled"])
	}
	if emb["provider"] != "voyage" {
		t.Errorf("embedding.provider = %v, want voyage", emb["provider"])
	}
	if emb["model"] != "voyage-3-large" {
		t.Errorf("embedding.model = %v, want voyage-3-large", emb["model"])
	}
	if got := emb["dimension"].(float64); got != 1024 {
		t.Errorf("embedding.dimension = %v, want 1024", got)
	}

	rr := data["rerank"].(map[string]any)
	if rr["enabled"] != false {
		t.Errorf("rerank.enabled = %v, want false (not configured in this test)", rr["enabled"])
	}
}
