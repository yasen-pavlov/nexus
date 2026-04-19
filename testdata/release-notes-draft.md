# Release Notes — draft

Working draft for the next homelab-stack release. Clean this up before tagging.

## Highlights

- **Hybrid search** across all connectors with reciprocal rank fusion and a
  Voyage-powered reranker. Replaces the previous BM25-only pipeline.
- **Telegram connector** with conversation-window indexing, attachment
  download, and a dedicated chat browser view.
- **IMAP connector** with DOM-aware HTML stripping (drops tracking redirects,
  style blocks, RFC 3676 signatures) so embeddings stay signal-dense.
- **Sync run history** — every connector sync is recorded with started/ended
  timestamps, item counts, and a cancellation hook. The sync dashboard
  streams progress over SSE.

## Breaking changes

- The `NEXUS_DATABASE_URL` environment variable is now required. Previously
  there was a SQLite fallback; removing it simplified the storage layer.
- Embedding configuration has moved from environment variables into the
  Settings UI and the `connector_configs` table. Existing
  `NEXUS_EMBEDDING_*` vars still work as overrides for now but will be
  removed in the next minor.

## Migrations

`migrations/` contains 6 new goose files. They run automatically on boot and
are idempotent — no manual steps needed.

## Upgrading

```sh
docker compose pull
docker compose --profile app up -d
```

First boot after upgrade triggers a one-time reindex if the OpenSearch schema
version has changed. Expect a 30–60s window where search returns empty before
the new index is ready.
