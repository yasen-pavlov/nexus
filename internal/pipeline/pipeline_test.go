//go:build integration

package pipeline

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/embedding"
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
		extConn.SetExtractor(extractor.NewRegistry(""))
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
