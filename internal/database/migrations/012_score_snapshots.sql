-- 012_score_snapshots.sql
-- Per-tick standings snapshot: powers the score-over-time chart, sparklines, rank deltas.

CREATE TABLE game_score_snapshots (
    tick_number INTEGER NOT NULL REFERENCES game_ticks(tick_number) ON DELETE CASCADE,
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    total NUMERIC(12, 3) NOT NULL DEFAULT 0,
    rank INTEGER,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (tick_number, team_id)
);

CREATE INDEX idx_game_snapshots_team ON game_score_snapshots(team_id, tick_number);
