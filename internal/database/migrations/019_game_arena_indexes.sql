-- Keep the live arena's latest-service lookup bounded as SLA history grows.
CREATE INDEX IF NOT EXISTS idx_game_sla_latest
    ON game_sla_checks(team_id, service_id, tick_number DESC)
    INCLUDE (status, latency_ms);

CREATE INDEX IF NOT EXISTS idx_game_captures_recent
    ON game_captures(submitted_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_game_koth_control_latest
    ON game_koth_control(hill_id, tick_number DESC)
    INCLUDE (controller_team_id);
