//go:build integration

package pipeline

import (
	"context"
	"os"
	"testing"

	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	tdb := testutil.NewTestDB(t, "pipeline", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestPipelineRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := New(st, zap.NewNop())

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
	if report.ConnectorName != "pipeline-test" {
		t.Errorf("expected connector name 'pipeline-test', got %q", report.ConnectorName)
	}

	// Verify documents are searchable
	result, err := st.Search(ctx, model.SearchRequest{Query: "xylophone", Limit: 10})
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
	if cursor.ItemsSynced != 2 {
		t.Errorf("expected cursor items_synced=2, got %d", cursor.ItemsSynced)
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
