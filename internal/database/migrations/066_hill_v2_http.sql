ALTER TABLE game_koth_hills
    ADD COLUMN IF NOT EXISTS controller_protocol TEXT NOT NULL DEFAULT 'koth-v1-http'
    CHECK (controller_protocol IN ('koth-v1-http', 'hill-v2-http'));

ALTER TABLE koth_entries ALTER COLUMN token DROP NOT NULL;
ALTER TABLE koth_entries ADD COLUMN IF NOT EXISTS holder_slug VARCHAR(32);
ALTER TABLE koth_entries ADD COLUMN IF NOT EXISTS bearer_token TEXT;
ALTER TABLE koth_entries ADD CONSTRAINT koth_entries_hill_v2_credentials
    CHECK ((holder_slug IS NULL) = (bearer_token IS NULL));
ALTER TABLE koth_entries ADD CONSTRAINT koth_entries_holder_slug_format
    CHECK (holder_slug IS NULL OR holder_slug ~ '^[a-z0-9][a-z0-9-]{0,31}$');

CREATE UNIQUE INDEX IF NOT EXISTS idx_koth_entries_holder_slug
    ON koth_entries(challenge_id, holder_slug) WHERE holder_slug IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_koth_entries_bearer_token
    ON koth_entries(bearer_token) WHERE bearer_token IS NOT NULL;
