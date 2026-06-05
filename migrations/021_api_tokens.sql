-- +goose Up
-- api_tokens backs long-lived "personal access token" auth so external
-- agents/CLIs can call the API without an interactive login. A token acts
-- as its owning user: the validator resolves the user's role live at
-- request time, so demoting or deleting the user immediately neuters every
-- token they minted (ON DELETE CASCADE cleans the rows up too).
--
-- We store only a SHA-256 hash of the token, never the plaintext. Tokens
-- are high-entropy random strings (not low-entropy passwords), so a fast
-- one-way hash with a UNIQUE index gives O(1) lookup without bcrypt's
-- per-row salted comparison. The plaintext is shown to the user exactly
-- once, at creation.
--
-- expires_at NULL means "never expires" — convenient for an always-on
-- agent; revoke by deleting the row. last_used_at is best-effort,
-- throttled telemetry so the UI can show "last used 3h ago".
CREATE TABLE api_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ
);

CREATE INDEX api_tokens_user_created_idx ON api_tokens (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
