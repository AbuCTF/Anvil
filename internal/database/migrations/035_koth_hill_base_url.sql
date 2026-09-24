-- 035_koth_hill_base_url.sql
-- A challenge-backed KotH hill is reached over HTTP at a base URL (the shared
-- instance's published endpoint), not the inet host+port the legacy exec-checker
-- hills use — the operator publishes an HTTPS FQDN via ingress, which an inet
-- column can't hold. The engine's httpProbe polls base_url + /koth/status and
-- drives base_url + /koth/reset. Set by the shared-target launcher at spawn.
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS base_url TEXT;
