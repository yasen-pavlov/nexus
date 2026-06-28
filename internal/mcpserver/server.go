// Package mcpserver exposes a read-only slice of the Nexus API as Model Context
// Protocol (MCP) tools over a stdio transport, so MCP-aware hosts (Claude Code,
// Cursor, VS Code, Zed, Goose, …) can search a user's personal corpus. It is a
// thin adapter over internal/cliclient: the same authenticated HTTP client the
// CLI uses backs every tool, so owner-scoping, the {"data": …} envelope, and
// bearer auth live in one place. Only retrieval primitives are exposed —
// nexus_search today — never the agentic ask flow, which would nest a second
// LLM tool-loop inside the calling host's own.
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muty/nexus/internal/cliclient"
)

// serverName is the MCP implementation name advertised in the initialize
// handshake. Kept stable so client configs and logs can key off it.
const serverName = "nexus-cli"

// New builds an MCP server exposing Nexus retrieval tools backed by client.
// version is surfaced in the MCP initialize handshake. The returned server is
// not yet connected to a transport; the caller runs it (e.g. over stdio).
func New(client *cliclient.Client, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Title:   "Nexus",
		Version: version,
	}, nil)
	addSearchTool(srv, client)
	return srv
}
