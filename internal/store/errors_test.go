//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

func TestOperationsOnClosedStore(t *testing.T) {
	st := newClosedStore(t)
	ctx := context.Background()

	t.Run("upsert on closed store", func(t *testing.T) {
		doc := &model.Document{
			ID: uuid.New(), SourceType: "test", SourceName: "test", SourceID: "test",
			Title: "test", Content: "test", Metadata: map[string]any{},
			Visibility: "private", CreatedAt: time.Now(),
		}
		err := st.UpsertDocument(ctx, doc)
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("get on closed store", func(t *testing.T) {
		_, err := st.GetDocument(ctx, uuid.New())
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("search on closed store", func(t *testing.T) {
		_, err := st.Search(ctx, model.SearchRequest{Query: "test", Limit: 10})
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("get cursor on closed store", func(t *testing.T) {
		_, err := st.GetSyncCursor(ctx, "test")
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("upsert cursor on closed store", func(t *testing.T) {
		cursor := &model.SyncCursor{
			ConnectorID: "test", CursorData: map[string]any{},
			LastSync: time.Now(), LastStatus: "test", ItemsSynced: 0,
		}
		err := st.UpsertSyncCursor(ctx, cursor)
		if err == nil {
			t.Error("expected error on closed store")
		}
	})
}
