//go:build integration

package search

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/testutil"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	url, index := testutil.TestOSConfig(t, "search")
	ctx := context.Background()
	client, err := NewWithIndex(ctx, url, index, nil, lang.Default())
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

// TestSearch_ReturnsAllDedupedResults verifies that the search-package layer
// returns the FULL deduped result set, without applying offset/limit
// pagination. Pagination has moved to the handler layer (after rerank /
// decay / bonus) so the reranker sees the full candidate pool. Handler-level
// pagination behavior is covered by integration tests in
// internal/api/integration_test.go.
func TestSearch_ReturnsAllDedupedResults(t *testing.T) {
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

	// Even with Limit=2 set on the request, the search layer returns all 3
	// matching docs — pagination is the handler's job now.
	result, err := c.Search(ctx, model.SearchRequest{Query: "searching", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 3 {
		t.Errorf("expected total 3, got %d", result.TotalCount)
	}
	if len(result.Documents) != 3 {
		t.Errorf("expected 3 docs (search layer no longer paginates), got %d", len(result.Documents))
	}

	// Same with offset — the search layer ignores it.
	result2, err := c.Search(ctx, model.SearchRequest{Query: "searching", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Documents) != 3 {
		t.Errorf("expected 3 docs (offset is the handler's job), got %d", len(result2.Documents))
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

func TestDeleteBySourceIDs_Granular(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Three chunks under the same source — DeleteBySourceIDs must
	// touch only the listed source_ids and leave the rest alone.
	for i, sid := range []string{"keep.txt", "drop.txt", "also-drop.txt"} {
		doc := testDoc(sid, "Doc"+strconv.Itoa(i), "content "+strconv.Itoa(i))
		doc.SourceName = "del-test"
		_ = c.IndexDocument(ctx, doc)
	}
	_ = c.Refresh(ctx)

	if err := c.DeleteBySourceIDs(ctx, "filesystem", "del-test", []string{"drop.txt", "also-drop.txt"}); err != nil {
		t.Fatalf("DeleteBySourceIDs: %v", err)
	}
	_ = c.Refresh(ctx)

	got, err := c.ListIndexedSourceIDs(ctx, "filesystem", "del-test")
	if err != nil {
		t.Fatalf("ListIndexedSourceIDs: %v", err)
	}
	if len(got) != 1 || got[0] != "keep.txt" {
		t.Errorf("expected only keep.txt, got %v", got)
	}
}

func TestDeleteBySourceIDs_EmptyInputIsNoop(t *testing.T) {
	c := newTestClient(t)
	// An empty slice must short-circuit without sending a delete-all
	// query that would nuke unrelated docs. Verifying via "no error,
	// nothing changed" requires no setup — empty input on an empty
	// index should still return nil.
	if err := c.DeleteBySourceIDs(context.Background(), "filesystem", "empty", nil); err != nil {
		t.Errorf("empty input should be a no-op, got error: %v", err)
	}
}

func TestListIndexedSourceIDs_DedupedAcrossChunks(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// One document with content long enough to chunk into multiple
	// pieces — terms aggregation must collapse them into a single
	// source_id bucket so the deletion-sync diff stays accurate.
	doc := testDoc("multichunk.txt", "Multi", strings.Repeat("alpha beta gamma ", 200))
	doc.SourceName = "list-test"
	if err := c.IndexDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	_ = c.Refresh(ctx)

	ids, err := c.ListIndexedSourceIDs(ctx, "filesystem", "list-test")
	if err != nil {
		t.Fatalf("ListIndexedSourceIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "multichunk.txt" {
		t.Errorf("expected ['multichunk.txt'], got %v", ids)
	}
}

func TestListIndexedSourceIDs_EmptySource(t *testing.T) {
	c := newTestClient(t)
	got, err := c.ListIndexedSourceIDs(context.Background(), "filesystem", "no-such-source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
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

func TestUpdateOwnershipBySource(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	now := time.Now()

	// Index chunks for two sources, plus one chunk on a third source we will not touch.
	chunks := []model.Chunk{
		{
			ID: "fs:owned:a:0", ParentID: "fs:owned:a", Title: "A", Content: "alpha content",
			SourceType: "filesystem", SourceName: "owned", SourceID: "a",
			OwnerID:   "user-1",
			Shared:    false,
			CreatedAt: now,
		},
		{
			ID: "fs:owned:b:0", ParentID: "fs:owned:b", Title: "B", Content: "beta content",
			SourceType: "filesystem", SourceName: "owned", SourceID: "b",
			OwnerID:   "user-1",
			Shared:    false,
			CreatedAt: now,
		},
		{
			ID: "fs:other:c:0", ParentID: "fs:other:c", Title: "C", Content: "gamma content",
			SourceType: "filesystem", SourceName: "other", SourceID: "c",
			OwnerID:   "user-1",
			Shared:    false,
			CreatedAt: now,
		},
	}
	if err := c.IndexChunks(ctx, chunks); err != nil {
		t.Fatalf("index chunks: %v", err)
	}
	c.Refresh(ctx) //nolint:errcheck // test

	visibleTo := func(t *testing.T, owner string) map[string]bool {
		t.Helper()
		res, err := c.Search(ctx, model.SearchRequest{Query: "content", Limit: 10, OwnerID: owner})
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]bool)
		for _, d := range res.Documents {
			out[d.SourceID] = true
		}
		return out
	}

	// Before any flip: user-1 sees all three (owns them); user-2 sees nothing.
	if got := visibleTo(t, "user-1"); !got["a"] || !got["b"] || !got["c"] {
		t.Errorf("baseline: user-1 should own a,b,c, got %+v", got)
	}
	if got := visibleTo(t, "user-2"); len(got) != 0 {
		t.Errorf("baseline: user-2 should see nothing, got %+v", got)
	}

	// Flip "owned" to shared with no owner.
	if err := c.UpdateOwnershipBySource(ctx, "filesystem", "owned", "", true); err != nil {
		t.Fatalf("update ownership: %v", err)
	}

	// user-2 should now see a and b (via shared) but NOT c (still private under user-1).
	got := visibleTo(t, "user-2")
	if !got["a"] || !got["b"] {
		t.Errorf("user-2 should see a and b after flip to shared, got %+v", got)
	}
	if got["c"] {
		t.Errorf("user-2 should NOT see c (different source, still private), got %+v", got)
	}

	// Flip back to private and reassign to user-3.
	if err := c.UpdateOwnershipBySource(ctx, "filesystem", "owned", "user-3", false); err != nil {
		t.Fatalf("update back: %v", err)
	}

	// user-3 now owns a and b.
	got = visibleTo(t, "user-3")
	if !got["a"] || !got["b"] {
		t.Errorf("user-3 should now own a and b, got %+v", got)
	}

	// user-2 should no longer see a and b after they went private.
	got = visibleTo(t, "user-2")
	if got["a"] || got["b"] {
		t.Errorf("user-2 should no longer see a/b after they went private, got %+v", got)
	}

	// user-1 should still own c (was never touched).
	got = visibleTo(t, "user-1")
	if !got["c"] {
		t.Errorf("user-1 should still own c, got %+v", got)
	}
	if got["a"] || got["b"] {
		t.Errorf("user-1 should no longer see a/b after reassign to user-3, got %+v", got)
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
	c, err := NewWithIndex(ctx, url, index, nil, lang.Default())
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

// TestHybridSearch_ExcludesHiddenDocs guards against the bug where
// HybridSearch built its BM25 sub-query inline without the `must_not
// hidden` clause that the BM25-only path gets via buildSearchQuery,
// causing per-message Telegram docs (Hidden=true) to leak into search
// results. Seed one hidden and one visible doc that both match the
// query and verify only the visible one comes back.
func TestHybridSearch_ExcludesHiddenDocs(t *testing.T) {
	url, index := testutil.TestOSConfig(t, "hybrid-hidden")
	ctx := context.Background()
	c, err := NewWithIndex(ctx, url, index, nil, lang.Default())
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	if err := c.EnsureIndex(ctx, 3); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.DeleteIndex(context.Background()) }) //nolint:errcheck // test

	now := time.Now()
	chunks := []model.Chunk{
		{
			ID: "tg:main:win:0", ParentID: "tg:main:win", DocID: "tg:main:win",
			ChunkIndex: 0, Title: "Chat", Content: "dinner plans tonight",
			SourceType: "telegram", SourceName: "main", SourceID: "win",
			Embedding: []float32{0.9, 0.1, 0.0}, Visibility: "private",
			Shared: true, CreatedAt: now,
		},
		{
			ID: "tg:main:msg:0", ParentID: "tg:main:msg", DocID: "tg:main:msg",
			ChunkIndex: 0, Title: "Chat", Content: "dinner plans tonight",
			SourceType: "telegram", SourceName: "main", SourceID: "msg",
			Embedding: []float32{0.9, 0.1, 0.0}, Visibility: "private",
			Shared: true, Hidden: true, CreatedAt: now,
		},
	}
	if err := c.IndexChunks(ctx, chunks); err != nil {
		t.Fatal(err)
	}
	c.Refresh(ctx) //nolint:errcheck // test

	queryEmb := []float32{0.9, 0.1, 0.0}
	result, err := c.HybridSearch(ctx, model.SearchRequest{Query: "dinner", Limit: 10}, queryEmb)
	if err != nil {
		t.Fatalf("hybrid search failed: %v", err)
	}

	for _, hit := range result.Documents {
		if hit.SourceID == "msg" {
			t.Errorf("hidden doc leaked into hybrid results: %+v", hit)
		}
	}
	if len(result.Documents) == 0 {
		t.Errorf("expected the visible window doc to match, got zero results")
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

// TestGetChunkByDocID_HappyPath verifies the download-endpoint's
// doc-UUID → chunk resolver. Indexes two chunks for one doc and
// expects the chunk-index-0 chunk back ordered correctly.
func TestGetChunkByDocID_HappyPath(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	docID := "fs:test:preview-me"
	chunks := []model.Chunk{
		{
			ID: docID + ":0", ParentID: docID, DocID: "abc-uuid", ChunkIndex: 0,
			Title: "Preview me", Content: "first chunk",
			SourceType: "filesystem", SourceName: "test", SourceID: "preview-me",
			OwnerID: "user-1", Shared: false, CreatedAt: time.Now(),
		},
		{
			ID: docID + ":1", ParentID: docID, DocID: "abc-uuid", ChunkIndex: 1,
			Title: "Preview me", Content: "second chunk",
			SourceType: "filesystem", SourceName: "test", SourceID: "preview-me",
			OwnerID: "user-1", Shared: false, CreatedAt: time.Now(),
		},
	}
	if err := c.IndexChunks(ctx, chunks); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := c.GetChunkByDocID(ctx, "abc-uuid")
	if err != nil {
		t.Fatalf("GetChunkByDocID: %v", err)
	}
	if got.ChunkIndex != 0 {
		t.Errorf("got chunk_index=%d, want 0 (lowest)", got.ChunkIndex)
	}
	if got.SourceID != "preview-me" || got.OwnerID != "user-1" {
		t.Errorf("wrong chunk returned: %+v", got)
	}
}

func TestGetChunkByDocID_NotFound(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	_, err := c.GetChunkByDocID(ctx, "no-such-uuid")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// TestLanguageAnalysis_Stemming exercises the per-field language analyzers
// wired through lang.Default(). Each sub-test indexes a document in one
// language and issues a query in a different morphological form; with the
// old standard-analyzer-only mapping none of these would match.
func TestLanguageAnalysis_Stemming(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		content string
		query   string
	}{
		{
			name:    "english_commands_to_command",
			title:   "Docker cheat sheet",
			content: "Common docker commands for daily use",
			query:   "command",
		},
		{
			name:    "english_containers_to_container",
			title:   "Production notes",
			content: "Running containers in production needs care",
			query:   "container",
		},
		{
			name:    "german_versicherungen_to_versicherung",
			title:   "Jahresübersicht",
			content: "Ihre Versicherung für das Jahr 2026 ist aktiv",
			query:   "Versicherungen",
		},
		{
			name:    "german_versicherung_to_versicherungen",
			title:   "Portfolio",
			content: "Alle Versicherungen auf einen Blick",
			query:   "Versicherung",
		},
		{
			name:    "bulgarian_sreshta_to_sreshti",
			title:   "Планове",
			content: "Имаме среща утре в офиса",
			query:   "срещи",
		},
		{
			name:    "bulgarian_sreshti_to_sreshta",
			title:   "Календар",
			content: "Следващата седмица планираме няколко срещи",
			query:   "среща",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t)
			ctx := context.Background()
			doc := testDoc(tt.name+".txt", tt.title, tt.content)
			if err := c.IndexDocument(ctx, doc); err != nil {
				t.Fatalf("index failed: %v", err)
			}
			if err := c.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			result, err := c.Search(ctx, model.SearchRequest{Query: tt.query, Limit: 10})
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}
			if result.TotalCount == 0 {
				t.Fatalf("query %q did not match doc with content %q — stemming broken", tt.query, tt.content)
			}
		})
	}
}

// TestLanguageAnalysis_MixedLanguage verifies that a single chunk
// containing text in two languages is reachable via queries in either
// language. This is the smoking-gun test that per-field analyzers work
// across all languages on every doc (not just the one language the doc
// "is") — without most_fields accumulating across sub-fields, one of
// these queries would miss.
func TestLanguageAnalysis_MixedLanguage(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	doc := testDoc(
		"mixed.txt",
		"Work notes",
		"Notes from today: review docker containers and confirm Ihre Versicherung renewal",
	)
	if err := c.IndexDocument(ctx, doc); err != nil {
		t.Fatalf("index failed: %v", err)
	}
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"container", "Versicherungen"} {
		result, err := c.Search(ctx, model.SearchRequest{Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("search %q failed: %v", q, err)
		}
		if result.TotalCount == 0 {
			t.Errorf("query %q did not match the mixed-language doc", q)
		}
	}
}

func TestCheckMappingCurrent_FreshIndex(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	ok, err := c.CheckMappingCurrent(ctx)
	if err != nil {
		t.Fatalf("CheckMappingCurrent failed: %v", err)
	}
	if !ok {
		t.Error("expected CheckMappingCurrent to return true on a fresh index built from lang.Default()")
	}
}

// TestCheckMappingCurrent_StaleIndex simulates the upgrade scenario: an
// existing index was created by an older build that didn't know about
// language sub-fields. A new client configured with lang.Default() pointing
// at that index should report the mapping as out of date.
func TestCheckMappingCurrent_StaleIndex(t *testing.T) {
	url, index := testutil.TestOSConfig(t, "stale-mapping")
	ctx := context.Background()

	// Step 1: build the index with empty languages — emits the old
	// standard-analyzer-only mapping, no sub-fields.
	old, err := NewWithIndex(ctx, url, index, nil, nil)
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	if err := old.EnsureIndex(ctx, 0); err != nil {
		t.Fatalf("create stale index: %v", err)
	}
	t.Cleanup(func() {
		old.DeleteIndex(context.Background()) //nolint:errcheck // test cleanup
	})

	// Step 2: a fresh client configured with lang.Default() pointing at
	// the same index should see the sub-fields as missing.
	fresh, err := NewWithIndex(ctx, url, index, nil, lang.Default())
	if err != nil {
		t.Fatalf("fresh client: %v", err)
	}
	ok, err := fresh.CheckMappingCurrent(ctx)
	if err != nil {
		t.Fatalf("CheckMappingCurrent failed: %v", err)
	}
	if ok {
		t.Error("expected CheckMappingCurrent to return false on an index without language sub-fields")
	}
}

// TestCheckMappingCurrent_WrongAnalyzer covers the branch where a sub-field
// exists but uses the wrong analyzer — e.g., someone manually edited the
// mapping or the language list changed but a reindex was skipped.
func TestCheckMappingCurrent_WrongAnalyzer(t *testing.T) {
	url, index := testutil.TestOSConfig(t, "wrong-analyzer")
	ctx := context.Background()

	// Build the index with a different language set (just english) so
	// content.english exists but content.german/content.bulgarian don't.
	partial, err := NewWithIndex(ctx, url, index, nil, []lang.Language{
		{Name: "english", OpenSearchAnalyzer: "english", TesseractCode: "eng"},
	})
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	if err := partial.EnsureIndex(ctx, 0); err != nil {
		t.Fatalf("create partial index: %v", err)
	}
	t.Cleanup(func() {
		partial.DeleteIndex(context.Background()) //nolint:errcheck // test cleanup
	})

	// A fresh client configured with the full Default() list should see
	// german and bulgarian sub-fields as missing.
	fresh, err := NewWithIndex(ctx, url, index, nil, lang.Default())
	if err != nil {
		t.Fatalf("fresh client: %v", err)
	}
	ok, err := fresh.CheckMappingCurrent(ctx)
	if err != nil {
		t.Fatalf("CheckMappingCurrent failed: %v", err)
	}
	if ok {
		t.Error("expected CheckMappingCurrent to return false when some language sub-fields are missing")
	}
}
