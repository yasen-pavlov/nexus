//go:build integration

package search

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/testutil"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	url, index := testutil.TestOSConfig(t, "search")
	ctx := context.Background()
	client, err := NewWithIndex(ctx, url, index, nil)
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	if err := client.EnsureIndex(ctx, 0); err != nil {
		t.Fatalf("create index: %v", err)
	}
	t.Cleanup(func() {
		client.DeleteIndex(context.Background()) //nolint:errcheck // test cleanup
	})
	return client
}

func testDoc(sourceID, title, content string) *model.Document {
	return &model.Document{
		ID:         uuid.New(),
		SourceType: "filesystem",
		SourceName: "test",
		SourceID:   sourceID,
		Title:      title,
		Content:    content,
		Metadata:   map[string]any{"path": sourceID},
		URL:        "file:///test/" + sourceID,
		Visibility: "private",
		CreatedAt:  time.Now(),
	}
}

func TestIndexAndSearch(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc := testDoc("test.txt", "Pasta Recipe", "Cook spaghetti with garlic and olive oil")
	if err := c.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("index failed: %v", err)
	}
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	result, err := c.Search(ctx, model.SearchRequest{Query: "spaghetti", Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.TotalCount != 1 {
		t.Fatalf("expected 1 result, got %d", result.TotalCount)
	}
	if result.Documents[0].Title != "Pasta Recipe" {
		t.Errorf("expected title 'Pasta Recipe', got %q", result.Documents[0].Title)
	}
	if result.Query != "spaghetti" {
		t.Errorf("expected query 'spaghetti', got %q", result.Query)
	}
}

func TestSearch_Highlighting(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc := testDoc("hl.txt", "Test Doc", "The quick brown fox jumps over the lazy dog")
	c.IndexDocument(ctx, doc) //nolint:errcheck // test
	c.Refresh(ctx)            //nolint:errcheck // test

	result, err := c.Search(ctx, model.SearchRequest{Query: "fox", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Fatalf("expected 1 result, got %d", result.TotalCount)
	}
	if result.Documents[0].Headline == "" {
		t.Error("expected headline with highlights")
	}
}

func TestSearch_Pagination(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	for i, content := range []string{
		"Alpha document about searching",
		"Beta document about searching",
		"Gamma document about searching",
	} {
		doc := testDoc(
			string(rune('a'+i))+".txt",
			"Doc "+string(rune('A'+i)),
			content,
		)
		c.IndexDocument(ctx, doc) //nolint:errcheck // test
	}
	c.Refresh(ctx) //nolint:errcheck // test

	result, err := c.Search(ctx, model.SearchRequest{Query: "searching", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 3 {
		t.Errorf("expected total 3, got %d", result.TotalCount)
	}
	if len(result.Documents) != 2 {
		t.Errorf("expected 2 docs with limit=2, got %d", len(result.Documents))
	}

	result2, err := c.Search(ctx, model.SearchRequest{Query: "searching", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Documents) != 1 {
		t.Errorf("expected 1 doc with offset=2, got %d", len(result2.Documents))
	}
}

func TestSearch_NoResults(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	result, err := c.Search(ctx, model.SearchRequest{Query: "xyznonexistent", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 0 {
		t.Errorf("expected 0 results, got %d", result.TotalCount)
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc := testDoc("def.txt", "Default", "Testing default limit behavior")
	c.IndexDocument(ctx, doc) //nolint:errcheck // test
	c.Refresh(ctx)            //nolint:errcheck // test

	result, err := c.Search(ctx, model.SearchRequest{Query: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 result, got %d", result.TotalCount)
	}
}

func TestIndexDocument_Dedup(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc1 := testDoc("dedup.txt", "Version 1", "First version content")
	doc2 := testDoc("dedup.txt", "Version 2", "Second version content")

	c.IndexDocument(ctx, doc1) //nolint:errcheck // test
	c.IndexDocument(ctx, doc2) //nolint:errcheck // test
	c.Refresh(ctx)             //nolint:errcheck // test

	result, err := c.Search(ctx, model.SearchRequest{Query: "version", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 result (dedup), got %d", result.TotalCount)
	}
	if result.TotalCount > 0 && result.Documents[0].Title != "Version 2" {
		t.Errorf("expected 'Version 2', got %q", result.Documents[0].Title)
	}
}

func TestDeleteBySource(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc1 := testDoc("del1.txt", "Delete Me", "Content to delete")
	doc1.SourceName = "source-a"
	doc2 := testDoc("del2.txt", "Keep Me", "Content to keep")
	doc2.SourceName = "source-b"

	c.IndexDocument(ctx, doc1) //nolint:errcheck // test
	c.IndexDocument(ctx, doc2) //nolint:errcheck // test
	c.Refresh(ctx)             //nolint:errcheck // test

	if err := c.DeleteBySource(ctx, "filesystem", "source-a"); err != nil {
		t.Fatalf("delete by source failed: %v", err)
	}
	c.Refresh(ctx) //nolint:errcheck // test

	result, err := c.Search(ctx, model.SearchRequest{Query: "content", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 result after delete, got %d", result.TotalCount)
	}
}

func TestIndexDocument_NilID(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc := &model.Document{
		SourceType: "filesystem", SourceName: "test", SourceID: "nil-id.txt",
		Title: "Nil ID", Content: "Test doc with nil UUID",
		Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
	}
	if err := c.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("index failed: %v", err)
	}
	if doc.ID == uuid.Nil {
		t.Error("expected ID to be generated")
	}
}

func TestIndexDocument_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	doc := testDoc("cancel.txt", "Cancel", "test")
	err := c.IndexDocument(ctx, doc)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSearch_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Search(ctx, model.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestDeleteBySource_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.DeleteBySource(ctx, "filesystem", "test")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestIndexChunks(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	now := time.Now()
	chunks := []model.Chunk{
		{
			ID: "fs:test:doc1:0", ParentID: "fs:test:doc1", ChunkIndex: 0,
			Title: "Test Doc", Content: "First chunk of the test document",
			FullContent: "First chunk of the test document. Second chunk continues here.",
			SourceType: "filesystem", SourceName: "test", SourceID: "doc1",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: now,
		},
		{
			ID: "fs:test:doc1:1", ParentID: "fs:test:doc1", ChunkIndex: 1,
			Title: "Test Doc", Content: "Second chunk continues here",
			SourceType: "filesystem", SourceName: "test", SourceID: "doc1",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: now,
		},
	}

	if err := c.IndexChunks(ctx, chunks); err != nil {
		t.Fatalf("index chunks failed: %v", err)
	}
	c.Refresh(ctx) //nolint:errcheck // test

	result, err := c.Search(ctx, model.SearchRequest{Query: "chunk", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// Should dedup by parent_id — only 1 result for the document
	if result.TotalCount != 1 {
		t.Errorf("expected 1 result (deduped), got %d", result.TotalCount)
	}
}

func TestIndexChunks_Empty(t *testing.T) {
	c := newTestClient(t)
	if err := c.IndexChunks(context.Background(), nil); err != nil {
		t.Fatalf("expected no error for empty chunks, got: %v", err)
	}
}

func TestIndexChunks_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	chunks := []model.Chunk{{ID: "test:0", ParentID: "test", Content: "test"}}
	err := c.IndexChunks(ctx, chunks)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestHybridSearch(t *testing.T) {
	url, index := testutil.TestOSConfig(t, "hybrid")
	ctx := context.Background()
	c, err := NewWithIndex(ctx, url, index, nil)
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	// Create index with k-NN (dim=3)
	if err := c.EnsureIndex(ctx, 3); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.DeleteIndex(context.Background()) }) //nolint:errcheck // test

	now := time.Now()
	chunks := []model.Chunk{
		{
			ID: "fs:test:a:0", ParentID: "fs:test:a", ChunkIndex: 0,
			Title: "Doc A", Content: "Semantic search with vector embeddings",
			SourceType: "filesystem", SourceName: "test", SourceID: "a",
			Embedding: []float32{0.9, 0.1, 0.0}, Metadata: map[string]any{},
			Visibility: "private", CreatedAt: now,
		},
		{
			ID: "fs:test:b:0", ParentID: "fs:test:b", ChunkIndex: 0,
			Title: "Doc B", Content: "Traditional keyword based search",
			SourceType: "filesystem", SourceName: "test", SourceID: "b",
			Embedding: []float32{0.1, 0.9, 0.0}, Metadata: map[string]any{},
			Visibility: "private", CreatedAt: now,
		},
	}

	if err := c.IndexChunks(ctx, chunks); err != nil {
		t.Fatal(err)
	}
	c.Refresh(ctx) //nolint:errcheck // test

	queryEmb := []float32{0.8, 0.2, 0.0} // closer to Doc A
	result, err := c.HybridSearch(ctx, model.SearchRequest{Query: "search", Limit: 10}, queryEmb)
	if err != nil {
		t.Fatalf("hybrid search failed: %v", err)
	}
	if result.TotalCount < 1 {
		t.Errorf("expected at least 1 result, got %d", result.TotalCount)
	}
}

func TestHybridSearch_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.HybridSearch(ctx, model.SearchRequest{Query: "test"}, []float32{0.1, 0.2})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestEnsureIndex_Idempotent(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Index already created by newTestClient, calling again should not error
	if err := c.EnsureIndex(ctx, 0); err != nil {
		t.Fatalf("second EnsureIndex failed: %v", err)
	}
}

func TestRecreateIndex(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Index a document first
	doc := &model.Document{
		SourceType: "test", SourceName: "test", SourceID: "1",
		Title: "Test", Content: "Before recreate",
	}
	if err := c.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("index failed: %v", err)
	}
	_ = c.Refresh(ctx)

	// Recreate with different dimension
	if err := c.RecreateIndex(ctx, 128); err != nil {
		t.Fatalf("RecreateIndex failed: %v", err)
	}

	// Old document should be gone
	_ = c.Refresh(ctx)
	result, err := c.Search(ctx, model.SearchRequest{Query: "Before recreate", Limit: 10})
	if err != nil {
		t.Fatalf("search after recreate failed: %v", err)
	}
	if len(result.Documents) != 0 {
		t.Errorf("expected 0 results after recreate, got %d", len(result.Documents))
	}
}
