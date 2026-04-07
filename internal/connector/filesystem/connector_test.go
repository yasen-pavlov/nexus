package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
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
		name:     "test",
		rootPath: dir,
		patterns: []string{"*.txt", "*.md"},
	}

	t.Run("full sync", func(t *testing.T) {
		result, err := c.Fetch(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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

		// Verify cursor
		if result.Cursor == nil {
			t.Fatal("expected cursor to be set")
		}
		if result.Cursor.LastStatus != "success" {
			t.Errorf("expected status 'success', got %q", result.Cursor.LastStatus)
		}
		if result.Cursor.ItemsSynced != 3 {
			t.Errorf("expected 3 items synced, got %d", result.Cursor.ItemsSynced)
		}
	})

	t.Run("incremental sync skips old files", func(t *testing.T) {
		pastCursor := &model.SyncCursor{
			CursorData: map[string]any{
				"last_sync_time": time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano),
			},
		}

		result, err := c.Fetch(context.Background(), pastCursor)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Documents) != 0 {
			t.Errorf("expected 0 documents for future cursor, got %d", len(result.Documents))
		}
	})
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

	_, err := c.Fetch(ctx, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
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
