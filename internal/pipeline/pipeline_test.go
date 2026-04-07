//go:build integration

package pipeline

import (
	"context"
	"os"
	"testing"

	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func newTestDeps(t *testing.T) (*store.Store, *search.Client) {
	t.Helper()
	tdb := testutil.NewTestDB(t, "pipeline", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	osURL, osIndex := testutil.TestOSConfig(t, "pipeline")
	sc, err := search.NewWithIndex(context.Background(), osURL, osIndex, nil)
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

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
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

	fsConn, _ := connector.Create("filesystem")
	_ = fsConn.Configure(connector.Config{
		"name": "embed-test", "root_path": dir, "patterns": "*.txt",
	})

	report, err := p.Run(ctx, fsConn)
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

	fsConn, err := connector.Create("filesystem")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := fsConn.Configure(connector.Config{
		"name":      "pipeline-test",
		"root_path": dir,
		"patterns":  "*.txt,*.md",
	}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	// First sync
	report, err := p.Run(ctx, fsConn)
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

	// Verify cursor was saved
	cursor, err := st.GetSyncCursor(ctx, "pipeline-test")
	if err != nil {
		t.Fatalf("get cursor failed: %v", err)
	}
	if cursor == nil {
		t.Fatal("expected cursor to be saved")
	}

	// Second sync (incremental — no new files)
	report2, err := p.Run(ctx, fsConn)
	if err != nil {
		t.Fatalf("second pipeline run failed: %v", err)
	}
	if report2.DocsProcessed != 0 {
		t.Errorf("expected 0 docs on incremental sync, got %d", report2.DocsProcessed)
	}
}
