ALTER TABLE flag_attempts
    ADD COLUMN IF NOT EXISTS user_agent TEXT,
    ADD COLUMN IF NOT EXISTS request_id VARCHAR(128);

ALTER TABLE flag_share_events
    ADD COLUMN IF NOT EXISTS flag_fingerprint VARCHAR(64),
    ADD COLUMN IF NOT EXISTS flag_length INTEGER,
    ADD COLUMN IF NOT EXISTS challenge_name VARCHAR(200),
    ADD COLUMN IF NOT EXISTS challenge_slug VARCHAR(200),
    ADD COLUMN IF NOT EXISTS flag_name VARCHAR(100),
    ADD COLUMN IF NOT EXISTS owner_username VARCHAR(50),
    ADD COLUMN IF NOT EXISTS owner_email VARCHAR(255),
    ADD COLUMN IF NOT EXISTS submitter_username VARCHAR(50),
    ADD COLUMN IF NOT EXISTS submitter_email VARCHAR(255),
    ADD COLUMN IF NOT EXISTS owner_team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS submitter_team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS owner_team_name VARCHAR(100),
    ADD COLUMN IF NOT EXISTS submitter_team_name VARCHAR(100),
    ADD COLUMN IF NOT EXISTS owner_ip VARCHAR(45),
    ADD COLUMN IF NOT EXISTS owner_user_agent TEXT,
    ADD COLUMN IF NOT EXISTS submitter_user_agent TEXT,
    ADD COLUMN IF NOT EXISTS request_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS country_code VARCHAR(2),
    ADD COLUMN IF NOT EXISTS region VARCHAR(120),
    ADD COLUMN IF NOT EXISTS city VARCHAR(120),
    ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS evidence_source VARCHAR(24) NOT NULL DEFAULT 'live',
    ADD COLUMN IF NOT EXISTS review_status VARCHAR(24) NOT NULL DEFAULT 'open',
    ADD COLUMN IF NOT EXISTS review_note TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;

UPDATE flag_share_events fse
SET flag_fingerprint = CASE
        WHEN fse.flag_value LIKE 'sha256:%' THEN SUBSTRING(fse.flag_value FROM 8)
        ELSE encode(digest(fse.flag_value, 'sha256'), 'hex')
    END,
    flag_length = CASE
        WHEN fse.flag_value LIKE 'sha256:%' OR fse.flag_value = '[redacted]' THEN NULL
        ELSE length(fse.flag_value)
    END
WHERE fse.flag_fingerprint IS NULL;

UPDATE flag_share_events fse
SET challenge_name = challenge.name,
    challenge_slug = challenge.slug,
    flag_name = flag.name,
    owner_username = owner.username,
    owner_email = owner.email,
    submitter_username = submitter.username,
    submitter_email = submitter.email,
    owner_team_id = owner.team_id,
    submitter_team_id = submitter.team_id,
    owner_team_name = (SELECT name FROM teams WHERE id = owner.team_id),
    submitter_team_name = (SELECT name FROM teams WHERE id = submitter.team_id)
FROM challenges challenge, flags flag, users owner, users submitter
WHERE challenge.id = fse.challenge_id
  AND flag.id = fse.flag_id
  AND owner.id = fse.owner_user_id
  AND submitter.id = fse.submitter_user_id
  AND (fse.owner_team_id IS NULL OR fse.submitter_team_id IS NULL);

UPDATE flag_share_events
SET evidence_source = 'redacted'
WHERE submitter_ip IS NULL OR flag_value = '[redacted]';

ALTER TABLE flag_share_events
    DROP CONSTRAINT IF EXISTS flag_share_events_challenge_id_fkey,
    DROP CONSTRAINT IF EXISTS flag_share_events_flag_id_fkey,
    DROP CONSTRAINT IF EXISTS flag_share_events_owner_user_id_fkey,
    DROP CONSTRAINT IF EXISTS flag_share_events_submitter_user_id_fkey,
    ALTER COLUMN challenge_id DROP NOT NULL,
    ALTER COLUMN flag_id DROP NOT NULL,
    ALTER COLUMN owner_user_id DROP NOT NULL,
    ALTER COLUMN submitter_user_id DROP NOT NULL,
    ADD CONSTRAINT flag_share_events_challenge_id_fkey
        FOREIGN KEY (challenge_id) REFERENCES challenges(id) ON DELETE SET NULL,
    ADD CONSTRAINT flag_share_events_flag_id_fkey
        FOREIGN KEY (flag_id) REFERENCES flags(id) ON DELETE SET NULL,
    ADD CONSTRAINT flag_share_events_owner_user_id_fkey
        FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE SET NULL,
    ADD CONSTRAINT flag_share_events_submitter_user_id_fkey
        FOREIGN KEY (submitter_user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE flag_share_events
    DROP CONSTRAINT IF EXISTS flag_share_events_evidence_source_check,
    ADD CONSTRAINT flag_share_events_evidence_source_check
        CHECK (evidence_source IN ('live', 'redacted', 'demo_simulated')),
    DROP CONSTRAINT IF EXISTS flag_share_events_review_status_check,
    ADD CONSTRAINT flag_share_events_review_status_check
        CHECK (review_status IN ('open', 'reviewing', 'confirmed', 'dismissed')),
    DROP CONSTRAINT IF EXISTS flag_share_events_latitude_check,
    ADD CONSTRAINT flag_share_events_latitude_check
        CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    DROP CONSTRAINT IF EXISTS flag_share_events_longitude_check,
    ADD CONSTRAINT flag_share_events_longitude_check
        CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),
    DROP CONSTRAINT IF EXISTS flag_share_events_flag_length_check,
    ADD CONSTRAINT flag_share_events_flag_length_check
        CHECK (flag_length IS NULL OR flag_length >= 0);

CREATE INDEX IF NOT EXISTS idx_flag_attempts_ip_created
    ON flag_attempts(ip_address, created_at DESC)
    WHERE ip_address IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_flag_share_review_created
    ON flag_share_events(review_status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_flag_share_submitter_ip_created
    ON flag_share_events(submitter_ip, created_at DESC)
    WHERE submitter_ip IS NOT NULL;
