-- 030_koth_team_native.sql
-- Bridge the existing game/ KotH engine (migration 010, keyed on the parallel
-- game_teams identity) onto the real teams table for quals shared-arena KotH.
--
-- Approach (deliberate, reversible): a team that buys into the arena gets a
-- game_teams MIRROR row with the SAME id (game_teams.id = teams.id) and
-- token = teams.koth_token, so the existing engine (pollHills -> game_koth_control
-- -> game_standings.koth) scores real teams with no query/FK rewrite. The engine's
-- game_standings.koth is then folded into teams.koth_score for the ONE unified
-- scoreboard. Collapsing game_teams entirely is a finals cleanup.

-- Opaque per-team KotH marker: the token a team plants on the shared hill to claim
-- it. NULL until the team buys in. UNIQUE (Postgres allows multiple NULLs).
ALTER TABLE teams ADD COLUMN IF NOT EXISTS koth_token VARCHAR(64) UNIQUE;

-- KotH hold-time contribution to the unified scoreboard. POINTS ONLY, never credits
-- (a credit faucet would let arena-farming buy jeopardy compute-reach). Kept separate
-- from jeopardy score so it stays outside the economy's crowd-decay / wrong-sub
-- mechanics; the scorer caps it so max-KotH < a strong jeopardy run.
ALTER TABLE teams ADD COLUMN IF NOT EXISTS koth_score NUMERIC(12,3) NOT NULL DEFAULT 0;

-- Feature flag: OFF => the game engine behaves exactly as before (game_teams only,
-- no fold into teams.total_score). Flip ON only once the pilot is validated.
INSERT INTO platform_settings (key, value, description, category) VALUES
    ('koth_native_enabled', 'false', 'Wire the KotH engine onto real teams (quals shared-arena mode)', 'general')
ON CONFLICT (key) DO NOTHING;

-- Hard cap on a team's KotH contribution to the unified board, so a KotH-perfect
-- team can't out-rank a strong jeopardy run (jeopardy stays decisive; KotH is a
-- bounded bonus tier). Default is intentionally high (effectively uncapped) for
-- pilot testing; CTF26-1's 12h+release economy re-sim sets the real ceiling.
INSERT INTO platform_settings (key, value, description, category) VALUES
    ('koth_score_cap', '100000', 'Max KotH points a team can add to the unified scoreboard', 'general')
ON CONFLICT (key) DO NOTHING;
