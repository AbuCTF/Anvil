-- a team's share of a challenge's value: flags held (weighted by points) or a
-- graded best, 0..1. existing holders were full solves, hence the 1.0 default.
ALTER TABLE economy_challenge_state ADD COLUMN IF NOT EXISTS frac DOUBLE PRECISION NOT NULL DEFAULT 1.0;
