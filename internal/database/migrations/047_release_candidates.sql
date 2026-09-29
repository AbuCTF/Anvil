CREATE TABLE IF NOT EXISTS release_candidates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sequence BIGSERIAL UNIQUE NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    event_checksum CHAR(64) NOT NULL,
    content_checksum CHAR(64) NOT NULL,
    economy_checksum CHAR(64) NOT NULL,
    report JSONB NOT NULL,
    waived_warnings JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_release_candidates_created_at ON release_candidates(created_at DESC);

CREATE OR REPLACE FUNCTION reject_release_candidate_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'release candidates are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS release_candidates_immutable ON release_candidates;
CREATE TRIGGER release_candidates_immutable
BEFORE UPDATE OR DELETE ON release_candidates
FOR EACH ROW EXECUTE FUNCTION reject_release_candidate_mutation();
