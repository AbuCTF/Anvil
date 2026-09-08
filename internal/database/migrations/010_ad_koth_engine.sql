-- 010_ad_koth_engine.sql
-- Attack-Defense + King-of-the-Hill engine: first-class teams, services, the
-- tick clock, flags, SLA, captures, KotH hills/rounds/control, and scoring.

CREATE TABLE game_teams (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) NOT NULL UNIQUE,

    subnet CIDR,
    vulnbox_ip INET,
    node_id UUID REFERENCES vm_nodes(id),

    token VARCHAR(64) UNIQUE,
    status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    is_nop BOOLEAN NOT NULL DEFAULT FALSE,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE game_team_members (
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL DEFAULT 'member'
        CHECK (role IN ('captain', 'member')),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE game_services (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) NOT NULL UNIQUE,
    category VARCHAR(20) NOT NULL
        CHECK (category IN ('pwn', 'web', 'crypto', 'misc', 'rev', 'forensics')),
    tier VARCHAR(20) NOT NULL DEFAULT 'core'
        CHECK (tier IN ('core', 'stretch')),

    port INTEGER,
    checker_ref VARCHAR(200),
    flag_stores INTEGER NOT NULL DEFAULT 1,

    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER DEFAULT 0,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE game_ticks (
    tick_number INTEGER PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'closed'))
);

CREATE TABLE game_flags (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tick_number INTEGER NOT NULL REFERENCES game_ticks(tick_number),
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    store_index INTEGER NOT NULL DEFAULT 0,

    flag VARCHAR(128) NOT NULL UNIQUE,
    valid_from_tick INTEGER NOT NULL,
    valid_until_tick INTEGER NOT NULL,
    planted_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE (tick_number, team_id, service_id, store_index)
);

CREATE INDEX idx_game_flags_lookup ON game_flags(team_id, service_id, tick_number);
CREATE INDEX idx_game_flags_window ON game_flags(valid_until_tick);

CREATE TABLE game_sla_checks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tick_number INTEGER NOT NULL REFERENCES game_ticks(tick_number),
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,

    status VARCHAR(20) NOT NULL
        CHECK (status IN ('OK', 'DOWN', 'FAULTY', 'FLAG_NOT_FOUND', 'RECOVERING')),
    latency_ms INTEGER,
    message TEXT,
    checked_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE (tick_number, team_id, service_id)
);

CREATE INDEX idx_game_sla_tick ON game_sla_checks(tick_number);

CREATE TABLE game_captures (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tick_number INTEGER NOT NULL REFERENCES game_ticks(tick_number),
    attacker_team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    victim_team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES game_services(id) ON DELETE CASCADE,
    flag_id UUID NOT NULL REFERENCES game_flags(id) ON DELETE CASCADE,
    submitted_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE (attacker_team_id, flag_id)
);

CREATE INDEX idx_game_captures_attacker ON game_captures(attacker_team_id, tick_number);
CREATE INDEX idx_game_captures_flag ON game_captures(flag_id);

CREATE TABLE game_koth_hills (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    slug VARCHAR(100) NOT NULL UNIQUE,
    category VARCHAR(20),

    port INTEGER,
    checker_ref VARCHAR(200),
    reset_seconds INTEGER NOT NULL DEFAULT 900,

    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE game_koth_rounds (
    round_number INTEGER PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ends_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'closed'))
);

CREATE TABLE game_koth_control (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tick_number INTEGER NOT NULL REFERENCES game_ticks(tick_number),
    round_number INTEGER REFERENCES game_koth_rounds(round_number),
    hill_id UUID NOT NULL REFERENCES game_koth_hills(id) ON DELETE CASCADE,
    controller_team_id UUID REFERENCES game_teams(id) ON DELETE SET NULL,
    checked_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE (tick_number, hill_id)
);

CREATE INDEX idx_game_koth_control_tick ON game_koth_control(tick_number);

CREATE TABLE game_score_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    team_id UUID NOT NULL REFERENCES game_teams(id) ON DELETE CASCADE,
    tick_number INTEGER REFERENCES game_ticks(tick_number),
    round_number INTEGER REFERENCES game_koth_rounds(round_number),

    stream VARCHAR(20) NOT NULL
        CHECK (stream IN ('ATTACK', 'DEFENSE', 'SLA', 'KOTH')),
    points NUMERIC(12, 3) NOT NULL,
    source VARCHAR(200),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_game_score_events_team ON game_score_events(team_id);
CREATE INDEX idx_game_score_events_tick ON game_score_events(tick_number);

CREATE TABLE game_standings (
    team_id UUID PRIMARY KEY REFERENCES game_teams(id) ON DELETE CASCADE,
    attack NUMERIC(12, 3) NOT NULL DEFAULT 0,
    defense NUMERIC(12, 3) NOT NULL DEFAULT 0,
    sla NUMERIC(12, 3) NOT NULL DEFAULT 0,
    koth NUMERIC(12, 3) NOT NULL DEFAULT 0,
    total NUMERIC(12, 3) NOT NULL DEFAULT 0,
    rank INTEGER,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
