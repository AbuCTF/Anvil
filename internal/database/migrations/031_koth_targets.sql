-- 031_koth_targets.sql
-- Shared-arena KotH targets: mark challenges that run ONE contested instance the
-- whole field attacks (vs one per team), and link a game_koth_hills row to that
-- challenge + the instancer-launched shared instance backing it. Foundation for the
-- buy-in gate and the shared-target launch.

-- Instancing mode: 'per_team' (default, one instance per team) or 'shared' (one
-- contested KotH target). A 'shared' challenge is the KotH-arena marker used by the
-- buy-in gate and the shared launcher.
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS instancing TEXT NOT NULL DEFAULT 'per_team'
    CHECK (instancing IN ('per_team', 'shared'));

-- Back a hill by a real challenge + the shared instance the instancer launched, so
-- the engine's host/port come from a running pod instead of a hand-typed IP.
-- generation is bumped on every reset so pre-reset holds never leak into post-reset
-- ticks. cpu_millis/mem_mib size the contested pod generously so all-teams-at-once
-- attack load can't CFS-throttle it into a false-DOWN (the "dirty concurrency" fix).
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS challenge_id UUID REFERENCES challenges(id) ON DELETE CASCADE;
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS instance_id  UUID;
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS generation   INT NOT NULL DEFAULT 0;
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS cpu_millis   INT NOT NULL DEFAULT 2000;
ALTER TABLE game_koth_hills ADD COLUMN IF NOT EXISTS mem_mib      INT NOT NULL DEFAULT 1024;
CREATE INDEX IF NOT EXISTS idx_game_koth_hills_challenge ON game_koth_hills(challenge_id);

-- One-time KotH buy-in cost (credits) a team pays to enter/enable its arena scoring
-- — the shared-arena analog of a launch cost, p2c-fundable. 500 = CTF26-1 12h re-sim
-- value (1.5-2.5 hard-launches; affordable from saved grant, a real allocation
-- choice, not a mid-tier lockout). Runtime-tunable.
INSERT INTO platform_settings (key, value, description, category) VALUES
    ('koth_buyin_cost', '500', 'Credits a team pays once to enter the KotH arena', 'general')
ON CONFLICT (key) DO NOTHING;
