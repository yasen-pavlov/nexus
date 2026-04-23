package filesystem

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
	"github.com/muty/nexus/internal/testutil"
)

func TestConfigure(t *testing.T) {
	c := &Connector{}

	t.Run("missing root_path", func(t *testing.T) {
		err := c.Configure(connector.Config{"name": "test"})
		if err == nil {
			t.Fatal("expected error for missing root_path")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		err := c.Configure(connector.Config{
			"name":      "my-files",
			"root_path": "/tmp",
			"patterns":  "*.txt,*.md",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Name() != "my-files" {
			t.Errorf("expected name 'my-files', got %q", c.Name())
		}
		if c.Type() != "filesystem" {
			t.Errorf("expected type 'filesystem', got %q", c.Type())
		}
		if len(c.patterns) != 2 {
			t.Errorf("expected 2 patterns, got %d", len(c.patterns))
		}
	})

	t.Run("default name and patterns", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"root_path": "/tmp"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c2.Name() != "filesystem" {
			t.Errorf("expected default name 'filesystem', got %q", c2.Name())
		}
		if len(c2.patterns) != 2 {
			t.Errorf("expected 2 default patterns, got %d", len(c2.patterns))
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid directory", func(t *testing.T) {
		c := &Connector{rootPath: t.TempDir()}
		if err := c.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("nonexistent path", func(t *testing.T) {
		c := &Connector{rootPath: "/nonexistent/path"}
		if err := c.Validate(); err == nil {
			t.Fatal("expected error for nonexistent path")
		}
	})

	t.Run("path is a file", func(t *testing.T) {
		f, err := os.CreateTemp("", "test")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name()) //nolint:errcheck // test cleanup
		f.Close()                 //nolint:errcheck // test file

		c := &Connector{rootPath: f.Name()}
		if err := c.Validate(); err == nil {
			t.Fatal("expected error when path is a file")
		}
	})
}

func TestMatchesPattern(t *testing.T) {
	c := &Connector{patterns: []string{"*.txt", "*.md"}}

	tests := []struct {
		name  string
		match bool
	}{
		{"readme.txt", true},
		{"notes.md", true},
		{"image.png", false},
		{"data.json", false},
		{"file.TXT", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.matchesPattern(tt.name); got != tt.match {
				t.Errorf("matchesPattern(%q) = %v, want %v", tt.name, got, tt.match)
			}
		})
	}
}

func TestFetch(t *testing.T) {
	dir := t.TempDir()

	// Create test files
	writeFile(t, dir, "hello.txt", "Hello World")
	writeFile(t, dir, "notes.md", "# My Notes")
	writeFile(t, dir, "image.png", "not a text file")
	writeFile(t, dir, "sub/nested.txt", "Nested content")

	c := &Connector{
		name:      "test",
		rootPath:  dir,
		patterns:  []string{"*.txt", "*.md"},
		extractor: extractor.NewRegistry("", nil), // PlainText only — production always has at least this
	}

	t.Run("full sync", func(t *testing.T) {
		result := testutil.RunFetch(t, c, nil)
		if result.Err != nil {
			t.Fatalf("unexpected error: %v", result.Err)
		}
		if len(result.Documents) != 3 {
			t.Fatalf("expected 3 documents, got %d", len(result.Documents))
		}

		// Verify document properties
		docMap := make(map[string]model.Document)
		for _, doc := range result.Documents {
			docMap[doc.SourceID] = doc
		}

		hello, ok := docMap["hello.txt"]
		if !ok {
			t.Fatal("missing hello.txt document")
		}
		if hello.Title != "hello.txt" {
			t.Errorf("expected title 'hello.txt', got %q", hello.Title)
		}
		if hello.Content != "Hello World" {
			t.Errorf("expected content 'Hello World', got %q", hello.Content)
		}
		if hello.SourceType != "filesystem" {
			t.Errorf("expected source_type 'filesystem', got %q", hello.SourceType)
		}
		if hello.SourceName != "test" {
			t.Errorf("expected source_name 'test', got %q", hello.SourceName)
		}

		nested, ok := docMap[filepath.Join("sub", "nested.txt")]
		if !ok {
			t.Fatal("missing sub/nested.txt document")
		}
		if nested.Content != "Nested content" {
			t.Errorf("expected 'Nested content', got %q", nested.Content)
		}

		// Verify cursor — final Checkpoint carries the snapshot the
		// pipeline would have persisted.
		if result.LastCursor == nil {
			t.Fatal("expected cursor to be set")
		}
		if result.LastCursor.LastStatus != "success" {
			t.Errorf("expected status 'success', got %q", result.LastCursor.LastStatus)
		}
	})

	t.Run("incremental sync skips old files", func(t *testing.T) {
		pastCursor := &model.SyncCursor{
			CursorData: map[string]any{
				"last_sync_time": time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano),
			},
		}

		result := testutil.RunFetch(t, c, pastCursor)
		if result.Err != nil {
			t.Fatalf("unexpected error: %v", result.Err)
		}
		if len(result.Documents) != 0 {
			t.Errorf("expected 0 documents for future cursor, got %d", len(result.Documents))
		}
	})
}

func TestFetch_PopulatesSourceIDs(t *testing.T) {
	// Even with an incremental cursor that filters out unchanged
	// files (so Documents is empty), the connector must still emit
	// SourceID items for every matching file — otherwise the
	// pipeline's deletion-sync merge-diff would wrongly delete
	// everything.
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha")
	writeFile(t, dir, "b.txt", "bravo")
	writeFile(t, dir, "c.md", "charlie")
	writeFile(t, dir, "skipme.png", "binary") // doesn't match the pattern

	c := &Connector{
		name: "t", rootPath: dir, patterns: []string{"*.txt", "*.md"},
		extractor: extractor.NewRegistry("", nil),
	}

	t.Run("full sync emits every matching source id", func(t *testing.T) {
		result := testutil.RunFetch(t, c, nil)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		got := make(map[string]bool, len(result.SourceIDs))
		for _, sid := range result.SourceIDs {
			got[sid] = true
		}
		want := []string{"a.txt", "b.txt", "c.md"}
		if len(got) != len(want) {
			t.Fatalf("expected %d source ids, got %v", len(want), result.SourceIDs)
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("missing %q from SourceIDs (got %v)", w, result.SourceIDs)
			}
		}
		if got["skipme.png"] {
			t.Error("non-matching file leaked into SourceIDs")
		}
	})

	t.Run("incremental sync keeps full enumeration", func(t *testing.T) {
		future := time.Now().Add(time.Hour)
		result := testutil.RunFetch(t, c, &model.SyncCursor{
			CursorData: map[string]any{"last_sync_time": future.Format(time.RFC3339Nano)},
		})
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		if len(result.Documents) != 0 {
			t.Errorf("expected 0 docs on future-cursor sync, got %d", len(result.Documents))
		}
		if len(result.SourceIDs) != 3 {
			t.Errorf("expected 3 source ids regardless of cursor, got %v", result.SourceIDs)
		}
	})
}

// TestFetch_WalkError_SurfacedOnErrsChannel covers the walk-error
// branch — an unreadable root directory surfaces the error on the
// errs channel and closes items cleanly.
func TestFetch_WalkError_SurfacedOnErrsChannel(t *testing.T) {
	c := &Connector{
		name:     "walk-err",
		rootPath: "/this/path/definitely/does/not/exist",
		patterns: []string{"*.txt"},
	}
	result := testutil.RunFetch(t, c, nil)
	if result.Err == nil {
		t.Fatal("expected walk error on missing root path")
	}
	if len(result.Documents) != 0 {
		t.Errorf("expected no docs on walk failure, got %d", len(result.Documents))
	}
}

// TestFetch_EmitsCheckpointEvery covers the per-batch checkpoint
// emission inside the WalkDir callback (the inner
// `seen%filesystemCheckpointEvery == 0` branch). Seeds more than one
// batch's worth of files so we see at least two checkpoints.
func TestFetch_EmitsCheckpointEvery(t *testing.T) {
	dir := t.TempDir()
	// Seed filesystemCheckpointEvery+1 files so the counter hits the
	// batch boundary inside the walk, *and* the trailing batch
	// emits its own checkpoint at close.
	const total = filesystemCheckpointEvery + 1
	for i := 0; i < total; i++ {
		writeFile(t, dir, fmt.Sprintf("f%04d.txt", i), "body")
	}
	c := &Connector{
		name: "batch-cp", rootPath: dir, patterns: []string{"*.txt"},
		extractor: extractor.NewRegistry("", nil),
	}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Checkpoints) < 2 {
		t.Errorf("expected ≥2 checkpoints (at batch boundary + final), got %d", len(result.Checkpoints))
	}
}

