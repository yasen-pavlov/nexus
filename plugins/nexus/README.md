# Nexus plugin for Claude Code

Search your self-hosted [Nexus](https://github.com/yasen-pavlov/nexus) personal
corpus — files, email, chat, documents, calendar — from inside Claude Code. The
plugin bundles the `nexus-cli mcp` server (exposing the `nexus_search` tool) plus
a skill that teaches Claude **when** to search your own data and **how** to cite
it.

## Requirements

- A running Nexus server you can reach.
- The **`nexus-cli` binary on your `PATH`**. Until prebuilt packages ship, build
  from source:

  ```bash
  git clone https://github.com/yasen-pavlov/nexus && cd nexus
  make build-cli            # -> bin/nexus-cli
  go install ./cmd/nexus-cli   # or symlink bin/nexus-cli into a PATH dir
  command -v nexus-cli      # confirm it resolves
  ```

  > The plugin installs fine without the binary, but the MCP server can't start
  > until `nexus-cli` is on `PATH` — Claude Code shows that as a generic
  > "failed to connect", a *different* failure from the authentication message
  > below (the process never launches, so there's no tool result to show).

## Install

```text
/plugin marketplace add yasen-pavlov/nexus
/plugin install nexus@nexus-tools
```

If you add it mid-session, reload with `/plugin` (plugin MCP servers are managed
through `/plugin`, not `/mcp`).

## Authenticate

Pick one — the bundled server resolves a token in this order: `NEXUS_TOKEN` env >
OS keychain > stored config.

- **Keychain (recommended):** `nexus-cli login --server https://your-nexus` once.
  The token stays in your OS keychain; nothing sensitive lands in any config file.
- **Environment:** export `NEXUS_URL` and `NEXUS_TOKEN` in the shell that launches
  Claude Code; the plugin's server inherits them.

If you haven't authenticated, `nexus_search` returns a clear, actionable message
in chat telling you to run `nexus-cli login` or set `NEXUS_TOKEN` — not an opaque
connection error.

## Use

Just ask about your own data — "what did Anna email me about the lease?", "find my
invoice for the standing desk", "when did I note the router password?" — and Claude
will search the corpus and cite sources. The explicit form `/nexus:search` is also
available.

## Other hosts

The plugin is convenience, not the only path. The same server runs in any MCP host
(Cursor, Zed, VS Code, Goose) — see [`docs/mcp.md`](../../docs/mcp.md) in the repo
for `claude mcp add` and raw `.mcp.json` snippets.
