-- 007_dynamic_flags.sql
-- Per-instance dynamic flag support for Docker challenges

-- ============================================================================
-- EXTEND FLAGS TABLE
-- ============================================================================

-- flag_type: 'static' (pre-hashed value stored in flag_hash) or
--            'dynamic' (Anvil generates PREFIX{uuid} per instance and injects
--                       it as the FLAG env var; flag_hash unused)
ALTER TABLE flags ADD COLUMN IF NOT EXISTS flag_type VARCHAR(20) DEFAULT 'static' NOT NULL;

-- e.g. "H7CTF"  →  "H7CTF{<uuid>}"
-- If null, defaults to "FLAG"
ALTER TABLE flags ADD COLUMN IF NOT EXISTS dynamic_flag_prefix VARCHAR(100);

-- ============================================================================
-- INSTANCE FLAGS
-- ============================================================================

-- Stores the unique flag value generated for each (instance, flag) pair.
CREATE TABLE IF NOT EXISTS instance_flags (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    instance_id  UUID NOT NULL REFERENCES instances(id)  ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    flag_id      UUID NOT NULL REFERENCES flags(id)      ON DELETE CASCADE,
    flag_value   TEXT NOT NULL,
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(instance_id, flag_id)
);

CREATE INDEX IF NOT EXISTS idx_instance_flags_user_challenge
    ON instance_flags(user_id, challenge_id);
CREATE INDEX IF NOT EXISTS idx_instance_flags_instance
    ON instance_flags(instance_id);
-- Fast lookup on submission: find any instance that generated this exact value
CREATE INDEX IF NOT EXISTS idx_instance_flags_value
    ON instance_flags(flag_value);

-- ============================================================================
-- FLAG SHARE EVENTS
-- ============================================================================

-- Logged whenever a user submits a dynamic flag that was generated for a
-- *different* user's instance. The submission is accepted transparently
-- (no error shown) so the sharer doesn't realise they were detected.
-- Admins can review this table and issue bans.
CREATE TABLE IF NOT EXISTS flag_share_events (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id      UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    flag_id           UUID NOT NULL REFERENCES flags(id)      ON DELETE CASCADE,
    -- The user who received the flag (original owner)
    owner_user_id     UUID NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    owner_instance_id UUID NOT NULL REFERENCES instances(id)  ON DELETE CASCADE,
    -- The user who submitted the shared flag
    submitter_user_id UUID NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    flag_value        TEXT NOT NULL,
    submitter_ip      VARCHAR(45),
    created_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_flag_share_submitter  ON flag_share_events(submitter_user_id);
CREATE INDEX IF NOT EXISTS idx_flag_share_owner      ON flag_share_events(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_flag_share_challenge  ON flag_share_events(challenge_id);
CREATE INDEX IF NOT EXISTS idx_flag_share_created    ON flag_share_events(created_at);