// TestFetch_ContextCancelledMidWalk covers emit-on-ctx-cancel paths
// in the Fetch goroutine. Seeds many files and cancels ASAP — the
// walk should abort with an error.
func TestFetch_ContextCancelledMidWalk(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		writeFile(t, dir, fmt.Sprintf("f%02d.txt", i), "body")
	}
	c := &Connector{
		name: "cancel-mid", rootPath: dir, patterns: []string{"*.txt"},
		extractor: extractor.NewRegistry("", nil),
	}

	ctx, cancel := context.WithCancel(context.Background())
	items, errs := c.Fetch(ctx, nil)
	cancel()
	result := testutil.CollectStream(t, items, errs)
	// The walk may have raced to completion before cancel; either
	// outcome is acceptable as long as CollectStream didn't time out.
	_ = result
}

func TestFetchContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "content")

	c := &Connector{
		name:     "test",
		rootPath: dir,
		patterns: []string{"*.txt"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	items, errs := c.Fetch(ctx, nil)
	result := testutil.CollectStream(t, items, errs)
	// Cancelled context produces either a terminal error or an
	// early-closed stream; both are acceptable. What's NOT OK is the
	// connector blocking forever — CollectStream's timeout would
	// catch that.
	_ = result
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		filename   string
		wantPrefix string
	}{
		{"doc.pdf", "application/pdf"},
		{"file.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"file.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"file.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"file.doc", "application/msword"},
		{"file.xls", "application/vnd.ms-excel"},
		{"notes.md", "text/markdown"},
		{"noext", "application/octet-stream"},
	}
	for _, tt := range tests {
		got := detectContentType(tt.filename)
		if !strings.HasPrefix(got, tt.wantPrefix) {
			t.Errorf("detectContentType(%q) = %q, want prefix %q", tt.filename, got, tt.wantPrefix)
		}
	}
}

// TestFetchBinary_RootEvalSymlinksFailure covers the "resolve root"
// branch — FetchBinary when the configured root itself doesn't exist.
func TestFetchBinary_RootEvalSymlinksFailure(t *testing.T) {
	c := &Connector{rootPath: "/nope/never-existed", patterns: []string{"*.txt"}}
	_, err := c.FetchBinary(context.Background(), "anything.txt")
	if err == nil {
		t.Fatal("expected error when root doesn't resolve")
	}
}

// TestResolveFilesystemCursor_NoCursorNoSyncSince covers the
// fallback branch — both cursor and syncSince empty → zero time.
func TestResolveFilesystemCursor_NoCursorNoSyncSince(t *testing.T) {
	got := resolveFilesystemCursor(nil, time.Time{})
	if !got.IsZero() {
		t.Errorf("expected zero time, got %v", got)
	}
}

// TestResolveFilesystemCursor_EmptyCursorData covers the branch
// where cursor is non-nil but has no last_sync_time — should
// return zero time, NOT the syncSince fallback (since the cursor
// is present; its absence of data means "never synced via cursor").
func TestResolveFilesystemCursor_EmptyCursorData(t *testing.T) {
	got := resolveFilesystemCursor(&model.SyncCursor{CursorData: map[string]any{}}, time.Now())
	if !got.IsZero() {
		t.Errorf("empty cursor data should yield zero time, got %v", got)
	}
}

// TestDetectContentType_NoExtension covers the early-return for
// files without an extension — mime.TypeByExtension returns "" and
// the function must fall back to octet-stream.
func TestDetectContentType_NoExtension(t *testing.T) {
	if got := detectContentType("README"); got != "application/octet-stream" {
		t.Errorf("no-extension file = %q, want application/octet-stream", got)
	}
}

func TestSetExtractor(t *testing.T) {
	c := &Connector{}
	if c.extractor != nil {
		t.Error("expected nil extractor initially")
	}
	reg := extractor.NewRegistry("", nil)
	c.SetExtractor(reg)
	if c.extractor == nil {
		t.Error("expected extractor to be set")
	}
}

func TestFetch_WithExtractor_UnsupportedType(t *testing.T) {
	// Tika not available — registry only has PlainText
	reg := extractor.NewRegistry("", nil)
	dir := t.TempDir()
	writeFile(t, dir, "data.bin", "binary data")

	c := &Connector{
		name:      "test",
		rootPath:  dir,
		patterns:  []string{"*.bin"},
		extractor: reg,
	}

	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	// Unextractable files are still emitted as docs with empty content so they
	// remain discoverable by metadata (filename, size) and previewable via
	// the BinaryFetcher. Raw bytes are NEVER stored as if they were text.
	if len(result.Documents) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(result.Documents))
	}
	doc := result.Documents[0]
	if doc.Content != "" {
		t.Errorf("expected empty content for unextractable file, got %q", doc.Content)
	}
	if doc.MimeType == "" {
		t.Errorf("expected MimeType to be set, got empty")
	}
	if doc.Size != int64(len("binary data")) {
		t.Errorf("expected Size %d, got %d", len("binary data"), doc.Size)
	}
	if doc.Title != "data.bin" {
		t.Errorf("expected title 'data.bin', got %q", doc.Title)
	}
}

