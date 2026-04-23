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

// stubConnector streams deterministic docs without touching the filesystem.
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
func (s *stubConnector) Fetch(ctx context.Context, _ *model.SyncCursor) (<-chan model.FetchItem, <-chan error) {
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		if s.err != nil {
			errs <- s.err
			return
		}
		for i := range s.docs {
			d := s.docs[i]
			select {
			case items <- model.FetchItem{Doc: &d}:
			case <-ctx.Done():
				return
			}
		}
		if s.cursor != nil {
			select {
			case items <- model.FetchItem{Checkpoint: s.cursor}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return items, errs
}

// failingEmbedder implements the embedding.Embedder interface but always fails.
type failingEmbedder struct{ dim int }

func (f *failingEmbedder) Embed(_ context.Context, _ []string, _ string) ([][]float32, error) {
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
				Title:    "Doc 1",
				Content:  "this is content long enough to be embedded by the pipeline embedder check",
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

	res, err := sc.Search(context.Background(), model.SearchRequest{Query: "uniquetestword", Limit: 10, OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 1 {
		t.Errorf("alice should see 1 doc, got %d", res.TotalCount)
	}

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

// TestPipelineRun_StreamErrorSurfacedViaErrChannel covers the path
// where a connector streams partial results and then signals failure
// via its error channel. The pipeline must index everything that
// made it through, skip deletion reconciliation (since the
// enumeration was incomplete), and return the terminal error.
func TestPipelineRun_StreamErrorSurfacedViaErrChannel(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	conn := &streamingErrorConnector{
		name: "err-stream",
		docs: []model.Document{
			{SourceType: "stub", SourceName: "err-stream", SourceID: "doc1",
				Title: "partial", Content: "partial content", Metadata: map[string]any{}, CreatedAt: time.Now()},
		},
		err: fmt.Errorf("simulated fetch tail error"),
	}
	report, err := p.RunWithProgress(context.Background(), uuid.New(), conn, "", true, nil)
	if err == nil || !strContains(err.Error(), "simulated fetch tail error") {
		t.Fatalf("expected tail error, got %v", err)
	}
	if report == nil {
		t.Fatal("expected partial report, got nil")
	}
	if report.DocsProcessed != 1 {
		t.Errorf("DocsProcessed = %d, want 1 (the partial doc)", report.DocsProcessed)
	}
}

// streamingErrorConnector emits docs then signals a terminal error
// on its error channel. Used to cover the consumeStream→drainErr
// path where items close cleanly but errs carries a failure.
type streamingErrorConnector struct {
	name string
	docs []model.Document
	err  error
}

func (s *streamingErrorConnector) Type() string                       { return "stub" }
func (s *streamingErrorConnector) Name() string                       { return s.name }
func (s *streamingErrorConnector) Configure(_ connector.Config) error { return nil }
func (s *streamingErrorConnector) Validate() error                    { return nil }
func (s *streamingErrorConnector) Fetch(ctx context.Context, _ *model.SyncCursor) (<-chan model.FetchItem, <-chan error) {
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		// Send items unbuffered so the consumer has drained each
		// one before the terminal error is enqueued — keeps the
		// test deterministic vs. select's randomized choice when
		// both channels are ready simultaneously.
		for i := range s.docs {
			d := s.docs[i]
			select {
			case items <- model.FetchItem{Doc: &d}:
			case <-ctx.Done():
				return
			}
		}
		errs <- s.err
	}()
	return items, errs
}

// strContains is inlined (rather than importing "strings") because
// errors_test.go already has a lean import list; keeping the
// dependency footprint minimal.
func strContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestPipelineRun_EstimatedTotalPath covers the progress-estimation
// branch in handleItem: a connector emits EstimatedTotal ahead of
// any doc, bumping the pipeline's `total` before `processed` starts
// growing.
func TestPipelineRun_EstimatedTotalPath(t *testing.T) {
	st, sc := newTestDeps(t)
	p := New(st, sc, nil, zap.NewNop())

	conn := &estimatingConnector{name: "est-test", estimates: []int64{10, 42}, docs: []model.Document{
		{SourceType: "stub", SourceName: "est-test", SourceID: "doc1",
			Title: "T", Content: "content for estimate test",
			Metadata: map[string]any{}, CreatedAt: time.Now()},
	}}

	var maxTotal int
	progress := func(total, _, _ int, _ string) {
		if total > maxTotal {
			maxTotal = total
		}
	}

	if _, err := p.RunWithProgress(context.Background(), uuid.New(), conn, "", true, progress); err != nil {
		t.Fatal(err)
	}
	// UI should have seen at least the highest EstimatedTotal the
	// connector reported.
	if maxTotal < 42 {
		t.Errorf("expected progress total to reach at least 42, got %d", maxTotal)
	}
}

// estimatingConnector emits a sequence of EstimatedTotal items, then
// a doc, covering the handleItem EstimatedTotal branch.
type estimatingConnector struct {
	name      string
	estimates []int64
	docs      []model.Document
}

func (e *estimatingConnector) Type() string                       { return "stub" }
func (e *estimatingConnector) Name() string                       { return e.name }
func (e *estimatingConnector) Configure(_ connector.Config) error { return nil }
func (e *estimatingConnector) Validate() error                    { return nil }
func (e *estimatingConnector) Fetch(ctx context.Context, _ *model.SyncCursor) (<-chan model.FetchItem, <-chan error) {
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		for i := range e.estimates {
			v := e.estimates[i]
			select {
			case items <- model.FetchItem{EstimatedTotal: &v}:
			case <-ctx.Done():
				return
			}
		}
		for i := range e.docs {
			d := e.docs[i]
			select {
			case items <- model.FetchItem{Doc: &d}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return items, errs
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
	progress := func(total, processed, errors int, _ string) {
		calls = append(calls, struct{ total, processed, errors int }{total, processed, errors})
	}

	if _, err := p.RunWithProgress(context.Background(), uuid.New(), stub, "", true, progress); err != nil {
		t.Fatal(err)
	}

	// Streaming pipeline reports progress at least twice: once
	// initially (0/0/0) and once after the final bulk flush.
	// Exact call count depends on bulk-flush cadence, so we just
	// assert the final state: all 3 docs processed, no errors.
	if len(calls) < 2 {
		t.Errorf("expected at least 2 progress calls, got %d", len(calls))
	}
	final := calls[len(calls)-1]
	if final.processed != 3 {
		t.Errorf("final progress: expected processed=3, got %d", final.processed)
	}
	if final.errors != 0 {
		t.Errorf("final progress: expected errors=0, got %d", final.errors)
	}
}
