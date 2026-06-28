// Command nexus-cli is the command-line client (and, later, MCP server) for a
// Nexus personal search server. It wraps the existing HTTP API.
package main

import "github.com/muty/nexus/internal/cli"

// Build-time metadata, injected via -ldflags "-X main.version=..." by
// GoReleaser. Declaring the vars here (unlike cmd/nexus) is what makes those
// ldflags actually take effect — without a matching symbol the linker silently
// drops the -X.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.Execute(version, commit, date)
}
