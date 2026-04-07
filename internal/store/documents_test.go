//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

func TestUpsertAndGetDocument(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	doc := &model.Document{
		ID:         uuid.New(),
		SourceType: "filesystem",
		SourceName: "test",
		SourceID:   "test-file.txt",
		Title:      "Test File",
		Content:    "This is test content for full text search",
		Metadata:   map[string]any{"path": "test-file.txt", "size": 42},
		URL:        "file:///data/test-file.txt",
		Visibility: "private",
		CreatedAt:  time.Now(),
	}

	// Insert
	if err := st.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	// Get
	got, err := st.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if got.Title != "Test File" {
		t.Errorf("expected title 'Test File', got %q", got.Title)
	}
	if got.Content != "This is test content for full text search" {
		t.Errorf("unexpected content: %q", got.Content)
	}
	if got.Metadata["path"] != "test-file.txt" {
		t.Errorf("unexpected metadata path: %v", got.Metadata["path"])
	}

	// Upsert again (update)
	doc.Content = "Updated content"
	if err := st.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("upsert update failed: %v", err)
	}

	got2, err := st.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get after update failed: %v", err)
	}
	if got2.Content != "Updated content" {
		t.Errorf("expected updated content, got %q", got2.Content)
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.GetDocument(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpsertDocument_Dedup(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	doc1 := &model.Document{
		ID:         uuid.New(),
		SourceType: "filesystem",
		SourceName: "test",
		SourceID:   "same-file.txt",
		Title:      "Version 1",
		Content:    "First version",
		Metadata:   map[string]any{},
		Visibility: "private",
		CreatedAt:  time.Now(),
	}

	doc2 := &model.Document{
		ID:         uuid.New(),
		SourceType: "filesystem",
		SourceName: "test",
		SourceID:   "same-file.txt",
		Title:      "Version 2",
		Content:    "Second version",
		Metadata:   map[string]any{},
		Visibility: "private",
		CreatedAt:  time.Now(),
	}

	if err := st.UpsertDocument(ctx, doc1); err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	if err := st.UpsertDocument(ctx, doc2); err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	// Should be updated, not duplicated — search should find only 1 result
	result, err := st.Search(ctx, model.SearchRequest{Query: "version", Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 document (dedup), got %d", result.TotalCount)
	}
	if len(result.Documents) > 0 && result.Documents[0].Title != "Version 2" {
		t.Errorf("expected 'Version 2', got %q", result.Documents[0].Title)
	}
}
