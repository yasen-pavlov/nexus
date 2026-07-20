-- +goose Up
-- Restore GLOBAL (type, name) uniqueness for connectors.
--
-- Everything downstream identifies a connector's data by (type, name):
--   * the OpenSearch document id is derived from source_type + source_name +
--     source_id (model.DocumentID),
--   * binary_store_entries is keyed (source_type, source_name, source_id),
--   * the ownership-rewrite and cache-delete operations filter on
--     (source_type, source_name).
--
-- The per-user index added in migration 007 (user_id, name) let two users
-- create same-type, same-name connectors, which then collided on all of the
-- above: one user's sync would overwrite the other's indexed documents and
-- cached blobs, and a share-toggle or delete on one connector would rewrite
-- or wipe the other's data. Enforcing global (type, name) uniqueness closes
-- that hazard. Different-type connectors may still share a name (their
-- document ids differ by type), so the constraint is (type, name), not name.
--
-- If this migration fails with a unique-violation, the database already
-- contains a colliding pair — resolve it by renaming one connector before
-- upgrading (the collision is the very corruption this prevents).
DROP INDEX IF EXISTS idx_connector_configs_user_name;
CREATE UNIQUE INDEX idx_connector_configs_type_name ON connector_configs (type, name);

-- +goose Down
DROP INDEX IF EXISTS idx_connector_configs_type_name;
CREATE UNIQUE INDEX idx_connector_configs_user_name ON connector_configs (user_id, name);
