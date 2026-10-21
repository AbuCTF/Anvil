-- finals scoring (h7 mirror) and the defend loop: per-tick readability of every
-- live flag, outbreak wave reads, deploy events, organizer re-credits and a
-- per-store score breakdown. additive only; nothing reads these outside finals mode.

-- one row per (flag, tick it was read back by a getflag)
CREATE TABLE IF NOT EXISTS game_flag_checks (
    flag_id UUID NOT NULL REFERENCES game_flags(id) ON DELETE CASCADE,
    tick_number INTEGER NOT NULL,
    readable BOOLEAN NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (flag_id, tick_number)
);

-- outbreak wave: the house bot running a store's easiest reference exploit
CREATE TABLE IF NOT EXISTS game_bot_reads (
    flag_id UUID NOT NULL REFERENCES game_flags(id) ON DELETE CASCADE,
    tick_number INTEGER NOT NULL,
    outcome VARCHAR(16) NOT NULL CHECK (outcome IN ('stolen', 'blocked', 'crashed', 'error')),
    latency_ms INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (flag_id, tick_number)
);

-- public game events: advisories, first bloods, shield up, crown changes
CREATE TABLE IF NOT EXISTS game_events (
    id BIGSERIAL PRIMARY KEY,
    kind VARCHAR(32) NOT NULL,
    tick_number INTEGER,
    team_id UUID REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID REFERENCES game_services(id) ON DELETE CASCADE,
    store_index INTEGER,
    hill_id UUID REFERENCES game_koth_hills(id) ON DELETE CASCADE,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_game_events_tick ON game_events(tick_number);

-- deploys from the model c pipeline; drive deploy pricing and digest stamping
CREATE TABLE IF NOT EXISTS game_deploys (
    id BIGSERIAL PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('deploy', 'rollback', 'revert', 'reset', 'fallback_a')),
    org_initiated BOOLEAN NOT NULL DEFAULT FALSE,
    image_digest TEXT,
    tick_number INTEGER,
    diff_files INTEGER,
    diff_lines INTEGER,
    diff_bytes INTEGER,
    deployed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_game_deploys_team_service ON game_deploys(team_id, service_id, deployed_at);

-- organizer re-credit: these ticks count as ORG_FAULT (green) for these teams
CREATE TABLE IF NOT EXISTS game_sla_credits (
    id BIGSERIAL PRIMARY KEY,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    team_id UUID REFERENCES game_teams(id) ON DELETE CASCADE, -- NULL = every team
    tick_from INTEGER NOT NULL,
    tick_to INTEGER NOT NULL,
    reason TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (tick_to >= tick_from)
);

-- per team x service x store points, rebuilt with the standings every tick
CREATE TABLE IF NOT EXISTS game_score_breakdown (
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    store_index INTEGER NOT NULL,
    steal NUMERIC(14, 3) NOT NULL DEFAULT 0,
    hold NUMERIC(14, 3) NOT NULL DEFAULT 0,
    up NUMERIC(14, 3) NOT NULL DEFAULT 0,
    flags_held INTEGER NOT NULL DEFAULT 0,
    flags_settled INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (team_id, service_id, store_index)
);

ALTER TABLE game_standings ADD COLUMN IF NOT EXISTS ad NUMERIC(14, 3) NOT NULL DEFAULT 0;

ALTER TABLE game_sla_checks
    ADD COLUMN IF NOT EXISTS step VARCHAR(16),
    ADD COLUMN IF NOT EXISTS code VARCHAR(48),
    ADD COLUMN IF NOT EXISTS image_digest TEXT;

ALTER TABLE game_services
    ADD COLUMN IF NOT EXISTS advisories JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS advisory_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS wave_exploits JSONB NOT NULL DEFAULT '{}'::jsonb;
