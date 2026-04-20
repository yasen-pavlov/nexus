-- +goose Up
-- Metadata index for the binary content cache. Blobs live on the filesystem
-- under NEXUS_BINARY_STORE_PATH; this table tracks what's cached, its size,
-- and last access time so the eviction goroutine can efficiently find
-- expired or LRU entries without walking the filesystem.
--
-- Keyed by the same (source_type, source_name, source_id) triple used
-- everywhere else in the system, so DeleteBySource mirrors the existing
-- pattern in internal/search/search.go.
CREATE TABLE binary_store_entries (
    source_type TEXT NOT NULL,
    source_name TEXT NOT NULL,
    source_id TEXT NOT NULL,
    file_path TEXT NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    stored_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_type, source_name, source_id)
);

-- Bulk-delete by connector (source_type + source_name).
CREATE INDEX idx_binary_store_source
    ON binary_store_entries (source_type, source_name);

-- Eviction queries: find expired entries per source type, ordered by LRU.
CREATE INDEX idx_binary_store_accessed
    ON binary_store_entries (source_type, last_accessed_at);

-- +goose Down
DROP TABLE IF EXISTS binary_store_entries;
