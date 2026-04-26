-- +goose Up
-- Phase 4 RAG: persist what the orchestrator decided BEFORE generation.
--
-- rewritten_query holds the rewriter's output (when the rewriter ran);
-- skipped_retrieval flags turns where the rewriter said the question
-- could be answered from chat history alone (greetings, meta questions,
-- history-only follow-ups). Together these let the FE phase strip
-- repopulate on chat reload, the Phase 7 eval harness know which query
-- actually hit OpenSearch, and operators debug "why didn't this turn
-- retrieve anything?" from a single SELECT.
ALTER TABLE chat_messages
    ADD COLUMN rewritten_query TEXT;

ALTER TABLE chat_messages
    ADD COLUMN skipped_retrieval BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE chat_messages
    DROP COLUMN skipped_retrieval;

ALTER TABLE chat_messages
    DROP COLUMN rewritten_query;
