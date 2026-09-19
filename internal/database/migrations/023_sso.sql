-- 023_sso.sql
-- ZeroPool -> Anvil SSO (model B): link an Anvil user to its ZeroPool participant
-- id so re-logins are idempotent, and a replay guard for the one-time signed
-- handoff tokens. Additive; SSO is gated by the sso.enabled config (default off).

ALTER TABLE users ADD COLUMN IF NOT EXISTS sso_subject VARCHAR(255);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_sso_subject
    ON users(sso_subject) WHERE sso_subject IS NOT NULL;

CREATE TABLE IF NOT EXISTS sso_used_tokens (
    jti        VARCHAR(255) PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sso_used_tokens_expires ON sso_used_tokens(expires_at);
