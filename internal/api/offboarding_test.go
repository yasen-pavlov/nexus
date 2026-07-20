//go:build integration

// Offboarding regression tests: deleting a connector or a user must also
// remove that data from OpenSearch (Gap 1 of the cross-cutting review). Before
// the fix, a deleted connector's chunks stayed searchable forever and deleting
// a connector-owning user 500'd on an FK violation.
package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

// indexOwnedChunk writes one chunk under a given (sourceType, sourceName,
// ownerID, shared) so offboarding tests can assert it disappears after a
// connector or user is deleted.
func indexOwnedChunk(t *testing.T, sc searchIndexer, sourceType, sourceName, sourceID, ownerID string, shared bool) {
	t.Helper()
	ctx := context.Background()
	chunk := model.Chunk{
		ID:          sourceID + ":0",
		ParentID:    sourceID,
		ChunkIndex:  0,
		Title:       sourceID,
		Content:     "offboarding fixture content",
		FullContent: "offboarding fixture content",
		SourceType:  sourceType,
		SourceName:  sourceName,
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
		t.Fatalf("refresh: %v", err)
	}
}

func TestDeleteConnector_RemovesOpenSearchDocs(t *testing.T) {
	_, sc, cm, router := newTestRouter(t)

	cfg := seedEnabledConnector(t, cm, uuid.Nil, "del-cleanup", true)
	indexOwnedChunk(t, sc, "filesystem", "del-cleanup", "doc-1", uuid.NewString(), true)

	// Precondition: the chunk is indexed under this connector's source name.
	before, err := sc.ListIndexedSourceIDs(context.Background(), "filesystem", "del-cleanup")
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 indexed doc before delete, got %d", len(before))
	}

	w := doJSON(t, router, http.MethodDelete, "/api/connectors/"+cfg.ID.String(), "", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete connector: expected 204, got %d (body=%s)", w.Code, w.Body.String())
	}

	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh after delete: %v", err)
	}
	after, err := sc.ListIndexedSourceIDs(context.Background(), "filesystem", "del-cleanup")
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 indexed docs after connector delete, got %d", len(after))
	}
}

func TestDeleteUser_WithOwnedConnector_CleansUp(t *testing.T) {
	st, sc, cm, router := newTestRouter(t)

	uid, _ := createTestUser(t, st)
	cfg := seedEnabledConnector(t, cm, uid, "user-owned", false)
	indexOwnedChunk(t, sc, "filesystem", "user-owned", "u-doc-1", uid.String(), false)

	// Before the fix this returned 500: connector_configs.user_id is a plain
	// FK with no ON DELETE action, so DELETE FROM users failed.
	w := doJSON(t, router, http.MethodDelete, "/api/users/"+uid.String(), "", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete user: expected 204, got %d (body=%s)", w.Code, w.Body.String())
	}

	// The connector row is gone.
	if _, err := st.GetConnectorConfig(context.Background(), cfg.ID); err == nil {
		t.Error("expected connector config to be deleted with its owner")
	}
	// The user is gone.
	if _, err := st.GetUserByID(context.Background(), uid); err == nil {
		t.Error("expected user to be deleted")
	}
	// The connector's indexed docs are gone.
	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	after, err := sc.ListIndexedSourceIDs(context.Background(), "filesystem", "user-owned")
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected owned connector's docs gone after user delete, got %d", len(after))
	}
}

// TestDeleteUser_KeepsOwnedSharedConnector pins the fix for the review's
// data-loss finding: deleting a user must NOT destroy the shared connectors
// they own (community data others search) — those are orphaned (user_id NULL)
// and their documents stay searchable.
func TestDeleteUser_KeepsOwnedSharedConnector(t *testing.T) {
	st, sc, cm, router := newTestRouter(t)

	uid, _ := createTestUser(t, st)
	cfg := seedEnabledConnector(t, cm, uid, "user-shared", true) // shared=true, owned by uid
	// A shared chunk carrying the owner's id (as a real sync would produce).
	indexOwnedChunk(t, sc, "filesystem", "user-shared", "s-doc-1", uid.String(), true)

	w := doJSON(t, router, http.MethodDelete, "/api/users/"+uid.String(), "", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete user: expected 204, got %d (body=%s)", w.Code, w.Body.String())
	}

	// The shared connector row survives, now with no owner.
	got, err := st.GetConnectorConfig(context.Background(), cfg.ID)
	if err != nil {
		t.Fatalf("shared connector should survive user deletion: %v", err)
	}
	if got.UserID != nil {
		t.Errorf("expected orphaned shared connector (user_id NULL), got owner %v", got.UserID)
	}
	// Its documents remain searchable.
	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	after, err := sc.ListIndexedSourceIDs(context.Background(), "filesystem", "user-shared")
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("expected shared connector's docs to survive, got %d", len(after))
	}
}

func TestDeleteUser_SweepsOrphanedChunks(t *testing.T) {
	st, sc, _, router := newTestRouter(t)

	uid, _ := createTestUser(t, st)
	// A chunk owned by the user under a source that has NO connector row —
	// only the DeleteByOwner sweep can reach it.
	indexOwnedChunk(t, sc, "filesystem", "no-such-connector", "orphan-1", uid.String(), false)

	w := doJSON(t, router, http.MethodDelete, "/api/users/"+uid.String(), "", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete user: expected 204, got %d (body=%s)", w.Code, w.Body.String())
	}

	if err := sc.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	after, err := sc.ListIndexedSourceIDs(context.Background(), "filesystem", "no-such-connector")
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected orphaned chunks swept after user delete, got %d", len(after))
	}
}
