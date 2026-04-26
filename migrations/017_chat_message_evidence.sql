-- +goose Up
-- Persist the evidence chunks that backed an assistant turn alongside
-- the citations that already live on the row. ChunkPreview.id is the
-- OpenSearch chunk handle, so the column doubles as a stable graph
-- pointer from any chat message back to the documents it was grounded
-- on (and onward to /related, /conversations, /blob, etc.).
ALTER TABLE chat_messages
    ADD COLUMN evidence JSONB;

-- +goose Down
ALTER TABLE chat_messages
    DROP COLUMN evidence;
