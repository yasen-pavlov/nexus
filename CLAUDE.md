# Nexus - Development Guide

## Project Overview

Nexus is a self-hosted personal search/RAG tool. It indexes data from multiple sources (filesystem, email, Telegram, Paperless-ngx, NAS) and provides unified full-text and semantic search via a web UI.

## Tech Stack

- **Backend:** Go with chi router, pgx (Postgres driver), goose (migrations)
- **Database:** PostgreSQL for application state (connector configs, sync cursors, jobs)
- **Search:** OpenSearch for document indexing and full-text search
- **Frontend:** React + TypeScript + Vite
- **Deployment:** Docker (multi-stage build, single container + Postgres + OpenSearch)

## Project Structure

```
cmd/nexus/          Entry point, wiring, graceful shutdown
internal/
  api/              HTTP handlers, chi router, connector manager, static file serving
  config/           Environment-based configuration (envconfig)
  connector/        Connector interface, registry, source implementations
    filesystem/     Filesystem crawler connector
    paperless/      Paperless-ngx API connector
  model/            Shared types (Document, SearchResult, SyncCursor, ConnectorConfig)
  pipeline/         Ingestion orchestration (fetch → index in OpenSearch)
    extractor/      Content extraction interface + implementations
  scheduler/        Cron-based automatic sync scheduling
  search/           OpenSearch client (indexing, search, highlighting)
  store/            PostgreSQL access layer (connector configs, sync cursors)
  testutil/         Shared test helpers (per-package isolated test databases + OpenSearch indices)
migrations/         SQL migrations (goose, embedded via go:embed)
web/                React frontend (Vite, served as static files by Go)
```

## Commands

```bash
make test                # Run all tests (unit + integration)
make test-unit           # Unit tests only (no database required)
make test-integration    # Integration tests (requires Postgres + OpenSearch)
make lint                # Run golangci-lint
make coverage            # Full coverage report (excludes testutil)
make build               # Build binary to bin/nexus
make dev-db              # Start Postgres + OpenSearch via docker-compose
make dev                 # Start DB + run app locally
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
- **Pipeline stages:** Fetch → Index in OpenSearch (future: Chunk → Embed)
- **Search:** OpenSearch handles document storage and search (BM25 + optional k-NN vector search). PostgreSQL only stores application state.
- **Embeddings:** Pluggable providers (Ollama, OpenAI, Voyage, Cohere) via `embedding.Embedder` interface. Documents are chunked (~500 tokens, ~100 overlap) before embedding. Hybrid search uses reciprocal rank fusion (RRF) to merge BM25 and vector results.
- **Chunking:** `internal/chunking/` splits text into overlapping chunks for embedding. Pure logic, no external dependencies.
- **Static embedding:** React build output is embedded into the Go binary via `//go:embed` in `internal/api/static/`
- **Migrations:** Embedded in the binary, run automatically at startup via goose

## Configuration

All via environment variables with `NEXUS_` prefix:
- `NEXUS_PORT` (default: 8080)
- `NEXUS_DATABASE_URL` (required)
- `NEXUS_OPENSEARCH_URL` (default: http://localhost:9200)
- `NEXUS_LOG_LEVEL` (default: info)
- `NEXUS_EMBEDDING_PROVIDER` — `ollama`, `openai`, `voyage`, `cohere` (empty = disabled)
- `NEXUS_EMBEDDING_MODEL` — model name (provider-specific defaults apply)
- `NEXUS_EMBEDDING_API_KEY` — API key for openai/voyage/cohere
- `NEXUS_OLLAMA_URL` (default: http://localhost:11434) — Ollama base URL
- `NEXUS_FS_ROOT_PATH` — filesystem connector root (seeds DB on first run)
- `NEXUS_FS_PATTERNS` — glob patterns (default: `*.txt,*.md`)
