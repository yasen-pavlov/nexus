package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muty/nexus/internal/cliclient"
)

// The stdio server itself is exercised end-to-end in internal/mcpserver via an
// in-memory transport; here we only verify the CLI wiring: the command exists
// and enforces the same auth gate every other command does before touching the
// transport.

func TestMCPNotLoggedIn(t *testing.T) {
	isolateConfig(t)
	_, err := run(t, "", "mcp")
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("expected not-authenticated error, got %v", err)
	}
}

func TestMCPRejectsArgs(t *testing.T) {
	isolateConfig(t)
	_, err := run(t, "", "mcp", "extra-arg")
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected arg rejection, got %v", err)
	}
}

// TestServeMCPRunsAndStops drives the command's run path over an in-memory
// transport: the server comes up, answers tools/list, and Run returns once the
// peer disconnects.
func TestServeMCPRunsAndStops(t *testing.T) {
	serverT, clientT := mcp.NewInMemoryTransports()
	errc := make(chan error, 1)
	go func() {
		errc <- serveMCP(context.Background(), cliclient.New("http://nexus.invalid", "tok"), "test", serverT)
	}()

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "nexus_search" {
		t.Fatalf("server did not expose nexus_search: %+v", tools.Tools)
	}

	// Disconnecting the client must let Run return promptly and cleanly.
	_ = cs.Close()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("clean client disconnect should return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveMCP did not return after client disconnect")
	}
}

// TestServeMCPCleanShutdownOnCancel verifies the signal path: cancelling the
// server context (as SIGINT/SIGTERM does) returns nil, not context.Canceled, so
// an intended stop exits 0 without cobra printing an error.
func TestServeMCPCleanShutdownOnCancel(t *testing.T) {
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		errc <- serveMCP(ctx, cliclient.New("http://nexus.invalid", "tok"), "test", serverT)
	}()

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("list tools: %v", err)
	}

	cancel() // stand-in for SIGINT/SIGTERM cancelling the server context
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("ctx-cancel shutdown must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveMCP did not return after ctx cancel")
	}
}
