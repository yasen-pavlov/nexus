-- +goose Up
ALTER TABLE connector_configs ADD COLUMN schedule TEXT NOT NULL DEFAULT '';
ALTER TABLE connector_configs ADD COLUMN last_run TIMESTAMPTZ;

-- +goose Down
ALTER TABLE connector_configs DROP COLUMN IF EXISTS last_run;
ALTER TABLE connector_configs DROP COLUMN IF EXISTS schedule;
