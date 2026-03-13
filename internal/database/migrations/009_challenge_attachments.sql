-- 009_challenge_attachments.sql
-- Adds file attachment support per challenge and flag submission brute-force tracking

-- ============================================================================
-- CHALLENGE ATTACHMENTS
-- ============================================================================

CREATE TABLE challenge_attachments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    uploaded_by UUID NOT NULL REFERENCES users(id),

    -- File info
    filename VARCHAR(500) NOT NULL,       -- Original filename shown to participants
    file_size BIGINT NOT NULL,
    content_type VARCHAR(200),

    -- Storage
    storage_key VARCHAR(500) NOT NULL UNIQUE,  -- Relative path under the storage backend root (e.g. 'challenge-attachments/{challenge_id}/{uuid}')

    -- Optional metadata
    description TEXT,
    sort_order INTEGER DEFAULT 0,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_challenge_attachments_challenge ON challenge_attachments(challenge_id);
CREATE INDEX idx_challenge_attachments_uploaded_by ON challenge_attachments(uploaded_by);

CREATE TRIGGER update_challenge_attachments_updated_at
    BEFORE UPDATE ON challenge_attachments
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ============================================================================
-- FLAG SUBMISSION BRUTE-FORCE PROTECTION
-- ============================================================================

-- Track consecutive wrong attempts per (user, challenge) for lockout
CREATE TABLE flag_attempt_lockouts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    wrong_attempts INTEGER NOT NULL DEFAULT 1,
    first_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE (user_id, challenge_id)
);

CREATE INDEX idx_flag_lockouts_user_challenge ON flag_attempt_lockouts(user_id, challenge_id);
CREATE INDEX idx_flag_lockouts_locked_until ON flag_attempt_lockouts(locked_until);
