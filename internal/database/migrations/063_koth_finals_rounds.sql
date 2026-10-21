-- finals king of the hill: rounds close on the clock (15 min by day, 30 overnight),
-- each hill ranks its own round, and a long reign carries a crown bounty. additive;
-- the quals path keeps its tick-derived rounds.

ALTER TABLE game_koth_rounds ADD COLUMN IF NOT EXISTS planned_end TIMESTAMPTZ;

-- one row per (round, hill, team that held it): the per-hill result and any bounty
CREATE TABLE IF NOT EXISTS game_koth_results (
    round_number INTEGER NOT NULL,
    hill_id UUID NOT NULL REFERENCES game_koth_hills(id) ON DELETE CASCADE,
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    held_ticks INTEGER NOT NULL,
    rank INTEGER NOT NULL,
    points NUMERIC(10, 3) NOT NULL DEFAULT 0,
    bounty NUMERIC(10, 3) NOT NULL DEFAULT 0,
    PRIMARY KEY (round_number, hill_id, team_id)
);
