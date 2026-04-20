-- +goose Up
-- Enforce that every connector either has an owner or is shared.
-- This catches programming bugs where a connector is created without
-- proper ownership context.
ALTER TABLE connector_configs
    ADD CONSTRAINT connector_owner_or_shared
    CHECK (user_id IS NOT NULL OR shared = TRUE);

-- +goose Down
ALTER TABLE connector_configs DROP CONSTRAINT connector_owner_or_shared;
