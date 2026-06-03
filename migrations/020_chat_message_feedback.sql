-- +goose Up
-- Phase 7: per-assistant-message thumbs feedback (telemetry). Nullable —
-- the vast majority of messages carry no feedback. A CHECK constraint
-- keeps the column to the two known values so eval/telemetry rollups
-- ("how many downvotes this week") stay clean without an enum type.
ALTER TABLE chat_messages
    ADD COLUMN feedback TEXT;

ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_feedback_check
    CHECK (feedback IS NULL OR feedback IN ('up', 'down'));

-- +goose Down
ALTER TABLE chat_messages
    DROP CONSTRAINT chat_messages_feedback_check;

ALTER TABLE chat_messages
    DROP COLUMN feedback;
