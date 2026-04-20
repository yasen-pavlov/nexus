-- +goose Up
-- pgvector was provisioned by the original migration 001 for a
-- chunks.embedding column that has since moved out of Postgres — chunk
-- embeddings live in OpenSearch now (see internal/search). Migration 004
-- dropped the chunks + documents tables but left the extension loaded.
--
-- Drop it explicitly so fresh installs and upgraded instances converge on
-- stock Postgres. `IF EXISTS` keeps the migration idempotent for any fresh
-- install running the edited 001 (which no longer creates the extension).
DROP EXTENSION IF EXISTS vector;

-- +goose Down
CREATE EXTENSION IF NOT EXISTS vector;
