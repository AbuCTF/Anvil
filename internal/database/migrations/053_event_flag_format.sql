INSERT INTO platform_settings (key, value, description, category)
VALUES ('event.flag_format', '"flag{...}"', 'Participant-facing flag format example', 'event')
ON CONFLICT (key) DO NOTHING;
