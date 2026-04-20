-- +goose Up
-- Per-connector self-identity: who the Nexus user IS on the external system
-- the connector represents (e.g. their own Telegram user ID + display name).
-- Populated post-auth by connectors that can discover it. Nullable because
-- shared connectors and connectors without a notion of "self" leave it empty.
ALTER TABLE connector_configs
    ADD COLUMN external_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN external_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE connector_configs
    DROP COLUMN external_name,
    DROP COLUMN external_id;
