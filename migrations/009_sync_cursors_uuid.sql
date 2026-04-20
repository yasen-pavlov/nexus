-- +goose Up
-- Re-key sync_cursors by connector UUID with a foreign key to connector_configs.
-- The previous text key was the connector name, which collided across users
-- now that connector names are unique per user (not globally). Old rows are
-- dropped — the worst case is one extra full sync per connector.
DROP TABLE IF EXISTS sync_cursors;

CREATE TABLE sync_cursors (
    connector_id UUID PRIMARY KEY REFERENCES connector_configs (id) ON DELETE CASCADE,
    cursor_data JSONB NOT NULL DEFAULT '{}',
    last_sync TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status TEXT NOT NULL DEFAULT '',
    items_synced INT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS sync_cursors;

CREATE TABLE sync_cursors (
    connector_id TEXT PRIMARY KEY,
    cursor_data JSONB NOT NULL DEFAULT '{}',
    last_sync TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status TEXT NOT NULL DEFAULT '',
    items_synced INT NOT NULL DEFAULT 0
);
