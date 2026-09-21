-- arena_enabled: whether this event runs the Arena (Attack/Defense, King-of-the-Hill)
-- alongside Jeopardy. Default off => Jeopardy-only quals; the Arena nav tab and its
-- routes are gated on it, same pattern as teams_mode / economy_mode.
INSERT INTO platform_settings (key, value, description, category) VALUES
    ('arena_enabled', 'false', 'Show the Arena (Attack/Defense, King-of-the-Hill) section', 'general')
ON CONFLICT (key) DO NOTHING;
