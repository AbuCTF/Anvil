-- 025_economy_conversion.sql
-- Points->credits conversion tracking: the diminishing block rate depends on how
-- many blocks a team has already converted, so track the cumulative count. Also a
-- scoreboard-freeze flag lives in platform_settings (blind board near the end).
-- Additive; economy-gated.

ALTER TABLE economy_team_score ADD COLUMN IF NOT EXISTS p2c_blocks INTEGER NOT NULL DEFAULT 0;

INSERT INTO platform_settings (key, value, description, category) VALUES
    ('scoreboard_frozen', 'false', 'When true the public scoreboard is frozen (blind) near the event end', 'general')
ON CONFLICT (key) DO NOTHING;
