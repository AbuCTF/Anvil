-- 021_teams_membership.sql
-- Teams / membership layer (Abu 2026-09-19): per-member accounts joined into teams,
-- team-shared instances, team-aggregated scoring. All additive and gated by the
-- 'teams_mode' platform setting (default off => zero change to current per-user behavior).
-- Ported from AbuCTF/CTFRiced's is_teams_mode() + team_id-keyed instance model.

CREATE TABLE IF NOT EXISTS teams (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name            VARCHAR(100) NOT NULL UNIQUE,
    join_code       VARCHAR(64)  NOT NULL UNIQUE,
    join_expires_at TIMESTAMPTZ,                       -- optional join-window expiry
    max_members     INTEGER,                           -- optional size cap (NULL = unlimited)
    total_score     INTEGER NOT NULL DEFAULT 0,        -- denormalized team score (standard mode)
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_teams_join_code   ON teams(join_code);
CREATE INDEX IF NOT EXISTS idx_teams_total_score ON teams(total_score DESC);

-- Membership + team-shared instance ownership. Nullable so existing rows are untouched.
ALTER TABLE users     ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams(id) ON DELETE SET NULL;
ALTER TABLE instances ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_users_team     ON users(team_id);
CREATE INDEX IF NOT EXISTS idx_instances_team ON instances(team_id);

-- Runtime toggle (mirrors scoreboard_enabled/registration_mode). Off => per-user behavior unchanged.
INSERT INTO platform_settings (key, value, description, category) VALUES
    ('teams_mode', 'false', 'Whether the competition is team-based (per-team scoring + shared instances)', 'general')
ON CONFLICT (key) DO NOTHING;
