//go:build integration

package pipeline

import (
	"context"
	"os"
	"testing"

	"github.com/muty/nexus/internal/connector"
	_ "github.com/muty/nexus/internal/connector/filesystem"
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

	_, err := p.Run(context.Background(), fsConn)
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

	_, err := p.Run(context.Background(), fsConn)
	if err == nil {
		t.Fatal("expected error from fetch")
	}
}
