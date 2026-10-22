ALTER TABLE vpn_configs
    ADD COLUMN IF NOT EXISTS private_key_ciphertext BYTEA;

DROP INDEX IF EXISTS idx_vpn_configs_ip;
CREATE UNIQUE INDEX IF NOT EXISTS idx_vpn_configs_active_ip
    ON vpn_configs(assigned_ip) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS vpn_peer_history (
    id BIGSERIAL PRIMARY KEY,
    user_id UUID NOT NULL,
    team_id UUID,
    team_number INTEGER,
    action VARCHAR(16) NOT NULL CHECK (action IN ('issued', 'rotated', 'revoked')),
    public_key VARCHAR(100) NOT NULL,
    assigned_ip INET NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vpn_peer_history_user_time
    ON vpn_peer_history(user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_vpn_peer_history_public_key
    ON vpn_peer_history(public_key);
