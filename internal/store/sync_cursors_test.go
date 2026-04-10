//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

// makeTestConnector creates a real connector_configs row so cursor inserts
// satisfy the foreign key constraint, and returns its ID.
func makeTestConnector(t *testing.T, st *Store, name string) uuid.UUID {
	t.Helper()
	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: name, Config: map[string]any{}, Enabled: true, Shared: true,
	}
	if err := st.CreateConnectorConfig(context.Background(), cfg); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	return cfg.ID
}

func TestSyncCursors(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id := makeTestConnector(t, st, "test-fs")

	t.Run("get nonexistent cursor returns nil", func(t *testing.T) {
		cursor, err := st.GetSyncCursor(ctx, uuid.New())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cursor != nil {
			t.Error("expected nil cursor for nonexistent connector")
		}
	})

	t.Run("upsert and get", func(t *testing.T) {
		cursor := &model.SyncCursor{
			ConnectorID: id,
			CursorData:  map[string]any{"last_sync_time": "2026-01-01T00:00:00Z"},
			LastSync:    time.Now(),
			LastStatus:  "success",
			ItemsSynced: 42,
		}

		if err := st.UpsertSyncCursor(ctx, cursor); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, id)
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got == nil {
			t.Fatal("expected cursor, got nil")
		}
		if got.ConnectorID != id {
			t.Errorf("expected connector_id %v, got %v", id, got.ConnectorID)
		}
		if got.ItemsSynced != 42 {
			t.Errorf("expected 42 items synced, got %d", got.ItemsSynced)
		}
		if got.CursorData["last_sync_time"] != "2026-01-01T00:00:00Z" {
			t.Errorf("unexpected cursor data: %v", got.CursorData)
		}
	})

	t.Run("upsert updates existing", func(t *testing.T) {
		cursor := &model.SyncCursor{
			ConnectorID: id,
			CursorData:  map[string]any{"last_sync_time": "2026-06-01T00:00:00Z"},
			LastSync:    time.Now(),
			LastStatus:  "success",
			ItemsSynced: 100,
		}

		if err := st.UpsertSyncCursor(ctx, cursor); err != nil {
			t.Fatalf("upsert update failed: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, id)
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got.ItemsSynced != 100 {
			t.Errorf("expected 100 items synced after update, got %d", got.ItemsSynced)
		}
	})

	t.Run("delete all cursors", func(t *testing.T) {
		c1 := makeTestConnector(t, st, "delete-all-c1")
		c2 := makeTestConnector(t, st, "delete-all-c2")
		_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: c1, CursorData: map[string]any{}, LastSync: time.Now(), LastStatus: "ok"})
		_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: c2, CursorData: map[string]any{}, LastSync: time.Now(), LastStatus: "ok"})

		if err := st.DeleteAllSyncCursors(ctx); err != nil {
			t.Fatalf("delete all failed: %v", err)
		}

		got1, _ := st.GetSyncCursor(ctx, c1)
		got2, _ := st.GetSyncCursor(ctx, c2)
		if got1 != nil || got2 != nil {
			t.Error("expected all cursors deleted")
		}

		// Re-insert for the next subtest
		_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{ConnectorID: id, CursorData: map[string]any{}, LastSync: time.Now(), LastStatus: "ok", ItemsSynced: 1})
	})

	t.Run("delete cursor", func(t *testing.T) {
		if err := st.DeleteSyncCursor(ctx, id); err != nil {
			t.Fatalf("delete failed: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, id)
		if err != nil {
			t.Fatalf("get after delete failed: %v", err)
		}
		if got != nil {
			t.Error("expected nil cursor after delete")
		}
	})

	t.Run("FK cascade deletes cursor when connector is deleted", func(t *testing.T) {
		cid := makeTestConnector(t, st, "cascade-test")
		_ = st.UpsertSyncCursor(ctx, &model.SyncCursor{
			ConnectorID: cid, CursorData: map[string]any{"x": 1}, LastSync: time.Now(), LastStatus: "ok",
		})

		if err := st.DeleteConnectorConfig(ctx, cid); err != nil {
			t.Fatalf("delete connector: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, cid)
		if err != nil {
			t.Fatalf("get after cascade: %v", err)
		}
		if got != nil {
			t.Error("expected cursor to be cascade-deleted with the connector")
		}
	})
}