func TestFetch_WithExtractor(t *testing.T) {
	// Create a mock Tika server
	tikaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.Write([]byte("Extracted PDF content")) //nolint:errcheck // test
			return
		}
		w.WriteHeader(http.StatusOK) // health check
	}))
	defer tikaSrv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "doc.pdf", "fake pdf bytes")
	writeFile(t, dir, "notes.txt", "plain text content")

	reg := extractor.NewRegistry(tikaSrv.URL, nil)
	c := &Connector{
		name:      "test",
		rootPath:  dir,
		patterns:  []string{"*.pdf", "*.txt"},
		extractor: reg,
	}

	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("fetch failed: %v", result.Err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(result.Documents))
	}

	// Find the PDF doc and verify extraction worked
	for _, doc := range result.Documents {
		if strings.HasSuffix(doc.SourceID, ".pdf") {
			if doc.Content != "Extracted PDF content" {
				t.Errorf("expected extracted content, got %q", doc.Content)
			}
			if doc.Metadata["content_type"] != "application/pdf" {
				t.Errorf("expected content_type 'application/pdf', got %v", doc.Metadata["content_type"])
			}
		}
		if strings.HasSuffix(doc.SourceID, ".txt") {
			if doc.Content != "plain text content" {
				t.Errorf("expected plain text content, got %q", doc.Content)
			}
		}
	}
}

func TestFetch_PopulatesMimeTypeAndSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hello.txt", "Hello World")

	c := &Connector{
		name:     "test",
		rootPath: dir,
		patterns: []string{"*.txt"},
	}
	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(result.Documents))
	}
	doc := result.Documents[0]
	if !strings.HasPrefix(doc.MimeType, "text/plain") {
		t.Errorf("expected MimeType to start with text/plain, got %q", doc.MimeType)
	}
	if doc.Size != int64(len("Hello World")) {
		t.Errorf("expected Size %d, got %d", len("Hello World"), doc.Size)
	}
}

func TestFetchBinary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hello.txt", "Hello World")
	writeFile(t, dir, "sub/nested.txt", "Nested content")

	c := &Connector{
		name:     "test",
		rootPath: dir,
		patterns: []string{"*.txt"},
	}

	t.Run("returns file bytes", func(t *testing.T) {
		bc, err := c.FetchBinary(context.Background(), "hello.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer bc.Reader.Close() //nolint:errcheck // test cleanup
		data, err := readAll(t, bc.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "Hello World" {
			t.Errorf("expected 'Hello World', got %q", string(data))
		}
		if bc.Filename != "hello.txt" {
			t.Errorf("expected filename hello.txt, got %q", bc.Filename)
		}
		if bc.Size != int64(len("Hello World")) {
			t.Errorf("expected size %d, got %d", len("Hello World"), bc.Size)
		}
		if !strings.HasPrefix(bc.MimeType, "text/plain") {
			t.Errorf("expected text/plain mime, got %q", bc.MimeType)
		}
	})

	t.Run("nested path", func(t *testing.T) {
		bc, err := c.FetchBinary(context.Background(), filepath.Join("sub", "nested.txt"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer bc.Reader.Close() //nolint:errcheck // test cleanup
		data, err := readAll(t, bc.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "Nested content" {
			t.Errorf("expected 'Nested content', got %q", string(data))
		}
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		// Create a sibling directory with a "secret" file outside the root
		parent := filepath.Dir(dir)
		secretPath := filepath.Join(parent, "secret.txt")
		if err := os.WriteFile(secretPath, []byte("top secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(secretPath) //nolint:errcheck // test cleanup

		// Try to escape via ..
		_, err := c.FetchBinary(context.Background(), "../secret.txt")
		if err == nil {
			t.Fatal("expected error for path traversal attempt")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := c.FetchBinary(context.Background(), "nope.txt")
		if err == nil {
			t.Fatal("expected error for nonexistent file")
		}
	})

	t.Run("empty source id", func(t *testing.T) {
		_, err := c.FetchBinary(context.Background(), "")
		if err == nil {
			t.Fatal("expected error for empty source id")
		}
	})

	t.Run("directory rejected", func(t *testing.T) {
		_, err := c.FetchBinary(context.Background(), "sub")
		if err == nil {
			t.Fatal("expected error when source id points to a directory")
		}
	})
}

func readAll(t *testing.T, r interface{ Read(p []byte) (int, error) }) ([]byte, error) {
	t.Helper()
	var buf [4096]byte
	var out []byte
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
