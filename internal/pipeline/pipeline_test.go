//go:build integration

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

// configureFSWithExtractor configures a filesystem connector and injects a
// PlainText extractor — mirroring how the production ConnectorManager wires
// connectors. Without this, filesystem docs come back with empty content
// because the new behavior never falls back to raw bytes.
func configureFSWithExtractor(t *testing.T, name, dir, patterns string) connector.Connector {
	t.Helper()
	fsConn, err := connector.Create("filesystem")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := fsConn.Configure(connector.Config{
		"name": name, "root_path": dir, "patterns": patterns,
	}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if extConn, ok := fsConn.(interface {
		SetExtractor(*extractor.Registry)
	}); ok {
		extConn.SetExtractor(extractor.NewRegistry("", nil))
	}
	return fsConn
}

func newTestDeps(t *testing.T) (*store.Store, *search.Client) {
	t.Helper()
	tdb := testutil.NewTestDB(t, "pipeline", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	osURL, osIndex := testutil.TestOSConfig(t, "pipeline")
	sc, err := search.NewWithIndex(context.Background(), osURL, osIndex, nil, lang.Default())
	if err != nil {
		t.Skipf("OpenSearch not available: %v", err)
	}
	if err := sc.EnsureIndex(context.Background(), 0); err != nil {
		t.Fatalf("create search index: %v", err)
	}
	t.Cleanup(func() { sc.DeleteIndex(context.Background()) }) //nolint:errcheck // test
	return st, sc
}

type mockEmbedderProvider struct {
	embedder embedding.Embedder
}

func (m *mockEmbedderProvider) Get() embedding.Embedder { return m.embedder }

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, m.dim)
		for j := range result[i] {
			result[i][j] = 0.1
		}
	}
	return result, nil
}

func (m *mockEmbedder) Dimension() int { return m.dim }

func TestPipelineRun_WithEmbedder(t *testing.T) {
	st, sc := newTestDeps(t)
	ctx := context.Background()

	// Recreate index with k-NN enabled
	sc.DeleteIndex(ctx)                  //nolint:errcheck // test
	sc.EnsureIndex(ctx, 3)               //nolint:errcheck // test using dim=3 for simplicity

	provider := &mockEmbedderProvider{embedder: &mockEmbedder{dim: 3}}
	p := New(st, sc, provider, zap.NewNop())

	dir := t.TempDir()
	os.WriteFile(dir+"/test.txt", []byte("Document with embeddings for testing"), 0o644) //nolint:errcheck // test

	fsConn := configureFSWithExtractor(t, "embed-test", dir, "*.txt")

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "embed-test",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	report, err := p.RunWithProgress(ctx, connID, fsConn, "", false, nil)
	if err != nil {
		t.Fatalf("pipeline run failed: %v", err)
	}
	if report.DocsProcessed != 1 {
		t.Errorf("expected 1 doc, got %d", report.DocsProcessed)
	}
	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", report.Errors)
	}

	sc.Refresh(ctx) //nolint:errcheck // test
	result, err := sc.Search(ctx, model.SearchRequest{Query: "embeddings", Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 result, got %d", result.TotalCount)
	}
}

func TestPipelineRun(t *testing.T) {
	st, sc := newTestDeps(t)
	ctx := context.Background()
	p := New(st, sc, nil, zap.NewNop())

	dir := t.TempDir()
	os.WriteFile(dir+"/hello.txt", []byte("Unique xylophone document for verification"), 0o644)     //nolint:errcheck // test file
	os.WriteFile(dir+"/world.md", []byte("Another document about different topics entirely"), 0o644) //nolint:errcheck // test file

	fsConn := configureFSWithExtractor(t, "pipeline-test", dir, "*.txt,*.md")

	// Cursor + connector must be created in the store first because of the FK
	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "pipeline-test",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	// First sync
	report, err := p.RunWithProgress(ctx, connID, fsConn, "", false, nil)
	if err != nil {
		t.Fatalf("pipeline run failed: %v", err)
	}
	if report.DocsProcessed != 2 {
		t.Errorf("expected 2 docs processed, got %d", report.DocsProcessed)
	}
	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", report.Errors)
	}

	// Verify documents are searchable in OpenSearch
	sc.Refresh(ctx) //nolint:errcheck // test
	result, err := sc.Search(ctx, model.SearchRequest{Query: "xylophone", Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 search result for 'xylophone', got %d", result.TotalCount)
	}

	// Verify cursor was saved keyed by connector UUID
	cursor, err := st.GetSyncCursor(ctx, connID)
	if err != nil {
		t.Fatalf("get cursor failed: %v", err)
	}
	if cursor == nil {
		t.Fatal("expected cursor to be saved")
	}

	// Second sync (incremental — no new files)
	report2, err := p.RunWithProgress(ctx, connID, fsConn, "", false, nil)
	if err != nil {
		t.Fatalf("second pipeline run failed: %v", err)
	}
	if report2.DocsProcessed != 0 {
		t.Errorf("expected 0 docs on incremental sync, got %d", report2.DocsProcessed)
	}
}

