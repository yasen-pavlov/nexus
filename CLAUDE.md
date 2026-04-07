# Nexus - Development Guide

## Project Overview

Nexus is a self-hosted personal search/RAG tool. It indexes data from multiple sources (filesystem, email, Telegram, Paperless-ngx, NAS) and provides unified full-text and semantic search via a web UI.

## Tech Stack

- **Backend:** Go with chi router, pgx (Postgres driver), goose (migrations)
- **Database:** PostgreSQL + pgvector (tsvector for full-text, pgvector for semantic search)
- **Frontend:** React + TypeScript + Vite
- **Deployment:** Docker (multi-stage build, single container + Postgres)

## Project Structure

```
cmd/nexus/          Entry point, wiring, graceful shutdown
internal/
  api/              HTTP handlers, chi router, static file serving
  config/           Environment-based configuration (envconfig)
  connector/        Connector interface, registry, source implementations
    filesystem/     Filesystem crawler connector
  model/            Shared types (Document, SearchResult, SyncCursor)
  pipeline/         Ingestion orchestration (fetch → store)
    extractor/      Content extraction interface + implementations
  search/           Search engine (future: hybrid search)
  store/            PostgreSQL access layer (documents, search, sync cursors)
  testutil/         Shared test helpers (per-package isolated test databases)
migrations/         SQL migrations (goose, embedded via go:embed)
web/                React frontend (Vite, served as static files by Go)
```

## Commands

```bash
make test                # Run all tests (unit + integration)
make test-unit           # Unit tests only (no database required)
make test-integration    # Integration tests (requires Postgres)
make lint                # Run golangci-lint
make coverage            # Full coverage report (excludes testutil)
make build               # Build binary to bin/nexus
make dev-db              # Start Postgres via docker-compose
make dev                 # Start DB + run app locally
```

## Development Workflow

### Running locally
```bash
make dev    # starts Postgres + Go app with testdata/
cd web && npm run dev   # starts Vite dev server at localhost:5173 (proxies /api to :8080)
```

### Docker
```bash
docker compose up --build    # full stack at localhost:8080
```

## Testing

- **Unit tests** have no build tag — run anywhere
- **Integration tests** use `//go:build integration` — require Postgres
- Integration tests get **per-package isolated databases** via `testutil.NewTestDB(t, "pkgname", migrations.FS)` — no cross-package interference
- Tests that need a DB but aren't behind the integration tag use `t.Skip` when DB is unavailable
- Target **90%+ test coverage** (excluding testutil)

## Linting

- Config: `.golangci.yml` (golangci-lint v2)
- Linters: errcheck, govet, staticcheck (all checks including ST style rules), unused, ineffassign
- Formatter: gofmt
- `web/` directory is excluded from Go linting
- Every Go package must have a package comment (ST1000)

## Architecture Patterns

- **Connector interface:** Each data source implements `connector.Connector` (Type, Name, Configure, Validate, Fetch with cursor-based incremental sync)
- **No ORM:** Raw SQL via pgx. Use squirrel only if dynamic query building is needed
- **Pipeline stages:** Fetch → Extract → (future: Chunk → Embed) → Store
- **Static embedding:** React build output is embedded into the Go binary via `//go:embed` in `internal/api/static/`
- **Migrations:** Embedded in the binary, run automatically at startup via goose

## Configuration

All via environment variables with `NEXUS_` prefix:
- `NEXUS_PORT` (default: 8080)
- `NEXUS_DATABASE_URL` (required)
- `NEXUS_LOG_LEVEL` (default: info)
- `NEXUS_FS_ROOT_PATH` — filesystem connector root
- `NEXUS_FS_PATTERNS` — glob patterns (default: `*.txt,*.md`)
