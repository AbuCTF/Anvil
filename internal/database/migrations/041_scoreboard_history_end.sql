-- Optional presentation cutoff for imported/historical scoreboards. When empty,
-- the public score chart follows event.end_at. Demo or migrated events can keep a
-- separate live interaction window without stretching historical score lines.
INSERT INTO platform_settings (key, value, description, category)
VALUES (
    'scoreboard.history_end_at',
    '""'::jsonb,
    'Optional RFC3339 cutoff for the public score-over-time chart; empty follows event.end_at',
    'general'
)
ON CONFLICT (key) DO NOTHING;
