-- +goose Up
-- The `sync_since_days` config key was a stopgap from early development —
-- it takes an int ("sync from the last N days") that the backend converted
-- to an absolute cutoff on every Configure() call, which drifted over time
-- as the app ran. Phase 3 replaces it with an explicit `sync_since` date
-- entered via a UI date picker.
--
-- This migration rewrites existing rows so no connector silently loses its
-- sync window: if a row has `sync_since_days` set and no `sync_since`,
-- we materialize the date now (CURRENT_DATE - days) and store it under
-- `sync_since`. Either way, `sync_since_days` is then removed.
--
-- Note: config is stored unencrypted in a JSONB column for non-sensitive
-- fields (sync_since{,_days} are not in the masking list), so a direct
-- SQL rewrite is safe — we don't need to go through the Go crypto layer.

UPDATE connector_configs
SET config = jsonb_set(
  config - 'sync_since_days',
  '{sync_since}',
  to_jsonb(
    to_char(
      current_date - ((config ->> 'sync_since_days')::int) * interval '1 day',
      'YYYY-MM-DD'
    )
  ),
  true
)
WHERE config ? 'sync_since_days'
  AND (config ->> 'sync_since_days') ~ '^[0-9]+$'
  AND NOT (config ? 'sync_since');

-- Drop the key from any remaining rows — either they already had a
-- sync_since set (in which case sync_since takes over and we just strip
-- the legacy key), or the value wasn't a valid int (in which case we
-- drop it silently; those rows get no sync window, which matches
-- post-migration behavior for unparseable values anyway).
UPDATE connector_configs
SET config = config - 'sync_since_days'
WHERE config ? 'sync_since_days';

-- +goose Down
-- Irreversible: we've lost the "last N days" semantics (since we
-- materialized to an absolute date). Down migration is a no-op so the
-- /goose down command doesn't error, but rolling back to pre-013
-- behavior would require restoring from a DB snapshot.
SELECT 1;
