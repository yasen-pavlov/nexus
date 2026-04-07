//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func TestSyncCursors(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	t.Run("get nonexistent cursor returns nil", func(t *testing.T) {
		cursor, err := st.GetSyncCursor(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cursor != nil {
			t.Error("expected nil cursor for nonexistent connector")
		}
	})

	t.Run("upsert and get", func(t *testing.T) {
		cursor := &model.SyncCursor{
			ConnectorID: "test-fs",
			CursorData:  map[string]any{"last_sync_time": "2026-01-01T00:00:00Z"},
			LastSync:    time.Now(),
			LastStatus:  "success",
			ItemsSynced: 42,
		}

		if err := st.UpsertSyncCursor(ctx, cursor); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, "test-fs")
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got == nil {
			t.Fatal("expected cursor, got nil")
		}
		if got.ConnectorID != "test-fs" {
			t.Errorf("expected connector_id 'test-fs', got %q", got.ConnectorID)
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
			ConnectorID: "test-fs",
			CursorData:  map[string]any{"last_sync_time": "2026-06-01T00:00:00Z"},
			LastSync:    time.Now(),
			LastStatus:  "success",
			ItemsSynced: 100,
		}

		if err := st.UpsertSyncCursor(ctx, cursor); err != nil {
			t.Fatalf("upsert update failed: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, "test-fs")
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got.ItemsSynced != 100 {
			t.Errorf("expected 100 items synced after update, got %d", got.ItemsSynced)
		}
	})

	t.Run("delete cursor", func(t *testing.T) {
		if err := st.DeleteSyncCursor(ctx, "test-fs"); err != nil {
			t.Fatalf("delete failed: %v", err)
		}

		got, err := st.GetSyncCursor(ctx, "test-fs")
		if err != nil {
			t.Fatalf("get after delete failed: %v", err)
		}
		if got != nil {
			t.Error("expected nil cursor after delete")
		}
	})
}
