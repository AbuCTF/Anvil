-- Explicit logout must invalidate the bearer token immediately, not only remove
-- it from one browser. Rows naturally age out with the token expiry.
-- user_id is a bare UUID, not a FK: adding REFERENCES users(id) takes a
-- ShareRowExclusiveLock on users at apply time, which can briefly block user-table
-- writes mid-event. The FK is deferred to post-event; nothing depends on it here.
CREATE TABLE IF NOT EXISTS revoked_access_tokens (
    token_hash CHAR(64) PRIMARY KEY,
    user_id UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_revoked_access_tokens_expires
    ON revoked_access_tokens(expires_at);
