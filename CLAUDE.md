# Nexus - Development Guide

## Project Overview

Nexus is a self-hosted personal search/RAG tool. It indexes data from multiple sources (filesystem, email/IMAP, Telegram, Paperless-ngx, iCal/CalDAV) and provides unified full-text and semantic search via a web UI. A companion CLI (`nexus-cli`) and an MCP server expose the same search to the terminal and to agents.

## Tech Stack

- **Backend:** Go with chi router, pgx (Postgres driver), goose (migrations)
- **Database:** PostgreSQL for application state (connector configs, sync cursors, jobs)
- **Search:** OpenSearch for document indexing and full-text search
- **Frontend:** React + TypeScript + Vite
- **Deployment:** Docker (multi-stage build, single container + Postgres + OpenSearch)

## Project Structure

```
cmd/nexus/          Entry point, wiring, graceful shutdown
cmd/nexus-cli/      Companion CLI (login/logout/search) — see `make build-cli`
cmd/rag-eval/       Offline RAG quality eval harness (golden set → LLM judge → report)
internal/
  api/              HTTP handlers, chi router, connector manager, static file serving
  auth/             JWT sessions, bcrypt passwords, role middleware, rate limiting
  chunking/         Splits text into overlapping chunks for embedding (pure logic)
  cli/              Cobra commands backing nexus-cli (login, logout, search)
  cliclient/        Shared HTTP client + OS-keychain token storage for the CLI/MCP
  config/           Environment-based configuration (envconfig)
  connector/        Connector interface, registry, source implementations
    filesystem/     Filesystem crawler connector
    ical/           iCal / iCloud CalDAV calendar connector
    imap/           IMAP mailbox connector (body cleaning, CONDSTORE)
    paperless/      Paperless-ngx API connector
    telegram/       Telegram connector (conversation windows, media cache)
  crypto/           AES-256-GCM encryption for sensitive connector config fields
  embedding/        Pluggable embedding providers (Ollama, OpenAI, Voyage, Cohere)
  lang/             Language detection for multi-analyzer indexing/highlighting
  llm/              LLM provider abstraction for the RAG ask flow
    anthropic/      Anthropic (Claude) adapter
    openai/         OpenAI (GPT) adapter
    ollama/         Ollama adapter
  mcpserver/        MCP server exposing nexus_search to agents
  model/            Shared types (Document, SearchResult, SyncCursor, ConnectorConfig)
  netguard/         SSRF-guarded HTTP client for user-configured connector URLs
  pipeline/         Ingestion orchestration (fetch → extract → chunk → embed → index)
    extractor/      Content extraction interface + implementations
  rag/              RAG orchestrator (retrieval, tool loop, citations, streaming)
    eval/           RAG eval scoring + LLM judging logic
  rerank/           Pluggable reranking providers (Voyage, Cohere)
  retry/            Shared retry/backoff helper for provider HTTP calls
  scheduler/        Cron-based automatic sync scheduling
  search/           OpenSearch client (indexing, search, highlighting)
  storage/          On-disk binary cache for attachments/media
  store/            PostgreSQL access layer (connector configs, sync cursors)
  syncruns/         Sync-run history + retention sweeper
  testutil/         Shared test helpers (per-package isolated test databases + OpenSearch indices)
docs/               Generated OpenAPI/Swagger spec (swag init output)
migrations/         SQL migrations (goose, embedded via go:embed)
web/                React frontend (Vite, served as static files by Go)
```

## Commands

```bash
make test                # Run all tests (unit + integration)
make test-unit           # Unit tests only (no database required)
make test-integration    # Integration tests (requires Postgres + OpenSearch)
make lint                # Run golangci-lint
make lint-sql            # Run sqlfluff on every migration
make coverage            # Full coverage report (excludes testutil)
make swagger             # Regenerate OpenAPI/Swagger spec (swag init → docs/)
make build               # Build binary to bin/nexus (runs swagger first)
make build-cli           # Build the companion CLI to bin/nexus-cli
make dev-db              # Start Postgres + OpenSearch + Tika via docker-compose
make dev                 # Start deps + run app locally against testdata/
make up                  # Start the full stack (app + deps) in Docker (needs .env)
make down                # Stop everything across all profiles
make logs                # Tail the app stack logs
make rag-eval            # Offline RAG quality eval → rag-eval-report.md
```

## Development Workflow

### Running locally
```bash
make dev    # starts Postgres + OpenSearch + Go app with testdata/
cd web && npm run dev   # starts Vite dev server at localhost:5173 (proxies /api to :8080)
```

### Docker
```bash
docker compose up --build    # full stack at localhost:8080
```

## Testing

