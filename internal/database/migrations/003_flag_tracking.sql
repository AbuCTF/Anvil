-- 003_flag_tracking.sql
-- Adds flag submission tracking and solve records

-- ============================================================================
-- FLAG ATTEMPTS
-- ============================================================================

CREATE TABLE IF NOT EXISTS flag_attempts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    flag_id UUID REFERENCES flags(id) ON DELETE SET NULL,
    submitted_flag TEXT NOT NULL,
    is_correct BOOLEAN NOT NULL,
    ip_address VARCHAR(45),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_flag_attempts_user ON flag_attempts(user_id);
CREATE INDEX idx_flag_attempts_challenge ON flag_attempts(challenge_id);
CREATE INDEX idx_flag_attempts_flag ON flag_attempts(flag_id);
CREATE INDEX idx_flag_attempts_created ON flag_attempts(created_at);

-- ============================================================================
-- SOLVES
-- ============================================================================

CREATE TABLE IF NOT EXISTS solves (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id UUID REFERENCES challenges(id) ON DELETE CASCADE,
    flag_id UUID NOT NULL REFERENCES flags(id) ON DELETE CASCADE,
    points_awarded INTEGER NOT NULL,
    solved_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, flag_id)
);

CREATE INDEX idx_solves_user ON solves(user_id);
CREATE INDEX idx_solves_challenge ON solves(challenge_id);
CREATE INDEX idx_solves_flag ON solves(flag_id);
CREATE INDEX idx_solves_solved_at ON solves(solved_at);

-- ============================================================================
-- USER COOLDOWNS (for VM restart limits)
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_cooldowns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    cooldown_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, challenge_id)
);

CREATE INDEX idx_user_cooldowns_user ON user_cooldowns(user_id);
CREATE INDEX idx_user_cooldowns_expires ON user_cooldowns(cooldown_until);
