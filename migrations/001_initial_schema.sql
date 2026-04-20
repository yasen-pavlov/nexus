-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE documents (
    id UUID PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_name TEXT NOT NULL,
    source_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    content_ts TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(title, '') || ' ' || coalesce(content, ''))
    ) STORED,
    metadata JSONB NOT NULL DEFAULT '{}',
    url TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT 'private',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_type, source_name, source_id)
);

CREATE INDEX idx_documents_content_ts ON documents USING GIN (content_ts);
CREATE INDEX idx_documents_source ON documents (source_type, source_name);

CREATE TABLE chunks (
    id UUID PRIMARY KEY,
    document_id UUID NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    content TEXT NOT NULL DEFAULT '',
    embedding VECTOR(768),
    chunk_index INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_chunks_document_id ON chunks (document_id);

CREATE TABLE sync_cursors (
    connector_id TEXT PRIMARY KEY,
    cursor_data JSONB NOT NULL DEFAULT '{}',
    last_sync TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status TEXT NOT NULL DEFAULT '',
    items_synced INT NOT NULL DEFAULT 0
);

CREATE TABLE jobs (
    id UUID PRIMARY KEY,
    connector_id TEXT NOT NULL,
    schedule TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_run TIMESTAMPTZ,
    next_run TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS sync_cursors;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS documents;
DROP EXTENSION IF EXISTS vector;