- **Unit tests** have no build tag — run anywhere
- **Integration tests** use `//go:build integration` — require Postgres and OpenSearch
- Integration tests get **per-package isolated databases** via `testutil.NewTestDB(t, "pkgname", migrations.FS)` — no cross-package interference
- OpenSearch tests use **per-test isolated indices** via `testutil.TestOSConfig(t, "prefix")` + `search.NewWithIndex`
- Tests that need external services but aren't behind the integration tag use `t.Skip` when unavailable
- Target **90%+ test coverage** (excluding testutil)

## Linting

- Config: `.golangci.yml` (golangci-lint v2)
- Linters: errcheck, govet, staticcheck (all checks including ST style rules), unused, ineffassign
- Formatter: gofmt
- `web/` directory is excluded from Go linting
- Every Go package must have a package comment (ST1000)

## Architecture Patterns

- **Connector interface:** Each data source implements `connector.Connector` (Type, Name, Configure, Validate, Fetch with cursor-based incremental sync)
- **Connector management:** CRUD API backed by `connector_configs` table, `ConnectorManager` handles lifecycle
- **Scheduler:** `robfig/cron/v3` for automatic sync, keyed by connector ID, updated live via `ScheduleObserver`
- **No ORM:** Raw SQL via pgx for Postgres operations
- **Pipeline stages:** Fetch → Extract → Chunk → Embed → Index in OpenSearch (embeddings optional; BM25 works without them)
- **Search:** OpenSearch handles document storage and search (BM25 + optional k-NN vector search). PostgreSQL only stores application state.
- **Embeddings:** Pluggable providers (Ollama, OpenAI, Voyage, Cohere) via `embedding.Embedder` interface. Documents are chunked (~500 tokens, ~100 overlap) before embedding. Voyage default model is `voyage-4-large` (1024-dim). Embedder calls take an `inputType` parameter (`document` or `query`) so providers that distinguish them (Voyage, Cohere) can prepend the right instructions internally — others ignore it. Hybrid search uses reciprocal rank fusion (RRF) to merge BM25 and vector results.
- **Chunking:** `internal/chunking/` splits text into overlapping chunks for embedding. Pure logic, no external dependencies.
- **Noise gate:** chunks with fewer than `minEmbeddingAlphabeticTokens` (10) alphabetic tokens skip embedding entirely — they're indexed for BM25 but contribute no vector. Filters out URL-only chunks, hashes, base64 blobs, and other low-information content that would otherwise produce noisy "hub" embeddings clustering near every query. See `internal/pipeline/pipeline.go`.
- **IMAP body cleaning:** before chunking, email bodies pass through DOM-aware HTML stripping (drops `<style>`/`<script>`/`<head>`, unwraps `<a>` to drop tracking redirects) and `cleanEmailText` (strips known tracking URL patterns + long opaque base64-y URLs, removes quoted-reply blocks and RFC 3676 signature blocks). See `internal/connector/imap/parser.go`.
- **Telegram conversation windows:** consecutive messages in a chat are grouped into "conversation windows" (~30-min gap, ~2000-char cap) and indexed as one document per window — not one per message. This gives the embedder enough context to produce meaningful vectors and avoids the noise-hub problem from indexing thousands of one-line chat messages individually. SourceID format is `chatID:firstMsgID-lastMsgID`. See `internal/connector/telegram/connector.go`.
- **Reranker dedup:** before sending candidates to the reranker, near-duplicates (chunks sharing the same first 200 chars of title+content) are deduped to avoid wasting reranker API budget on multiple chunks of the same boilerplate-heavy newsletter. See `internal/api/handlers.go` `dedupeNearDuplicates`.
- **Static embedding:** React build output is embedded into the Go binary via `//go:embed` in `internal/api/static/`
- **Migrations:** Embedded in the binary, run automatically at startup via goose

## Configuration

