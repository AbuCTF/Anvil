CREATE TABLE IF NOT EXISTS competition_events (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id UUID NOT NULL DEFAULT uuid_generate_v4() UNIQUE,
    event_slug VARCHAR(120) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    payload_checksum CHAR(64) NOT NULL,
    kind VARCHAR(80) NOT NULL,
    stream VARCHAR(40) NOT NULL,
    subject_type VARCHAR(20) NOT NULL CHECK (subject_type IN ('user', 'team', 'system')),
    subject_id UUID,
    user_id UUID,
    team_id UUID,
    challenge_id UUID,
    instance_id UUID,
    delta NUMERIC(18,6) NOT NULL DEFAULT 0,
    value_after NUMERIC(18,6),
    policy_name VARCHAR(120) NOT NULL,
    policy_revision INTEGER NOT NULL CHECK (policy_revision > 0),
    policy_checksum VARCHAR(128) NOT NULL,
    source VARCHAR(120) NOT NULL,
    actor_id UUID,
    causation_id UUID,
    correlation_id UUID NOT NULL,
    request_id VARCHAR(255),
    occurred_at TIMESTAMPTZ NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (event_slug, idempotency_key),
    CHECK (subject_type = 'system' OR subject_id IS NOT NULL),
    CHECK (payload_checksum ~ '^[0-9a-f]{64}$'),
    CHECK (jsonb_typeof(metadata) = 'object')
);

INSERT INTO platform_settings (key, value, description, category)
VALUES ('competition.events_shadow_enabled', 'false', 'Mirror standard scoring writes into the competition event stream', 'scoring')
ON CONFLICT (key) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_competition_events_subject
    ON competition_events(event_slug, subject_type, subject_id, sequence);
CREATE INDEX IF NOT EXISTS idx_competition_events_challenge
    ON competition_events(event_slug, challenge_id, sequence) WHERE challenge_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_competition_events_correlation
    ON competition_events(correlation_id, sequence);
CREATE INDEX IF NOT EXISTS idx_competition_events_effective
    ON competition_events(event_slug, effective_at, sequence);

CREATE TABLE IF NOT EXISTS competition_projection_checkpoints (
    event_slug VARCHAR(120) NOT NULL,
    projection VARCHAR(80) NOT NULL,
    last_sequence BIGINT NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    state_checksum VARCHAR(128),
    rebuilt_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_slug, projection)
);

CREATE TABLE IF NOT EXISTS competition_score_projections (
    event_slug VARCHAR(120) NOT NULL,
    stream VARCHAR(40) NOT NULL,
    subject_type VARCHAR(20) NOT NULL CHECK (subject_type IN ('user', 'team')),
    subject_id UUID NOT NULL,
    score NUMERIC(18,6) NOT NULL DEFAULT 0,
    last_sequence BIGINT NOT NULL CHECK (last_sequence > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_slug, stream, subject_type, subject_id)
);

CREATE INDEX IF NOT EXISTS idx_competition_score_projections_rank
    ON competition_score_projections(event_slug, stream, score DESC, last_sequence);

CREATE OR REPLACE FUNCTION reject_competition_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'competition events are append-only';
END;
$$;

DROP TRIGGER IF EXISTS competition_events_append_only ON competition_events;
CREATE TRIGGER competition_events_append_only
BEFORE UPDATE OR DELETE ON competition_events
FOR EACH ROW EXECUTE FUNCTION reject_competition_event_mutation();
