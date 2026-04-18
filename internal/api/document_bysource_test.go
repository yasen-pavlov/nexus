//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

// indexChunkWithOwner writes a single chunk to the search index under a
// specific owner_id/shared combo so auth-scoping assertions can verify
// the owner filter fires.
func indexChunkWithOwner(t *testing.T, sc searchIndexer, ownerID string, shared bool, sourceType, sourceID, content string) {
	t.Helper()
	ctx := context.Background()
	chunk := model.Chunk{
		ID:          sourceID + ":0",
		ParentID:    sourceID,
		ChunkIndex:  0,
		Title:       sourceID,
		Content:     content,
		FullContent: content,
		SourceType:  sourceType,
		SourceName:  "test",
		SourceID:    sourceID,
		Visibility:  "private",
		OwnerID:     ownerID,
		Shared:      shared,
		CreatedAt:   time.Now(),
	}
	if err := sc.IndexChunks(ctx, []model.Chunk{chunk}); err != nil {
		t.Fatalf("index chunk: %v", err)
	}
	if err := sc.Refresh(ctx); err != nil {
		t.Fatalf("refresh index: %v", err)
	}
}

// searchIndexer is a minimal slice of *search.Client to keep the test
// helper decoupled from the concrete implementation.
type searchIndexer interface {
	IndexChunks(ctx context.Context, chunks []model.Chunk) error
	Refresh(ctx context.Context) error
}

func TestGetDocumentBySource_ReturnsOwnedDoc(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	indexChunkWithOwner(t, sc, userID.String(), false, "telegram", "12345:42:msg", "hello")

	w := doJSON(t, router, http.MethodGet,
		"/api/documents/by-source?source_type=telegram&source_id=12345:42:msg",
		"", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var doc model.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	if doc.SourceID != "12345:42:msg" {
		t.Errorf("wrong source_id: %s", doc.SourceID)
	}
}

func TestGetDocumentBySource_404WhenUnauthorized(t *testing.T) {
	st, sc, _, router := newTestRouter(t)
	otherID, _ := createTestUser(t, st)
	_, token := createTestUser(t, st) // different user owns the token

	indexChunkWithOwner(t, sc, otherID.String(), false, "telegram", "12345:99:msg", "secret")

	w := doJSON(t, router, http.MethodGet,
		"/api/documents/by-source?source_type=telegram&source_id=12345:99:msg",
		"", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (non-leaking), got %d", w.Code)
	}
}

func TestGetDocumentBySource_400WhenMissingParams(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)

	w := doJSON(t, router, http.MethodGet, "/api/documents/by-source", "", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetDocumentBySource_404WhenUnknown(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)

	w := doJSON(t, router, http.MethodGet,
		"/api/documents/by-source?source_type=telegram&source_id=nope",
		"", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
