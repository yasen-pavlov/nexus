package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muty/nexus/internal/cliclient"
	"github.com/muty/nexus/internal/mcpserver"
	"github.com/spf13/cobra"
)

// newMCPCmd builds the `nexus-cli mcp` subcommand: a stdio MCP server exposing
// Nexus retrieval tools to MCP-aware hosts (Claude Code, Cursor, Zed, …). It
// reuses the same token resolution as every other command (flag > NEXUS_TOKEN >
// stored), so a host config can inject NEXUS_URL + NEXUS_TOKEN via its env block
// or rely on a prior `nexus-cli login`.
//
// The transport is newline-delimited JSON over stdin/stdout, so nothing but the
// protocol may touch stdout — diagnostics and the cobra usage/error output go to
// stderr. The server runs until the host closes stdin (EOF) or the process is
// signalled.
func newMCPCmd(rf *rootFlags, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server over stdio",
		Long: "Expose Nexus search to MCP-aware hosts (Claude Code, Cursor, Zed, …) as a\n" +
			"stdio server. Authentication reuses the resolved token (flag, NEXUS_TOKEN, or a\n" +
			"prior login); hosts typically inject NEXUS_URL and NEXUS_TOKEN via their config.\n\n" +
			"This command speaks JSON-RPC on stdin/stdout and is not meant to be run\n" +
			"interactively — point an MCP client at it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// resolveClient (not authedClient) so the server starts even with no
			// token: the host then completes the handshake and the nexus_search
			// tool returns an actionable "not authenticated" message, instead of
			// the command exiting and the host showing an opaque -32000.
			client, _, err := resolveClient(rf)
			if err != nil {
				return err
			}
			// Cancel the server on Ctrl-C / SIGTERM in addition to the stdin-EOF
			// path the StdioTransport already handles, so a directly-launched
			// server shuts down cleanly.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveMCP(ctx, client, version, &mcp.StdioTransport{})
		},
	}
}

// serveMCP builds the MCP server backed by client and runs it on transport
// until ctx is cancelled or the peer disconnects. Split from the command body
// so the run path is testable with an in-memory transport (the command itself
// always speaks stdio).
//
// A Ctrl-C / SIGTERM cancels ctx, and the SDK's Run then returns ctx.Err()
// (context.Canceled). That is an intended shutdown, not a failure, so it is
// folded to nil — otherwise cobra would print "Error: context canceled" and
// exit non-zero, the opposite of the clean stop the signal handler exists for.
// The host-driven EOF path already returns nil. Genuine transport errors still
// propagate.
func serveMCP(ctx context.Context, client *cliclient.Client, version string, transport mcp.Transport) error {
	err := mcpserver.New(client, version).Run(ctx, transport)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
