-- 033_koth_reset_secret.sql
-- Per-instance shared secret for engine-driven KotH resets. The shared-target
-- launcher generates this at spawn, injects it into the target pod's env as
-- KOTH_ADMIN_TOKEN, and stores it here; the engine's httpProbe presents it as a
-- Bearer token to POST /koth/reset so only the engine can clear the holder at a
-- round boundary (GridWatch's self-heal timer is off by default). NULL for legacy
-- exec-checker hills, which don't use it.
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS reset_secret TEXT;
