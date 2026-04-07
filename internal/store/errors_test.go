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

	t.Run("delete cursor on closed store", func(t *testing.T) {
		err := st.DeleteSyncCursor(ctx, "test")
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("list connector configs on closed store", func(t *testing.T) {
		_, err := st.ListConnectorConfigs(ctx)
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("get connector config on closed store", func(t *testing.T) {
		_, err := st.GetConnectorConfig(ctx, uuid.New())
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("create connector config on closed store", func(t *testing.T) {
		cfg := &model.ConnectorConfig{Type: "test", Name: "test", Config: map[string]any{}}
		err := st.CreateConnectorConfig(ctx, cfg)
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("update connector config on closed store", func(t *testing.T) {
		cfg := &model.ConnectorConfig{ID: uuid.New(), Type: "test", Name: "test", Config: map[string]any{}}
		err := st.UpdateConnectorConfig(ctx, cfg)
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("delete connector config on closed store", func(t *testing.T) {
		err := st.DeleteConnectorConfig(ctx, uuid.New())
		if err == nil {
			t.Error("expected error on closed store")
		}
	})

	t.Run("update last_run on closed store", func(t *testing.T) {
		err := st.UpdateLastRun(ctx, uuid.New(), time.Now())
		if err == nil {
			t.Error("expected error on closed store")
		}
	})
}
