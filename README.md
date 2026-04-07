# Nexus

A personal search tool you can self-host. Index and search across your files, emails, messages, and documents from a single interface.



## Features

- **Unified search** across multiple data sources
- **Full-text search** with highlighted snippets powered by PostgreSQL tsvector
- **Modular connectors** — plug in new data sources easily
- **Incremental sync** — only processes new/changed data
- **Single binary deployment** — Go backend serves the React frontend
- **Docker-ready** — one `docker compose up` to run everything

### Current Connectors

- **Filesystem** — crawl local directories for text files

### Planned Connectors

- IMAP (iCloud Mail, Gmail)
- Telegram
- Paperless-ngx
- Synology NAS (file crawling)

## Quick Start

### Docker (recommended)

```bash
docker compose up --build
```

This starts PostgreSQL and Nexus at [http://localhost:8080](http://localhost:8080).

The included `testdata/` directory is mounted with sample files. Click "Sync filesystem" to index them, then search away.

### Local Development

Prerequisites: Go 1.22+, Node.js 22+, Docker (for Postgres)

```bash
# Start the database
make dev-db

# Run the backend (in one terminal)
make dev

# Run the frontend dev server (in another terminal)
cd web && npm install && npm run dev
```

- Backend API: [http://localhost:8080](http://localhost:8080)
- Frontend dev server: [http://localhost:5173](http://localhost:5173) (proxies API requests to backend)

## Configuration

Nexus is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NEXUS_PORT` | `8080` | HTTP server port |
| `NEXUS_DATABASE_URL` | *(required)* | PostgreSQL connection string |
| `NEXUS_LOG_LEVEL` | `info` | Log level (`info`, `debug`) |
| `NEXUS_FS_ROOT_PATH` | | Directory to index |
| `NEXUS_FS_PATTERNS` | `*.txt,*.md` | File glob patterns to match |

## API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/health` | GET | Health check |
| `/api/search?q=term&limit=20&offset=0` | GET | Full-text search |
| `/api/connectors` | GET | List configured connectors |
| `/api/sync/{connector}` | POST | Trigger sync for a connector |

## Architecture

```
React SPA → REST API (chi) → PostgreSQL (tsvector + pgvector)
                ↑
        Connector plugins → Ingestion pipeline → Document store
```

- **Connectors** fetch data from sources using cursor-based incremental sync
- **Pipeline** processes fetched documents and stores them
- **Store** handles PostgreSQL operations including full-text search
- **API** serves search results and manages sync operations

## Tech Stack

- **Go** — backend, API, ingestion pipeline
- **PostgreSQL + pgvector** — storage, full-text search, vector search (future)
- **React + Vite + TypeScript** — frontend
- **chi** — HTTP router
- **pgx** — PostgreSQL driver
- **goose** — database migrations

## License

TBD
