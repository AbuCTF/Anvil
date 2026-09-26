-- Explicit logout must invalidate the bearer token immediately, not only remove
-- it from one browser. Rows naturally age out with the token expiry.
CREATE TABLE IF NOT EXISTS revoked_access_tokens (
    token_hash CHAR(64) PRIMARY KEY,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_revoked_access_tokens_expires
    ON revoked_access_tokens(expires_at);
