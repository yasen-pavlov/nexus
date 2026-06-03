-- +goose Up
-- Phase 4 polish: persist server-measured turn duration so the FE can
-- show the same value before and after a page refresh. The live timer
-- (FE clock) and the persisted-message timer (server clock) drift by
-- network + SSE-flush latency; recording duration once on the server
-- removes the divergence and lets the assistant-turn footer render the
-- canonical wall-clock for the orchestrator's runTurn.
ALTER TABLE chat_messages
    ADD COLUMN duration_ms INTEGER;

-- +goose Down
ALTER TABLE chat_messages
    DROP COLUMN duration_ms;
