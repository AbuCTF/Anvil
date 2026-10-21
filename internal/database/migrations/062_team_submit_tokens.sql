-- checker task detail columns for the admin log view, and the team alerts table
-- for the e8 bucketed exploitation alert system.
-- note: game_teams.token VARCHAR(64) already exists from migration 010.

-- code: structured ENO result code (e.g. INTERNAL_ERROR, TIMEOUT) distinct from
-- the freetext message; private_log: full ops-level stderr/trace, never served to teams.
ALTER TABLE game_checker_tasks
    ADD COLUMN IF NOT EXISTS code        VARCHAR(40),
    ADD COLUMN IF NOT EXISTS private_log TEXT;

-- one row per alert published to a team. the alert generator (e8) inserts here at
-- the end of tick t+1 for events that occurred in tick t.
CREATE TABLE IF NOT EXISTS game_team_alerts (
    id          BIGSERIAL   PRIMARY KEY,
    team_id     UUID        NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    tick_number INTEGER     NOT NULL,
    kind        VARCHAR(20) NOT NULL,   -- 'exploit', 'holding', 'shield_up'
    service_id  UUID        REFERENCES game_services(id) ON DELETE SET NULL,
    bucket      INTEGER,                -- exploit count bucket (1, 3, 7, 8+)
    detail      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (team_id, tick_number, kind, service_id)
);

CREATE INDEX IF NOT EXISTS idx_game_team_alerts_team_tick ON game_team_alerts(team_id, tick_number);
CREATE INDEX IF NOT EXISTS idx_game_team_alerts_created ON game_team_alerts(created_at);
