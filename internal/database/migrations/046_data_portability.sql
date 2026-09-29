CREATE TABLE IF NOT EXISTS data_import_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    created_by UUID NOT NULL REFERENCES users(id),
    entity VARCHAR(40) NOT NULL,
    source_format VARCHAR(10) NOT NULL,
    import_mode VARCHAR(10) NOT NULL,
    source_name VARCHAR(255) NOT NULL,
    checksum CHAR(64) NOT NULL,
    row_count INTEGER NOT NULL,
    plan JSONB NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    result JSONB,
    error TEXT,
    applied_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT data_import_jobs_entity CHECK (entity IN ('categories', 'challenges', 'users', 'teams', 'team_members')),
    CONSTRAINT data_import_jobs_format CHECK (source_format IN ('csv', 'json')),
    CONSTRAINT data_import_jobs_mode CHECK (import_mode IN ('create', 'merge')),
    CONSTRAINT data_import_jobs_status CHECK (status IN ('pending', 'applied', 'failed', 'expired')),
    CONSTRAINT data_import_jobs_row_count CHECK (row_count >= 0 AND row_count <= 5000)
);

CREATE INDEX IF NOT EXISTS idx_data_import_jobs_created_by ON data_import_jobs(created_by, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_data_import_jobs_pending ON data_import_jobs(expires_at) WHERE status = 'pending';
