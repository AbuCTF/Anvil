-- 001_initial_schema.sql
-- Anvil Platform - Initial Database Schema

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================================
-- USERS & AUTHENTICATION
-- ============================================================================

-- User roles enum
CREATE TYPE user_role AS ENUM ('user', 'author', 'admin');

-- User status enum  
CREATE TYPE user_status AS ENUM ('active', 'suspended', 'banned');

-- Registration mode enum (for platform config)
CREATE TYPE registration_mode AS ENUM ('open', 'invite', 'token', 'disabled');

-- Users table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username VARCHAR(50) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE,
    password_hash VARCHAR(255),
    role user_role DEFAULT 'user',
    status user_status DEFAULT 'active',
    
    -- Profile
    display_name VARCHAR(100),
    avatar_url VARCHAR(500),
    bio TEXT,
    
    -- Stats (denormalized for performance)
    total_score INTEGER DEFAULT 0,
    challenges_solved INTEGER DEFAULT 0,
    
    -- Metadata
    email_verified BOOLEAN DEFAULT FALSE,
    last_login_at TIMESTAMPTZ,
    last_login_ip VARCHAR(45),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_role ON users(role);
CREATE INDEX idx_users_total_score ON users(total_score DESC);

-- Team tokens (for token-based registration)
CREATE TABLE team_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    token VARCHAR(50) UNIQUE NOT NULL,
    team_name VARCHAR(100) NOT NULL,
    max_uses INTEGER DEFAULT 1,
    current_uses INTEGER DEFAULT 0,
    expires_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_team_tokens_token ON team_tokens(token);

-- Invite codes
CREATE TABLE invite_codes (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    code VARCHAR(50) UNIQUE NOT NULL,
    max_uses INTEGER DEFAULT 1,
    current_uses INTEGER DEFAULT 0,
    expires_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_invite_codes_code ON invite_codes(code);

-- Sessions (for token-based auth without full registration)
CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    token_id UUID REFERENCES team_tokens(id) ON DELETE CASCADE,
    session_token VARCHAR(255) UNIQUE NOT NULL,
    ip_address VARCHAR(45),
    user_agent TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT session_has_user_or_token CHECK (
        user_id IS NOT NULL OR token_id IS NOT NULL
    )
);

CREATE INDEX idx_sessions_token ON sessions(session_token);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- Refresh tokens
CREATE TABLE refresh_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens(token_hash);

-- ============================================================================
-- CHALLENGES
-- ============================================================================

-- Challenge difficulty enum
CREATE TYPE challenge_difficulty AS ENUM ('easy', 'medium', 'hard', 'insane');

-- Challenge status enum
CREATE TYPE challenge_status AS ENUM ('draft', 'published', 'archived');

