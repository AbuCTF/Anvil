-- team Operations page (/team/finals) stays hidden until an organizer flips this.
INSERT INTO platform_settings (key, value, description, category)
VALUES ('finals.operations_enabled', 'false', 'Show the team Operations page during finals', 'general')
ON CONFLICT (key) DO NOTHING;
