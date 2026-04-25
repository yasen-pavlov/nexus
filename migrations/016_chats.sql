-- +goose Up
-- chats and chat_messages back the RAG (Ask) feature. A chat groups a
-- conversation thread; chat_messages holds user, assistant, and (future)
-- tool turns. Per-chat seq is monotonic and assigned inside a tx with
-- a row lock on the parent chat (see internal/store/chats.go).
--
-- Forward-compat columns (citations / tool_calls / usage / model /
-- stop_reason) populate later phases — Phase 2 only writes citations,
-- usage, model, stop_reason; tool_calls stays NULL until Phase 5.
CREATE TABLE chats (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title         TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX chats_user_updated_idx ON chats(user_id, updated_at DESC);

CREATE TABLE chat_messages (
    id           UUID PRIMARY KEY,
    chat_id      UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    seq          INTEGER NOT NULL,
    content      TEXT NOT NULL,
    model        TEXT,
    citations    JSONB,
    tool_calls   JSONB,
    usage        JSONB,
    stop_reason  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, seq)
);
CREATE INDEX chat_messages_chat_seq_idx ON chat_messages(chat_id, seq);

-- +goose Down
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS chats;
