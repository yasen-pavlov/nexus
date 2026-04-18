-- +goose Up
-- Persistent history of sync runs. Each row represents one execution of a
-- connector's sync pipeline, whether triggered manually from the API or
-- fired by the cron scheduler. The row is inserted at sync start and
-- updated at completion; this lets the UI show in-progress runs without
-- querying in-memory state, and gives us a canonical record that survives
-- process restarts.
--
-- sync_runs is the source of truth for history; internal/api/SyncJobManager
-- is a pure in-memory broadcaster for live progress (SSE) + cancel
-- registry, keyed by the same UUID so consumers can correlate.
--
-- No retention logic yet — the table is expected to grow. A sweeper can be
-- added later once we have real data on growth rates.
CREATE TABLE sync_runs (
    id               UUID PRIMARY KEY,
    connector_id     UUID NOT NULL REFERENCES connector_configs(id) ON DELETE CASCADE,
    status           TEXT NOT NULL, -- 'running' | 'completed' | 'failed' | 'canceled'
    docs_total       INT NOT NULL DEFAULT 0,
    docs_processed   INT NOT NULL DEFAULT 0,
    docs_deleted     INT NOT NULL DEFAULT 0,
    errors           INT NOT NULL DEFAULT 0,
    error_message    TEXT NOT NULL DEFAULT '',
    started_at       TIMESTAMPTZ NOT NULL,
    completed_at     TIMESTAMPTZ
);

-- Activity timeline query: "most recent runs for this connector".
CREATE INDEX idx_sync_runs_connector_started
    ON sync_runs (connector_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS sync_runs;
