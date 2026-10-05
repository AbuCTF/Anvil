ALTER TABLE users
    ADD COLUMN IF NOT EXISTS auth_revoked_before TIMESTAMPTZ;

ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS session_id UUID,
    ADD COLUMN IF NOT EXISTS ip_address INET,
    ADD COLUMN IF NOT EXISTS user_agent TEXT,
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

UPDATE refresh_tokens
SET session_id = id
WHERE session_id IS NULL;

UPDATE refresh_tokens
SET revoked_at = created_at
WHERE revoked = TRUE AND revoked_at IS NULL;

ALTER TABLE refresh_tokens
    ALTER COLUMN session_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_session
    ON refresh_tokens(user_id, session_id);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_active_session
    ON refresh_tokens(user_id, session_id, expires_at)
    WHERE revoked = FALSE;
