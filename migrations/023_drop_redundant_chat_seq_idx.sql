-- +goose Up
-- Drop the redundant chat_messages_chat_seq_idx (migration 016).
--
-- chat_messages already declares UNIQUE (chat_id, seq), which Postgres backs
-- with a unique btree index on exactly (chat_id, seq). chat_messages_chat_seq_idx
-- was a second index on the identical key — Postgres maintained both on every
-- AppendMessage insert (one per chat turn), doubling write amplification and
-- disk for that key with zero query benefit: the planner uses the unique index
-- for ListMessages' `WHERE chat_id = $1 ORDER BY seq` and the MAX(seq) lookup.
DROP INDEX IF EXISTS chat_messages_chat_seq_idx;

-- +goose Down
CREATE INDEX chat_messages_chat_seq_idx ON chat_messages (chat_id, seq);
