-- 032_koth_entries.sql
-- Per-(team, challenge) KotH buy-in tokens. Each KotH challenge is an INDEPENDENT
-- arena (decision 2026-09-24): a team enters each shared-arena challenge on its own,
-- pays that arena's buy-in, and gets a distinct opaque token to plant on THAT
-- challenge's contested target — so holds are attributed per challenge and one team
-- can hold several arenas at once. Replaces the single global teams.koth_token, which
-- is left in place but no longer written (harmless dead column; dropping it is a
-- finals cleanup). Hold-time still folds into ONE capped teams.koth_score, so the
-- economy invariant (KotH is a bounded bonus tier) holds across any number of arenas.
CREATE TABLE IF NOT EXISTS koth_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id      UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    token        VARCHAR(64) NOT NULL UNIQUE,
    entered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (team_id, challenge_id)
);
CREATE INDEX IF NOT EXISTS idx_koth_entries_challenge ON koth_entries(challenge_id);
CREATE INDEX IF NOT EXISTS idx_koth_entries_token ON koth_entries(token);
