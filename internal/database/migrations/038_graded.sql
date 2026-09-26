-- 038_graded.sql
-- Graded (relative-score) challenges: instead of a binary flag, a grader running
-- inside the team's instance (in an internal role players can't reach) scores
-- each solution a team submits through the challenge's own portal. Per solution
-- the grader first asks /api/v1/graded/evaluate (the ledger charge point), then
-- posts the score in [0,1] to /api/v1/graded/report. The platform keeps each
-- team's monotonic best and awards points in proportion. Both calls are
-- HMAC-signed with a per-challenge secret that only the grader role's env
-- receives (${GRADER_SECRET}); it is never returned by a player-facing API.

ALTER TABLE challenges ADD COLUMN IF NOT EXISTS scoring_mode TEXT NOT NULL DEFAULT 'flag'
    CHECK (scoring_mode IN ('flag', 'graded'));

-- every challenge gets its own secret up front (volatile default => one per row);
-- only graded challenges ever use it. rotated from the admin editor.
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS graded_secret TEXT NOT NULL
    DEFAULT encode(gen_random_bytes(32), 'hex');

-- per-team best: the depth race + the points basis (points = round(best * base_points)).
CREATE TABLE IF NOT EXISTS graded_scores (
    team_id      UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    best         DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (best >= 0 AND best <= 1),
    raw          JSONB,        -- grader detail attached to the best report (shown to the team)
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, challenge_id)
);
CREATE INDEX IF NOT EXISTS idx_graded_scores_race ON graded_scores(challenge_id, best DESC, updated_at);

-- one row per evaluation a grader was allowed to run. seq/charged are fixed at
-- admission; a report closes it (ok | infra_error, which refunds), and one left
-- pending past 15 minutes is expired + refunded by the server janitor.
CREATE TABLE IF NOT EXISTS graded_evaluations (
    id           BIGSERIAL PRIMARY KEY,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    team_id      UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    eval_id      VARCHAR(64) NOT NULL,
    seq          INTEGER NOT NULL,                  -- the team's k-th evaluation since opening
    charged      NUMERIC(12,3) NOT NULL DEFAULT 0,  -- credits taken at admission
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'ok', 'infra_error', 'expired')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at    TIMESTAMPTZ,
    CONSTRAINT graded_evaluations_eval_id UNIQUE (challenge_id, eval_id)
);
CREATE INDEX IF NOT EXISTS idx_graded_evaluations_team ON graded_evaluations(team_id, challenge_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_graded_evaluations_pending ON graded_evaluations(created_at) WHERE status = 'pending';

-- every accepted report (the admin log). one report closes one evaluation.
CREATE TABLE IF NOT EXISTS graded_reports (
    id              BIGSERIAL PRIMARY KEY,
    challenge_id    UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    team_id         UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    eval_id         VARCHAR(64) NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('ok', 'infra_error')),
    score           DOUBLE PRECISION,  -- null for infra_error
    idempotency_key VARCHAR(64) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT graded_reports_idempotency_key UNIQUE (challenge_id, idempotency_key),
    CONSTRAINT graded_reports_eval_id UNIQUE (challenge_id, eval_id)
);
CREATE INDEX IF NOT EXISTS idx_graded_reports_challenge ON graded_reports(challenge_id, id DESC);

-- single-use request nonces, shared by evaluate + report so a signed body can't
-- be replayed against the other endpoint. pruned once past the signature window.
CREATE TABLE IF NOT EXISTS graded_nonces (
    nonce      VARCHAR(128) PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_graded_nonces_age ON graded_nonces(created_at);
