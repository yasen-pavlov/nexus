# Connect any MCP host to Nexus

`nexus-cli mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server over stdio. It exposes one read-only tool, **`nexus_search`**, backed by
your Nexus instance — so any MCP-aware host (Claude Code, Cursor, Zed, VS Code,
Goose, …) can search your personal corpus and ground its answers in your own
data. Results are always scoped to the authenticated user.

The server is the portable, load-bearing interface; the
[Claude Code plugin](../plugins/nexus/README.md) is just convenience packaging on
top of it.

## 1. Install `nexus-cli`

Until prebuilt packages ship, build from source and put the binary on your `PATH`:

```bash
make build-cli            # -> bin/nexus-cli
go install ./cmd/nexus-cli   # or symlink bin/nexus-cli into a PATH dir
command -v nexus-cli      # confirm
```

## 2. Authenticate (once)

The server resolves a token in this order: `NEXUS_TOKEN` env > OS keychain >
stored config. Pick one:

```bash
# Recommended: token stored in your OS keychain, nothing in any config file.
nexus-cli login --server https://your-nexus

# Or: export in the shell that launches your MCP host (it inherits these).
export NEXUS_URL=https://your-nexus
export NEXUS_TOKEN=nexus_pat_xxxxxxxx
```

If a host launches the server with no token, `nexus_search` returns a clear,
actionable message telling you to run `nexus-cli login` (or set `NEXUS_TOKEN`)
rather than failing opaquely.

## 3. Register it with your host

> MCP support is young and config formats differ per host and move between
> versions. The snippets below are a starting point — if a host doesn't pick the
> server up, check its own MCP docs for the current key names and file path. In
> all of them the goal is identical: run the command `nexus-cli mcp` as a stdio
> server.

### Claude Code

```bash
# One-off MCP server:
claude mcp add nexus -- nexus-cli mcp

# Or the full plugin (adds the search skill too):
# /plugin marketplace add yasen-pavlov/nexus
# /plugin install nexus@nexus-tools
```

### Generic `mcpServers` block (Cursor, Windsurf, …)

Most hosts read an `mcpServers` block from a JSON file — only the path differs
(Claude Code: `.mcp.json`; Cursor: `.cursor/mcp.json`; Windsurf:
`~/.codeium/windsurf/mcp_config.json`):

```json
{
  "mcpServers": {
    "nexus": {
      "command": "nexus-cli",
      "args": ["mcp"]
    }
  }
}
```

Add an `env` block only if you prefer environment auth over the keychain:

```json
{
  "mcpServers": {
    "nexus": {
      "command": "nexus-cli",
      "args": ["mcp"],
      "env": { "NEXUS_URL": "https://your-nexus", "NEXUS_TOKEN": "nexus_pat_xxxx" }
    }
  }
}
```

> This bakes a long-lived credential into a file. Prefer the keychain
> (`nexus-cli login`) or a shell `export`; if you must use the file, make sure it
> is git-ignored so the token isn't committed.

### Zed

In `settings.json`:

```json
{
  "context_servers": {
    "nexus": {
      "command": "nexus-cli",
      "args": ["mcp"],
      "env": {}
    }
  }
}
```

### VS Code (GitHub Copilot agent / MCP)

In `.vscode/mcp.json`:

```json
{
  "servers": {
    "nexus": { "command": "nexus-cli", "args": ["mcp"] }
  }
}
```

### Goose

In `~/.config/goose/config.yaml` (the entry must be enabled to load):

```yaml
extensions:
  nexus:
    name: nexus
    type: stdio
    cmd: nexus-cli
    args: ["mcp"]
    enabled: true
```

Or add it interactively with `goose configure` → *Add Extension* → *Command-line
Extension*, which writes the same entry.

## The tool

`nexus_search` accepts:

| field | type | notes |
| --- | --- | --- |
| `query` | string (required) | natural-language query |
| `sources` | string[] | restrict to `filesystem`, `imap`, `telegram`, `paperless`, `ical` |
| `date_from` / `date_to` | `YYYY-MM-DD` | inclusive date bounds |

It returns the top results as readable text **and** structured content
(`id`, `title`, `source_type`, `source_name`, `date`, `url`, `snippet`, `rank`).
The server also advertises usage instructions in its MCP handshake, so hosts that
honor them will reach for it automatically on questions about your own data.
