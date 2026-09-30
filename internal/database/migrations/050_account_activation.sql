ALTER TABLE data_import_jobs ADD COLUMN IF NOT EXISTS options JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS account_activation_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose VARCHAR(32) NOT NULL DEFAULT 'activation',
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_activation_tokens_purpose CHECK (purpose IN ('activation', 'password_reset'))
);

CREATE INDEX IF NOT EXISTS idx_account_activation_tokens_user ON account_activation_tokens(user_id, purpose, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_account_activation_tokens_active ON account_activation_tokens(expires_at) WHERE used_at IS NULL;

INSERT INTO platform_settings (key, value, description, category)
VALUES ('event.public_url', '""', 'Public HTTPS origin used in participant links', 'event')
ON CONFLICT (key) DO NOTHING;
