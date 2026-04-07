-- +goose Up
CREATE TABLE connector_configs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type       TEXT NOT NULL,
    name       TEXT NOT NULL UNIQUE,
    config     JSONB NOT NULL DEFAULT '{}',
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_connector_configs_type ON connector_configs (type);

-- +goose Down
DROP TABLE IF EXISTS connector_configs;
