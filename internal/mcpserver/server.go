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

// serverInstructions is advertised in the initialize handshake (InitializeResult.
// Instructions) and honored by every MCP host — so the "when/how to search the
// user's corpus" guidance reaches Cursor/Zed/Goose/VS Code too, not only Claude
// Code's bundled skill. Kept well under the 2KB hosts truncate at.
const serverInstructions = "Nexus indexes the user's personal corpus — their own files, email, " +
	"chat/Telegram history, Paperless documents, and calendar. Use the nexus_search tool to " +
	"ground answers whenever the user asks about THEIR own data (\"what did I…\", \"find my…\", " +
	"\"according to my notes/emails/files\") instead of answering from memory. Pass a specific " +
	"query; narrow with the sources filter (filesystem, imap, telegram, paperless, ical) and " +
	"date_from/date_to when the user implies a channel or timeframe. Cite each result by title, " +
	"source, and date, and include its URL when present. Results are already scoped to the " +
	"user's permissions."

// New builds an MCP server exposing Nexus retrieval tools backed by client.
// version is surfaced in the MCP initialize handshake. The returned server is
// not yet connected to a transport; the caller runs it (e.g. over stdio).
func New(client *cliclient.Client, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Title:   "Nexus",
		Version: version,
	}, &mcp.ServerOptions{Instructions: serverInstructions})
	addSearchTool(srv, client)
	return srv
}
