//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

func TestPipelineRun_CursorError(t *testing.T) {
	st, sc := newTestDeps(t)

	// Close store to trigger cursor error
	st.Close()

	p := New(st, sc, nil, zap.NewNop())

	dir := t.TempDir()
	os.WriteFile(dir+"/test.txt", []byte("test"), 0o644) //nolint:errcheck // test file

	fsConn, _ := connector.Create("filesystem")
	_ = fsConn.Configure(connector.Config{
		"name": "error-test", "root_path": dir, "patterns": "*.txt",
	})

	_, err := p.RunWithProgress(context.Background(), uuid.New(), fsConn, "", false, nil)
	if err == nil {
		t.Fatal("expected error from closed store")
	}
}

func TestPipelineRun_FetchError(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	fsConn, _ := connector.Create("filesystem")
	_ = fsConn.Configure(connector.Config{
		"name": "fetch-error", "root_path": "/nonexistent/path/surely", "patterns": "*.txt",
	})

	_, err := p.RunWithProgress(context.Background(), uuid.New(), fsConn, "", false, nil)
	if err == nil {
		t.Fatal("expected error from fetch")
	}
}

// stubConnector lets tests inject deterministic Fetch results without touching the filesystem.
type stubConnector struct {
	name   string
	docs   []model.Document
	cursor *model.SyncCursor
	err    error
}

func (s *stubConnector) Type() string                       { return "stub" }
func (s *stubConnector) Name() string                       { return s.name }
func (s *stubConnector) Configure(_ connector.Config) error { return nil }
func (s *stubConnector) Validate() error                    { return nil }
func (s *stubConnector) Fetch(_ context.Context, _ *model.SyncCursor) (*model.FetchResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &model.FetchResult{Documents: s.docs, Cursor: s.cursor}, nil
}

// failingEmbedder implements the embedding.Embedder interface but always fails.
type failingEmbedder struct{ dim int }

func (f *failingEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, fmt.Errorf("embed: simulated failure")
}
func (f *failingEmbedder) Dimension() int { return f.dim }

func TestPipelineRun_NilStore(t *testing.T) {
	_, sc := newTestDeps(t)
	p := New(nil, sc, nil, zap.NewNop())
	stub := &stubConnector{name: "nil-store"}

	_, err := p.RunWithProgress(context.Background(), uuid.New(), stub, "", false, nil)
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
}

func TestPipelineRun_EmbedderFailureStillIndexes(t *testing.T) {
	st, sc := newTestDeps(t)
	provider := &mockEmbedderProvider{embedder: &failingEmbedder{dim: 3}}
	p := New(st, sc, provider, zap.NewNop())

	stub := &stubConnector{
		name: "embed-fail",
		docs: []model.Document{
			{
				SourceType: "stub", SourceName: "embed-fail", SourceID: "doc1",
				Title:   "Doc 1",
				Content: "this is content long enough to be embedded by the pipeline embedder check",
				Metadata: map[string]any{}, CreatedAt: time.Now(),
			},
		},
	}

	report, err := p.RunWithProgress(context.Background(), uuid.New(), stub, "owner-x", false, nil)
	if err != nil {
		t.Fatalf("RunWithProgress: %v", err)
	}
	if report.DocsProcessed != 1 {
		t.Errorf("DocsProcessed = %d, want 1", report.DocsProcessed)
	}
	if report.Errors != 0 {
		t.Errorf("expected 0 errors (embed failure should not increment), got %d", report.Errors)
	}

	// Doc should still be searchable in OpenSearch — embedding just isn't set
	sc.Refresh(context.Background()) //nolint:errcheck // test
	result, err := sc.Search(context.Background(), model.SearchRequest{Query: "embedder check", Limit: 10, OwnerID: "owner-x"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 indexed result, got %d", result.TotalCount)
	}
}

func TestPipelineRun_OwnershipPropagation(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	stub := &stubConnector{
		name: "ownership-test",
		docs: []model.Document{
			{
				SourceType: "stub", SourceName: "ownership-test", SourceID: "doc1",
				Title: "Owned", Content: "uniquetestword content for the ownership test",
				Metadata: map[string]any{}, CreatedAt: time.Now(),
			},
		},
	}

	if _, err := p.RunWithProgress(context.Background(), uuid.New(), stub, "alice", false, nil); err != nil {
		t.Fatal(err)
	}
	sc.Refresh(context.Background()) //nolint:errcheck // test

	// alice should see her own doc
	res, err := sc.Search(context.Background(), model.SearchRequest{Query: "uniquetestword", Limit: 10, OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 1 {
		t.Errorf("alice should see 1 doc, got %d", res.TotalCount)
	}

	// bob should not (private)
	res, err = sc.Search(context.Background(), model.SearchRequest{Query: "uniquetestword", Limit: 10, OwnerID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 0 {
		t.Errorf("bob should see 0 docs (private), got %d", res.TotalCount)
	}
}

func TestPipelineRun_SharedPropagation(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	stub := &stubConnector{
		name: "shared-test",
		docs: []model.Document{
			{
				SourceType: "stub", SourceName: "shared-test", SourceID: "doc1",
				Title: "Shared", Content: "anotheruniqueword content for the shared test",
				Metadata: map[string]any{}, CreatedAt: time.Now(),
			},
		},
	}

	if _, err := p.RunWithProgress(context.Background(), uuid.New(), stub, "", true, nil); err != nil {
		t.Fatal(err)
	}
	sc.Refresh(context.Background()) //nolint:errcheck // test

	// any user should see it via the shared clause
	for _, owner := range []string{"alice", "bob", "charlie"} {
		res, err := sc.Search(context.Background(), model.SearchRequest{Query: "anotheruniqueword", Limit: 10, OwnerID: owner})
		if err != nil {
			t.Fatal(err)
		}
		if res.TotalCount != 1 {
			t.Errorf("%s should see shared doc, got %d", owner, res.TotalCount)
		}
	}
}

func TestPipelineRun_ProgressCallback(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	stub := &stubConnector{
		name: "progress-test",
		docs: []model.Document{
			{SourceType: "stub", SourceName: "progress-test", SourceID: "1", Title: "1", Content: "first", Metadata: map[string]any{}, CreatedAt: time.Now()},
			{SourceType: "stub", SourceName: "progress-test", SourceID: "2", Title: "2", Content: "second", Metadata: map[string]any{}, CreatedAt: time.Now()},
			{SourceType: "stub", SourceName: "progress-test", SourceID: "3", Title: "3", Content: "third", Metadata: map[string]any{}, CreatedAt: time.Now()},
		},
	}

	var calls []struct{ total, processed, errors int }
	progress := func(total, processed, errors int) {
		calls = append(calls, struct{ total, processed, errors int }{total, processed, errors})
	}

	if _, err := p.RunWithProgress(context.Background(), uuid.New(), stub, "", true, progress); err != nil {
		t.Fatal(err)
	}

	// We expect 4 calls: initial (0/3) + one per doc
	if len(calls) != 4 {
		t.Errorf("expected 4 progress calls, got %d", len(calls))
	}
	if calls[0].processed != 0 || calls[0].total != 3 {
		t.Errorf("first call: expected (3,0), got (%d,%d)", calls[0].total, calls[0].processed)
	}
	if calls[len(calls)-1].processed != 3 {
		t.Errorf("final call: expected processed=3, got %d", calls[len(calls)-1].processed)
	}
}
