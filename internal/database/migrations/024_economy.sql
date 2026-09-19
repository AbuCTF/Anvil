-- 024_economy.sql
-- Ledger economy (Abu's firm go for quals). Dual-ledger OVERLAY on top of the
-- membership layer: the scoreboard still ranks POINTS; credits are a separate
-- spendable leash that gates launching. All additive and gated by the
-- economy.enabled config (default off => standard scoring, the validated fallback).
-- Design: /home/abu/Main/Projects/CTF26/ledger-anvil-integration-draft.md
-- Params/contract: ledger-sim/reference_config.json + economy_test_vectors.md.

-- Per-team economy state: point total (the rank), credit balance (the leash),
-- and the once-per-team grant/bailout flags. Ledgers below are the audit source.
CREATE TABLE IF NOT EXISTS economy_team_score (
    team_id      UUID PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    points       NUMERIC(12,3) NOT NULL DEFAULT 0,   -- sum of held-solve current values (the scoreboard rank)
    credits      NUMERIC(12,3) NOT NULL DEFAULT 0,   -- current credit balance (ledger is the audit trail)
    grant_issued BOOLEAN       NOT NULL DEFAULT FALSE,
    bailout_used BOOLEAN       NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- Append-only credit ledger (grant | launch_spend | clean_refund | abandon_refund
-- | extend_spend | bailout | p2c_convert | c2p_freeze). Signed amount + running balance.
CREATE TABLE IF NOT EXISTS economy_credit_events (
    id            BIGSERIAL PRIMARY KEY,
    team_id       UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    amount        NUMERIC(12,3) NOT NULL,
    balance_after NUMERIC(12,3) NOT NULL,
    challenge_id  UUID REFERENCES challenges(id) ON DELETE SET NULL,
    instance_id   UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_economy_credit_events_team ON economy_credit_events(team_id, id);

-- Append-only point ledger (solve_hold | crowd_recompute | wrong_sub | convert_out
-- | freeze_convert_in). value_after = this team's current value for the challenge.
CREATE TABLE IF NOT EXISTS economy_point_events (
    id           BIGSERIAL PRIMARY KEY,
    team_id      UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    value_after  NUMERIC(12,3) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_economy_point_events_team ON economy_point_events(team_id, id);
CREATE INDEX IF NOT EXISTS idx_economy_point_events_chal ON economy_point_events(challenge_id);

-- Per-team-per-challenge live state: the 3-open concurrency gate, the wrong-sub
-- multiplier counter, the timer, and whether the team holds the solve.
CREATE TABLE IF NOT EXISTS economy_challenge_state (
    team_id         UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    challenge_id    UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'unopened', -- unopened|open|solved|abandoned|expired
    opened_at       TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    extensions_used SMALLINT NOT NULL DEFAULT 0,
    wrong_subs      INTEGER  NOT NULL DEFAULT 0,
    holds_solve     BOOLEAN  NOT NULL DEFAULT FALSE,
    current_value   NUMERIC(12,3) NOT NULL DEFAULT 0, -- this team's current point value for the challenge (for retroactive decay deltas)
    PRIMARY KEY (team_id, challenge_id)
);
-- Fast concurrency-cap count of a team's open challenges.
CREATE INDEX IF NOT EXISTS idx_economy_state_open ON economy_challenge_state(team_id) WHERE status = 'open';

-- Global per-challenge solve count drives retroactive crowd decay (all holders
-- share the current value; recomputed on each solve).
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS economy_solve_count INTEGER NOT NULL DEFAULT 0;

-- Runtime toggle (mirrors teams_mode); off => standard scoring unchanged.
INSERT INTO platform_settings (key, value, description, category) VALUES
    ('economy_mode', 'false', 'Whether the Ledger economy (credits + launch-gating + dynamic points) is active', 'general')
ON CONFLICT (key) DO NOTHING;
