INSERT INTO platform_settings (key, value, description, category)
VALUES (
    'scoreboard.score_source',
    '"auto"'::jsonb,
    'Score source used for public standings',
    'scoreboard'
)
ON CONFLICT (key) DO NOTHING;