-- Categories
CREATE TABLE categories (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) UNIQUE NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    color VARCHAR(7), -- hex color
    icon VARCHAR(50),
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Challenges (parent container for B2R machines)
CREATE TABLE challenges (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    
    -- Basic info
    name VARCHAR(200) NOT NULL,
    slug VARCHAR(200) UNIQUE NOT NULL,
    description TEXT,
    difficulty challenge_difficulty NOT NULL,
    category_id UUID REFERENCES categories(id),
    status challenge_status DEFAULT 'draft',
    
    -- Author
    author_id UUID REFERENCES users(id),
    author_name VARCHAR(100), -- Display name (in case author deleted)
    
    -- Container configuration
    container_image VARCHAR(500) NOT NULL,
    container_registry VARCHAR(200), -- Optional private registry
    container_tag VARCHAR(100) DEFAULT 'latest',
    
    -- Resource limits
    cpu_limit VARCHAR(20) DEFAULT '1',      -- e.g., "0.5", "1", "2"
    memory_limit VARCHAR(20) DEFAULT '512m', -- e.g., "256m", "1g"
    
    -- Network configuration
    exposed_ports JSONB DEFAULT '[]',  -- [{"port": 80, "protocol": "tcp"}]
    network_mode VARCHAR(50) DEFAULT 'bridge',
    
    -- Instance settings (can override platform defaults)
    instance_timeout INTEGER, -- seconds, null = use platform default
    max_extensions INTEGER,   -- null = use platform default
    
    -- Scoring
    base_points INTEGER DEFAULT 100,
    
    -- Flags count (denormalized)
    total_flags INTEGER DEFAULT 0,
    
    -- Stats (denormalized)
    total_solves INTEGER DEFAULT 0,
    total_attempts INTEGER DEFAULT 0,
    
    -- Metadata
    release_date TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_challenges_slug ON challenges(slug);
CREATE INDEX idx_challenges_status ON challenges(status);
CREATE INDEX idx_challenges_category ON challenges(category_id);
CREATE INDEX idx_challenges_difficulty ON challenges(difficulty);
CREATE INDEX idx_challenges_author ON challenges(author_id);

-- Flags (sub-challenges within a B2R machine)
CREATE TABLE flags (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    
    -- Flag details
    name VARCHAR(100) NOT NULL, -- e.g., "User Flag", "Root Flag"
    description TEXT,           -- Hint about what this flag represents
    sort_order INTEGER DEFAULT 0,
    
    -- Flag value (stored as hash for security)
    flag_hash VARCHAR(255) NOT NULL,
    flag_format VARCHAR(100), -- e.g., "ANVIL{...}", regex pattern
    is_regex BOOLEAN DEFAULT FALSE,
    case_sensitive BOOLEAN DEFAULT TRUE,
    
    -- Scoring
    points INTEGER NOT NULL,
    
    -- Stats
    total_solves INTEGER DEFAULT 0,
    first_blood_user_id UUID REFERENCES users(id),
    first_blood_at TIMESTAMPTZ,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(challenge_id, sort_order)
);

CREATE INDEX idx_flags_challenge ON flags(challenge_id);

-- Hints
CREATE TABLE hints (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    flag_id UUID REFERENCES flags(id) ON DELETE CASCADE, -- Optional: hint for specific flag
    
    content TEXT NOT NULL,
    cost INTEGER DEFAULT 0, -- Point deduction for viewing
    sort_order INTEGER DEFAULT 0,
    
    -- Unlock conditions
    unlock_after_attempts INTEGER, -- Auto-show after X failed attempts
    unlock_after_time INTEGER,     -- Auto-show after X seconds
    
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_hints_challenge ON hints(challenge_id);

-- ============================================================================
-- INSTANCES (Running containers)
-- ============================================================================

-- Instance status enum
CREATE TYPE instance_status AS ENUM (
    'pending',     -- Queued for creation
    'creating',    -- Being created
    'running',     -- Active and accessible
    'stopping',    -- Being stopped
    'stopped',     -- Stopped but not removed
    'failed',      -- Failed to start
    'expired'      -- Timed out
);

CREATE TABLE instances (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge_id UUID NOT NULL REFERENCES challenges(id),
    user_id UUID REFERENCES users(id),
    session_id UUID REFERENCES sessions(id),
    
    -- Container details
    container_id VARCHAR(100),
    container_name VARCHAR(200),
    status instance_status DEFAULT 'pending',
    
    -- Network
    ip_address VARCHAR(45),
    assigned_ports JSONB DEFAULT '{}', -- {"80": 32001, "22": 32002}
    
    -- Timing
    started_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    stopped_at TIMESTAMPTZ,
    extensions_used INTEGER DEFAULT 0,
    
    -- Error tracking
    error_message TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT instance_has_user_or_session CHECK (
        user_id IS NOT NULL OR session_id IS NOT NULL
    )
);

CREATE INDEX idx_instances_challenge ON instances(challenge_id);
CREATE INDEX idx_instances_user ON instances(user_id);
CREATE INDEX idx_instances_status ON instances(status);
CREATE INDEX idx_instances_expires ON instances(expires_at);

-- ============================================================================
-- SUBMISSIONS & SCORING
-- ============================================================================

CREATE TABLE submissions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id),
    session_id UUID REFERENCES sessions(id),
    challenge_id UUID NOT NULL REFERENCES challenges(id),
    flag_id UUID REFERENCES flags(id),
    instance_id UUID REFERENCES instances(id),
    
    -- Submission details
    submitted_flag VARCHAR(500) NOT NULL,
    is_correct BOOLEAN NOT NULL,
    points_awarded INTEGER DEFAULT 0,
    
    -- Metadata
    ip_address VARCHAR(45),
    user_agent TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT submission_has_user_or_session CHECK (
        user_id IS NOT NULL OR session_id IS NOT NULL
    )
);

CREATE INDEX idx_submissions_user ON submissions(user_id);
CREATE INDEX idx_submissions_challenge ON submissions(challenge_id);
CREATE INDEX idx_submissions_flag ON submissions(flag_id);
CREATE INDEX idx_submissions_correct ON submissions(is_correct);
CREATE INDEX idx_submissions_created ON submissions(created_at);

-- Solved flags (unique constraint to prevent double-scoring)
CREATE TABLE solved_flags (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id),
    session_id UUID REFERENCES sessions(id),
    challenge_id UUID NOT NULL REFERENCES challenges(id),
    flag_id UUID NOT NULL REFERENCES flags(id),
    submission_id UUID REFERENCES submissions(id),
    
    points_awarded INTEGER NOT NULL,
    solved_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT solved_has_user_or_session CHECK (
        user_id IS NOT NULL OR session_id IS NOT NULL
    )
);

