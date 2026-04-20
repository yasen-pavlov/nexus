-- +goose Up
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS documents;

-- +goose Down
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
    chunk_index INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_chunks_document_id ON chunks (document_id);