All via environment variables with `NEXUS_` prefix:
- `NEXUS_PORT` (default: 8080)
- `NEXUS_DATABASE_URL` (required)
- `NEXUS_OPENSEARCH_URL` (default: http://localhost:9200)
- `NEXUS_OPENSEARCH_USERNAME` / `NEXUS_OPENSEARCH_PASSWORD` — basic auth for the OpenSearch client (empty = no auth; default deploy relies on docker-network isolation, host port bound to 127.0.0.1). Opt into the security plugin via `docker-compose.secure.yml`.
- `NEXUS_OPENSEARCH_CA_FILE` — PEM CA bundle to verify the OpenSearch server cert; `NEXUS_OPENSEARCH_INSECURE_SKIP_VERIFY` (bool, default false) skips TLS verification for demo certs over a private bridge. Wired in `internal/search/search.go` `buildClientConfig` via a custom TLS transport.
- `NEXUS_LOG_LEVEL` (default: info)
- `NEXUS_TIKA_URL` (default: http://localhost:9998) — Apache Tika endpoint for rich binary extraction / OCR
- `NEXUS_EMBEDDING_PROVIDER` — `ollama`, `openai`, `voyage`, `cohere` (empty = disabled)
- `NEXUS_EMBEDDING_MODEL` — model name (provider-specific defaults apply)
- `NEXUS_EMBEDDING_API_KEY` — API key for openai/voyage/cohere
- `NEXUS_OLLAMA_URL` (default: http://localhost:11434) — Ollama base URL
- `NEXUS_RERANK_PROVIDER` — `voyage`, `cohere` (empty = disabled)
- `NEXUS_RERANK_MODEL` — reranking model name (provider-specific defaults apply)
- `NEXUS_RERANK_API_KEY` — API key for reranking (falls back to embedding key if same provider)
- `NEXUS_LLM_DEFAULT_MODEL` — provider-prefixed default model for the RAG ask flow (e.g. `anthropic:claude-sonnet-4-6`). Empty = first-boot picks the cheapest available across configured providers.
- `NEXUS_LLM_ANTHROPIC_API_KEY` — Anthropic API key (enables Claude models)
- `NEXUS_LLM_OPENAI_API_KEY` — OpenAI API key (enables GPT models)
- `NEXUS_LLM_OLLAMA_URL` — dedicated LLM Ollama URL; falls back to `NEXUS_OLLAMA_URL` when empty
- `NEXUS_ENCRYPTION_KEY` — 64-char hex string (32 bytes) for AES-256-GCM encryption of sensitive connector config fields (empty = disabled). A changed/lost key no longer bricks boot: rows that fail to decrypt degrade per-row (`scanConnectorConfig` sets the transient `credentials_unreadable` flag + strips the ciphertext) and load inactive so the owner can re-enter secrets. Planned rotation: `nexus rotate-key -new-key <hex>` (store.RotateEncryptionKey re-encrypts connector + settings secrets in one transaction).
- `NEXUS_JWT_SECRET` — secret used to sign JWT session tokens. If empty, a random one is generated on each boot (which logs everyone out on restart). Set explicitly for stable sessions across restarts.
- `NEXUS_CORS_ORIGINS` — comma-separated list of allowed CORS origins (default: `http://localhost:5173`)
- `NEXUS_FS_ROOT_PATH` — filesystem connector root (seeds DB on first run as a shared connector)
- `NEXUS_FS_PATTERNS` — glob patterns (default: `*.txt,*.md`)
- `NEXUS_BINARY_STORE_PATH` (default: `data/binaries`) — on-disk cache for connector attachments/media (IMAP, Telegram); mount as a volume to persist across restarts

## Authentication and authorization

- Username + password auth with bcrypt-hashed passwords. Sessions are JWT-based (HS256, 24h expiry), signed with `NEXUS_JWT_SECRET`.
- Two roles: `admin` and `user`. The first user to register becomes admin; subsequent registrations are disabled (admin creates additional users via `/api/users`).
- The `/api/health` endpoint returns `setup_required: true` when no users exist — the frontend uses this to show the registration form.
- Connectors are owned by a user (`user_id`) or marked `shared`. The schema enforces `(user_id IS NOT NULL OR shared = true)` so every connector either has an owner or is shared.
- Search results are scoped per request: a user only sees chunks where `owner_id` matches them OR `shared = true`. The seeded filesystem connector (`NEXUS_FS_ROOT_PATH`) is always created as shared.
- Connector handlers (`Get`/`Update`/`Delete`/`TriggerSync`/`DeleteCursor`/`StreamProgress`) all enforce ownership: a regular user can only modify their own connectors; admins can modify anything; users can read shared connectors but not mutate them. The same ownership check covers the connector-scoped routes: avatar fetch (`/api/connectors/{id}/avatars/{external_id}`), sync-run history (`/api/connectors/{id}/runs`), and the Telegram auth flow (`/api/connectors/{id}/auth/start`, `/api/connectors/{id}/auth/code`).
- Chats are owned by a user (`chats.user_id`); ownership is enforced for ALL chat operations including read. Admins are NOT exempt — admins cannot view or modify other users' chats. Non-owners receive 404 (not 403) on `/api/chats/:id*` to avoid leaking chat existence. The RAG orchestrator scopes retrieval by the chat owner's UUID, so even tool-issued searches stay inside the user's permitted corpus.
- Admin-only routes: `/api/settings/*`, `/api/reindex`, `/api/sync/cursors`, `/api/users/*`, `/api/admin/stats`, `/api/storage/stats`, `/api/storage/cache`, `/api/storage/cache/{id}`.
