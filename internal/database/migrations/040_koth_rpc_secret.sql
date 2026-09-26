-- per-entry private secret handed to the buying team at koth-enter. the arena's
-- verify endpoint checks (challenge_id, token, rpc_secret) together, so a harvested
-- public token (visible in /koth/status) can no longer write or grief the arena.
-- nullable: any pre-existing QA entries predate this and are wiped before the drop.
ALTER TABLE koth_entries ADD COLUMN IF NOT EXISTS rpc_secret TEXT;
