-- +goose Up
ALTER TABLE connector_configs ADD COLUMN user_id UUID REFERENCES users (id);
ALTER TABLE connector_configs ADD COLUMN shared BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE connector_configs DROP CONSTRAINT connector_configs_name_key;
CREATE UNIQUE INDEX idx_connector_configs_user_name ON connector_configs (user_id, name);

-- +goose Down
DROP INDEX IF EXISTS idx_connector_configs_user_name;
ALTER TABLE connector_configs DROP COLUMN shared;
ALTER TABLE connector_configs DROP COLUMN user_id;
ALTER TABLE connector_configs ADD CONSTRAINT connector_configs_name_key UNIQUE (name);
