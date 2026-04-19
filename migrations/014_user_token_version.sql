-- +goose Up
-- token_version is bumped on password change so existing JWTs (which carry
-- the version they were minted with) become invalid. Auth middleware compares
-- the claim against the row's current value and rejects mismatches.
ALTER TABLE users ADD COLUMN token_version INT NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE users DROP COLUMN token_version;