-- Unique constraint: one solve per user/session per flag
CREATE UNIQUE INDEX idx_solved_flags_user_flag ON solved_flags(user_id, flag_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_solved_flags_session_flag ON solved_flags(session_id, flag_id) WHERE session_id IS NOT NULL;

CREATE INDEX idx_solved_flags_challenge ON solved_flags(challenge_id);
CREATE INDEX idx_solved_flags_solved_at ON solved_flags(solved_at);

-- Hint unlocks
CREATE TABLE hint_unlocks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id),
    session_id UUID REFERENCES sessions(id),
    hint_id UUID NOT NULL REFERENCES hints(id),
    points_deducted INTEGER DEFAULT 0,
    unlocked_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT hint_unlock_has_user_or_session CHECK (
        user_id IS NOT NULL OR session_id IS NOT NULL
    )
);

CREATE UNIQUE INDEX idx_hint_unlocks_user ON hint_unlocks(user_id, hint_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_hint_unlocks_session ON hint_unlocks(session_id, hint_id) WHERE session_id IS NOT NULL;

-- ============================================================================
-- VPN
-- ============================================================================

CREATE TABLE vpn_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
    
    -- WireGuard keys
    private_key VARCHAR(100) NOT NULL,
    public_key VARCHAR(100) NOT NULL,
    
    -- Assigned IP
    assigned_ip VARCHAR(45) NOT NULL,
    
    -- Status
    is_active BOOLEAN DEFAULT TRUE,
    last_handshake TIMESTAMPTZ,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT vpn_has_user_or_session CHECK (
        user_id IS NOT NULL OR session_id IS NOT NULL
    )
);

CREATE UNIQUE INDEX idx_vpn_configs_user ON vpn_configs(user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX idx_vpn_configs_session ON vpn_configs(session_id) WHERE session_id IS NOT NULL;
CREATE UNIQUE INDEX idx_vpn_configs_ip ON vpn_configs(assigned_ip);
CREATE INDEX idx_vpn_configs_public_key ON vpn_configs(public_key);

-- ============================================================================
-- PLATFORM CONFIGURATION
-- ============================================================================

CREATE TABLE platform_settings (
    key VARCHAR(100) PRIMARY KEY,
    value JSONB NOT NULL,
    description TEXT,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    updated_by UUID REFERENCES users(id)
);

-- Insert default settings
INSERT INTO platform_settings (key, value, description) VALUES
    ('platform_name', '"Anvil"', 'Platform display name'),
    ('platform_description', '"Forge your skills"', 'Platform tagline'),
    ('registration_mode', '"open"', 'Registration mode: open, invite, token, disabled'),
    ('require_email_verify', 'false', 'Require email verification'),
    ('scoring_enabled', 'true', 'Enable scoring system'),
    ('scoreboard_enabled', 'true', 'Enable scoreboard'),
    ('scoreboard_public', 'true', 'Make scoreboard public'),
    ('scoring_mode', '"static"', 'Scoring mode: static, dynamic, time_decay'),
    ('dynamic_scoring_config', '{"min_points": 100, "max_points": 500, "decay": 20}', 'Dynamic scoring configuration'),
    ('flag_submission_enabled', 'true', 'Enable flag submission'),
    ('hints_enabled', 'true', 'Enable hints system'),
    ('default_instance_timeout', '7200', 'Default instance timeout in seconds'),
    ('max_instance_extensions', '3', 'Maximum instance extensions allowed'),
    ('extension_duration', '1800', 'Extension duration in seconds'),
    ('maintenance_mode', 'false', 'Platform maintenance mode'),
    ('maintenance_message', '""', 'Maintenance mode message');

-- ============================================================================
-- AUDIT LOG
-- ============================================================================

CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id),
    action VARCHAR(100) NOT NULL,
    entity_type VARCHAR(50),
    entity_id UUID,
    old_values JSONB,
    new_values JSONB,
    ip_address VARCHAR(45),
    user_agent TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_log_user ON audit_log(user_id);
CREATE INDEX idx_audit_log_action ON audit_log(action);
CREATE INDEX idx_audit_log_entity ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_log_created ON audit_log(created_at);

-- ============================================================================
-- HELPER FUNCTIONS
-- ============================================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply triggers
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_challenges_updated_at BEFORE UPDATE ON challenges
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_flags_updated_at BEFORE UPDATE ON flags
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_instances_updated_at BEFORE UPDATE ON instances
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_vpn_configs_updated_at BEFORE UPDATE ON vpn_configs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Function to hash flags
CREATE OR REPLACE FUNCTION hash_flag(flag TEXT)
RETURNS VARCHAR(255) AS $$
BEGIN
    RETURN encode(digest(flag, 'sha256'), 'hex');
END;
$$ language 'plpgsql';

-- Function to verify flag
CREATE OR REPLACE FUNCTION verify_flag(submitted TEXT, stored_hash VARCHAR(255))
RETURNS BOOLEAN AS $$
BEGIN
    RETURN hash_flag(submitted) = stored_hash;
END;
$$ language 'plpgsql';