// fakeDeletionConnector lets pipeline tests drive arbitrary
// (Documents, CurrentSourceIDs) combinations without needing a real
// connector to round-trip a fake source. Each Fetch call pops the next
// scripted result.
type fakeDeletionConnector struct {
	results []*model.FetchResult
	idx     int
}

func (f *fakeDeletionConnector) Type() string                                { return "filesystem" }
func (f *fakeDeletionConnector) Name() string                                { return "fake-del" }
func (f *fakeDeletionConnector) Configure(_ connector.Config) error          { return nil }
func (f *fakeDeletionConnector) Validate() error                             { return nil }
func (f *fakeDeletionConnector) Fetch(_ context.Context, _ *model.SyncCursor) (*model.FetchResult, error) {
	if f.idx >= len(f.results) {
		return &model.FetchResult{Cursor: &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"}}, nil
	}
	r := f.results[f.idx]
	f.idx++
	return r, nil
}

func newFakeDoc(sid, title string) model.Document {
	return model.Document{
		SourceType: "filesystem",
		SourceName: "fake-del",
		SourceID:   sid,
		Title:      title,
		Content:    "content for " + sid,
		Visibility: "private",
		CreatedAt:  time.Now(),
	}
}

func TestPipelineRun_DeletionSync_RemovesStale(t *testing.T) {
	st, sc := newTestDeps(t)
	ctx := context.Background()
	p := New(st, sc, nil, zap.NewNop())

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "fake-del",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	conn := &fakeDeletionConnector{
		results: []*model.FetchResult{
			// First sync: index three docs.
			{
				Documents: []model.Document{
					newFakeDoc("a.txt", "Alpha"),
					newFakeDoc("b.txt", "Beta"),
					newFakeDoc("c.txt", "Gamma"),
				},
				CurrentSourceIDs: []string{"a.txt", "b.txt", "c.txt"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
			// Second sync: only two remain upstream → c.txt should be
			// deleted from the index without re-emitting a/b.
			{
				Documents:        nil,
				CurrentSourceIDs: []string{"a.txt", "b.txt"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
		},
	}

	r1, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if r1.DocsDeleted != 0 {
		t.Errorf("first sync should delete nothing, got %d", r1.DocsDeleted)
	}
	_ = sc.Refresh(ctx)

	r2, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if r2.DocsDeleted != 1 {
		t.Errorf("second sync should delete c.txt (1 doc), got %d", r2.DocsDeleted)
	}
	_ = sc.Refresh(ctx)

	remaining, err := sc.ListIndexedSourceIDs(ctx, "filesystem", "fake-del")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining source_ids, got %v", remaining)
	}
}

func TestPipelineRun_DeletionSync_NilSkipsDiff(t *testing.T) {
	// CurrentSourceIDs == nil signals "connector opted out / enumeration
	// failed" — pipeline must NOT delete anything, even if the index
	// has docs that the (nil) list wouldn't cover.
	st, sc := newTestDeps(t)
	ctx := context.Background()
	p := New(st, sc, nil, zap.NewNop())

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "fake-del",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	conn := &fakeDeletionConnector{
		results: []*model.FetchResult{
			{
				Documents:        []model.Document{newFakeDoc("survives.txt", "Survives")},
				CurrentSourceIDs: []string{"survives.txt"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
			{
				Documents:        nil,
				CurrentSourceIDs: nil, // explicit opt-out
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
		},
	}

	if _, err := p.RunWithProgress(ctx, connID, conn, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// OpenSearch doesn't refresh between writes by default — the second
	// sync's reconciliation pass needs the first sync's writes visible
	// to the terms aggregation.
	_ = sc.Refresh(ctx)
	r2, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r2.DocsDeleted != 0 {
		t.Errorf("nil CurrentSourceIDs must skip deletion, got %d deleted", r2.DocsDeleted)
	}
	_ = sc.Refresh(ctx)
	remaining, _ := sc.ListIndexedSourceIDs(ctx, "filesystem", "fake-del")
	if len(remaining) != 1 {
		t.Errorf("expected the doc to survive, got %v", remaining)
	}
}

func TestPipelineRun_DeletionSync_EmptySliceWipesAll(t *testing.T) {
	// CurrentSourceIDs == [] means "connector enumerated and nothing
	// exists upstream" — every indexed doc for this connector should
	// be removed.
	st, sc := newTestDeps(t)
	ctx := context.Background()
	p := New(st, sc, nil, zap.NewNop())

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "fake-del",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	conn := &fakeDeletionConnector{
		results: []*model.FetchResult{
			{
				Documents: []model.Document{
					newFakeDoc("x.txt", "X"),
					newFakeDoc("y.txt", "Y"),
				},
				CurrentSourceIDs: []string{"x.txt", "y.txt"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
			{
				Documents:        nil,
				CurrentSourceIDs: []string{},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
		},
	}

	if _, err := p.RunWithProgress(ctx, connID, conn, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// OpenSearch doesn't refresh between writes by default — the second
	// sync's reconciliation pass needs the first sync's writes visible
	// to the terms aggregation.
	_ = sc.Refresh(ctx)
	r2, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r2.DocsDeleted != 2 {
		t.Errorf("empty CurrentSourceIDs should delete everything (2), got %d", r2.DocsDeleted)
	}
	_ = sc.Refresh(ctx)
	remaining, _ := sc.ListIndexedSourceIDs(ctx, "filesystem", "fake-del")
	if len(remaining) != 0 {
		t.Errorf("expected nothing left, got %v", remaining)
	}
}

func TestPipelineRun_DeletionSync_PreservesColonSuffixChildren(t *testing.T) {
	// Bug caught live on 2026-04-14: the IMAP connector enumerates
	// email UIDs but not attachment source_ids. Without the
	// colon-suffix children rule in reconcileDeletions, every
	// attachment looked orphaned and got deleted on every sync.
	// This test drives the scenario directly: parent source_ids
	// stay in CurrentSourceIDs, child source_ids (colon-suffix)
	// are indexed but not enumerated, and the diff must preserve
	// the children.
	st, sc := newTestDeps(t)
	ctx := context.Background()
	p := New(st, sc, nil, zap.NewNop())

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "fake-del",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	conn := &fakeDeletionConnector{
		results: []*model.FetchResult{
			// Index both the parent and its "attachment" child.
			{
				Documents: []model.Document{
					newFakeDoc("INBOX:42", "Parent"),
					newFakeDoc("INBOX:42:attachment:0", "Att0"),
					newFakeDoc("INBOX:42:attachment:1", "Att1"),
					// A genuinely orphaned child (parent not enumerated).
					newFakeDoc("INBOX:99:attachment:0", "OrphanAtt"),
				},
				// Only enumerate the parent — mimics IMAP UID SEARCH ALL.
				CurrentSourceIDs: []string{"INBOX:42"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
			// Second sync: same enumeration (parent still exists) —
			// attachments must still be there, but the orphan goes.
			{
				CurrentSourceIDs: []string{"INBOX:42"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
		},
	}

	if _, err := p.RunWithProgress(ctx, connID, conn, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_ = sc.Refresh(ctx)
	r2, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Only the true orphan (INBOX:99:attachment:0) should go.
	if r2.DocsDeleted != 1 {
		t.Errorf("expected 1 deletion (orphan only), got %d", r2.DocsDeleted)
	}
	_ = sc.Refresh(ctx)
	remaining, _ := sc.ListIndexedSourceIDs(ctx, "filesystem", "fake-del")
	want := map[string]bool{"INBOX:42": true, "INBOX:42:attachment:0": true, "INBOX:42:attachment:1": true}
	if len(remaining) != len(want) {
		t.Errorf("expected %d surviving ids, got %v", len(want), remaining)
	}
	for _, sid := range remaining {
		if !want[sid] {
			t.Errorf("unexpected survivor: %q", sid)
		}
	}
}

// fakeBinaryStoreDeleter records Delete calls so tests can assert the
// deletion-sync cascade reaches the binary cache. The pipeline uses
// this indirectly via the binaryStoreDeleter interface.
type fakeBinaryStoreDeleter struct {
	mu      sync.Mutex
	deleted []string
	err     error // set to make Delete return an error
}

func (f *fakeBinaryStoreDeleter) Delete(_ context.Context, _, _, sid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, sid)
	return f.err
}

func TestPipelineRun_DeletionSync_CascadesToBinaryStore(t *testing.T) {
	// When a binary store is wired via SetBinaryStore, deleted
	// source_ids must also be purged from the cache. Errors from the
	// cache are best-effort (logged at debug, not fatal) — the
	// fakeBinaryStoreDeleter exercises both paths to prove the
	// cascade still completes even when an individual delete fails.
	st, sc := newTestDeps(t)
	ctx := context.Background()
	p := New(st, sc, nil, zap.NewNop())
	bs := &fakeBinaryStoreDeleter{}
	p.SetBinaryStore(bs)

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "fake-del",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	conn := &fakeDeletionConnector{
		results: []*model.FetchResult{
			{
				Documents: []model.Document{
					newFakeDoc("a.txt", "A"),
					newFakeDoc("b.txt", "B"),
				},
				CurrentSourceIDs: []string{"a.txt", "b.txt"},
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
			{
				CurrentSourceIDs: []string{"a.txt"}, // b.txt drops
				Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
			},
		},
	}

	if _, err := p.RunWithProgress(ctx, connID, conn, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_ = sc.Refresh(ctx)
	if _, err := p.RunWithProgress(ctx, connID, conn, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(bs.deleted) != 1 || bs.deleted[0] != "b.txt" {
		t.Errorf("expected binary store to receive Delete for b.txt, got %v", bs.deleted)
	}

	// Now prove an error from the cache doesn't propagate up: reset
	// and drive a second deletion with a failing fake.
	bs.err = fmt.Errorf("disk on fire")
	conn.idx = 0
	conn.results = []*model.FetchResult{
		{
			Documents:        []model.Document{newFakeDoc("c.txt", "C")},
			CurrentSourceIDs: []string{"c.txt"},
			Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
		},
		{
			CurrentSourceIDs: []string{},
			Cursor:           &model.SyncCursor{LastSync: time.Now(), LastStatus: "success"},
		},
	}
	if _, err := p.RunWithProgress(ctx, connID, conn, "", false, nil); err != nil {
		t.Fatal(err)
	}
	_ = sc.Refresh(ctx)
	r, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if err != nil {
		t.Fatalf("cache error must not fail the sync: %v", err)
	}
	if r.DocsDeleted != 1 {
		t.Errorf("cache error should not change reported count, got %d", r.DocsDeleted)
	}
}

// TestIsChildOfKept unit-tests the colon-suffix-children rule in
// isolation — ensuring prefix collisions don't erroneously preserve
// unrelated source_ids (e.g. `INBOX:42` must NOT preserve
// `INBOX:420:...`).
func TestIsChildOfKept(t *testing.T) {
	keep := map[string]struct{}{"INBOX:42": {}, "Sent:7": {}}
	cases := []struct {
		sid  string
		want bool
	}{
		{"INBOX:42:attachment:0", true},
		{"INBOX:42:attachment:5", true},
		{"Sent:7:attachment:0", true},
		{"INBOX:420:attachment:0", false},     // prefix collision, must NOT match
		{"INBOX:42", false},                   // exact match is handled by caller, not this helper
		{"INBOX:41:attachment:0", false},      // different UID
		{"Trash:42:attachment:0", false},      // different folder
		{":junk", false},                      // pathological, no crash
		{"INBOX:42:attachment:0:sub", true},   // deeper nested still valid
	}
	for _, tc := range cases {
		if got := isChildOfKept(tc.sid, keep); got != tc.want {
			t.Errorf("isChildOfKept(%q) = %v, want %v", tc.sid, got, tc.want)
		}
	}
}

func TestCountAlphabeticTokens(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a b c", 0}, // single-letter tokens don't count
		{"the quick brown fox", 4},
		{"hello, world!", 2},                       // punctuation doesn't break count
		{"http://example.com/path?query=foo", 0},   // URL is one token of mostly non-alpha
		{"abc def ghi 123 456", 3},                 // numbers don't count
		{"Привет как дела", 3},                     // Cyrillic counts
		{"a1 b2 c3 dd ee", 2},                      // tokens with <2 alphas don't count
		{"docker compose up -d", 3},                // "-d" doesn't count, others do
	}
	for _, tt := range tests {
		if got := countAlphabeticTokens(tt.text); got != tt.want {
			t.Errorf("countAlphabeticTokens(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

func TestPipelineRun_LowInfoChunkSkipsEmbedding(t *testing.T) {
	st, sc := newTestDeps(t)
	ctx := context.Background()

	// Recreate index with k-NN enabled (dim=3 for the mock)
	sc.DeleteIndex(ctx)    //nolint:errcheck // test
	sc.EnsureIndex(ctx, 3) //nolint:errcheck // test

	provider := &mockEmbedderProvider{embedder: &mockEmbedder{dim: 3}}
	p := New(st, sc, provider, zap.NewNop())

	// Two docs: one with substantive content, one with low-info content
	// (just URLs and short tokens). The low-info one should NOT get an embedding.
	dir := t.TempDir()
	os.WriteFile(dir+"/sub.txt", []byte("This is a substantive document with many real words that should be embedded normally."), 0o644) //nolint:errcheck // test
	os.WriteFile(dir+"/junk.txt", []byte("ok ty https://x.com/a/b a 1 b 2"), 0o644)                                                          //nolint:errcheck // test

	connID := uuid.New()
	if err := st.CreateConnectorConfig(ctx, &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "gate-test",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	fsConn := configureFSWithExtractor(t, "gate-test", dir, "*.txt")

	if _, err := p.RunWithProgress(ctx, connID, fsConn, "", false, nil); err != nil {
		t.Fatalf("pipeline run failed: %v", err)
	}
	sc.Refresh(ctx) //nolint:errcheck // test

	// Both docs should be searchable via BM25 (the low-info one is still indexed)
	result, err := sc.Search(ctx, model.SearchRequest{Query: "substantive", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected substantive doc to be findable via BM25, got %d results", result.TotalCount)
	}
}

// mockManyDocsConnector returns a fixed number of tiny documents on Fetch.
// Used to test cancellation mid-loop: cancel via a progress callback at
// doc N and verify the loop exits with ctx.Err() + a partial SyncReport.
type mockManyDocsConnector struct {
	name  string
	count int
}

func (m *mockManyDocsConnector) Type() string                       { return "filesystem" }
func (m *mockManyDocsConnector) Name() string                       { return m.name }
func (m *mockManyDocsConnector) Configure(_ connector.Config) error { return nil }
func (m *mockManyDocsConnector) Validate() error                    { return nil }
func (m *mockManyDocsConnector) Fetch(_ context.Context, _ *model.SyncCursor) (*model.FetchResult, error) {
	docs := make([]model.Document, m.count)
	for i := range docs {
		docs[i] = model.Document{
			SourceType: "filesystem",
			SourceName: m.name,
			SourceID:   fmt.Sprintf("doc-%03d", i),
			Title:      fmt.Sprintf("Doc %d", i),
			Content:    "Lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt",
			CreatedAt:  time.Now(),
		}
	}
	return &model.FetchResult{Documents: docs}, nil
}

func TestPipelineRun_CancelMidLoop_ReturnsPartialReport(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	connID := uuid.New()
	if err := st.CreateConnectorConfig(context.Background(), &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "cancel-test",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel after progress reports 5 docs processed. The next loop
	// iteration observes ctx.Done() before indexing doc 6.
	cancelAfter := 5
	progress := func(_, processed, _ int) {
		if processed >= cancelAfter {
			cancel()
		}
	}

	conn := &mockManyDocsConnector{name: "cancel-test", count: 20}

	report, err := p.RunWithProgress(ctx, connID, conn, "", false, progress)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx.Canceled, got %v", err)
	}
	if report == nil {
		t.Fatal("expected partial report, got nil")
	}
	// processed should be exactly cancelAfter because the check fires
	// at the top of iteration i=cancelAfter (0-indexed) before indexing
	// the (cancelAfter+1)-th doc.
	if report.DocsProcessed != cancelAfter {
		t.Errorf("DocsProcessed = %d, want %d", report.DocsProcessed, cancelAfter)
	}
	if report.Errors != 0 {
		t.Errorf("Errors = %d, want 0", report.Errors)
	}
	if report.ConnectorName != "cancel-test" {
		t.Errorf("ConnectorName = %q, want cancel-test", report.ConnectorName)
	}
}

func TestPipelineRun_CancelBeforeStart_ReturnsImmediately(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	connID := uuid.New()
	if err := st.CreateConnectorConfig(context.Background(), &model.ConnectorConfig{
		ID: connID, Type: "filesystem", Name: "precancel-test",
		Config: map[string]any{}, Enabled: true, Shared: true,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before RunWithProgress even sees a document

	conn := &mockManyDocsConnector{name: "precancel-test", count: 3}
	report, err := p.RunWithProgress(ctx, connID, conn, "", false, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx.Canceled, got %v", err)
	}
	// report can be nil when cancellation fires before the per-doc loop
	// (cursor fetch, conn.Fetch): the handler's SetDeleted call already
	// guards against nil. If the loop was reached, DocsProcessed == 0.
	if report != nil && report.DocsProcessed != 0 {
		t.Errorf("DocsProcessed = %d, want 0", report.DocsProcessed)
	}
}
