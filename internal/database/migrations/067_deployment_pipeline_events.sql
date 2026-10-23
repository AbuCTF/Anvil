-- Redacted, team-scoped deployment lifecycle events. The organizer controller
-- supplies only the normalized state and an allow-listed failure code; build
-- logs, source revisions, image digests and runner output never enter this table.
CREATE TABLE IF NOT EXISTS game_deployment_events (
    id BIGSERIAL PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    source_sequence BIGINT NOT NULL CHECK (source_sequence > 0),
    state VARCHAR(32) NOT NULL CHECK (state IN (
        'queued', 'building', 'gating', 'waiting_quiet_window',
        'promoted', 'rolled_back', 'failed'
    )),
    reason_code VARCHAR(32) CHECK (reason_code IS NULL OR reason_code IN (
        'policy', 'build', 'gate', 'promotion', 'health',
        'rollback', 'operator', 'unknown'
    )),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (team_id, source_sequence),
    CHECK ((state = 'failed') = (reason_code IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_game_deployment_events_team_service
    ON game_deployment_events(team_id, service_id, recorded_at DESC, id DESC);
