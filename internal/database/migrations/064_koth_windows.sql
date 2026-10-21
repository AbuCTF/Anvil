-- finals hill schedule: the engine opens and closes each hill on these windows at
-- tick start, so pausing and extending the game shifts them with everything else.
-- hills without windows keep their manual enabled flag.
CREATE TABLE IF NOT EXISTS game_koth_windows (
    id BIGSERIAL PRIMARY KEY,
    hill_id UUID NOT NULL REFERENCES game_koth_hills(id) ON DELETE CASCADE,
    opens_at TIMESTAMPTZ NOT NULL,
    closes_at TIMESTAMPTZ NOT NULL,
    CHECK (closes_at > opens_at)
);
CREATE INDEX IF NOT EXISTS idx_game_koth_windows_hill ON game_koth_windows(hill_id);
