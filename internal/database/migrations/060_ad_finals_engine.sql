-- finals attack-defense: enowars-style checker tasks, flag variants carrying attack
-- info, an organizer-fault sla status and scheduled service release. additive only:
-- none of this changes behavior until platform_settings game.state is set, so no
-- settings row is inserted here.

ALTER TABLE game_flags ADD COLUMN IF NOT EXISTS attack_info TEXT;

ALTER TABLE game_services
    ADD COLUMN IF NOT EXISTS noise_variants INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS havoc_variants INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS release_at TIMESTAMPTZ;

-- numeric team id for the checker protocol (teamId is an int there) and addressing
ALTER TABLE game_teams ADD COLUMN IF NOT EXISTS team_number INTEGER UNIQUE;

-- ERROR = organizer fault (checker INTERNAL_ERROR, our own outage): credited, never charged
ALTER TABLE game_sla_checks DROP CONSTRAINT IF EXISTS game_sla_checks_status_check;
ALTER TABLE game_sla_checks ADD CONSTRAINT game_sla_checks_status_check
    CHECK (status IN ('OK', 'DOWN', 'FAULTY', 'FLAG_NOT_FOUND', 'RECOVERING', 'ERROR'));

-- one row per checker task; the id doubles as the protocol's unique taskId
CREATE TABLE IF NOT EXISTS game_checker_tasks (
    id BIGSERIAL PRIMARY KEY,
    tick_number INTEGER NOT NULL,
    related_tick INTEGER NOT NULL,
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    method VARCHAR(16) NOT NULL,
    variant INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(20),
    message TEXT,
    latency_ms INTEGER,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_game_checker_tasks_team_tick ON game_checker_tasks(team_id, tick_number);
CREATE INDEX IF NOT EXISTS idx_game_checker_tasks_tick ON game_checker_tasks(tick_number);
