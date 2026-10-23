INSERT INTO platform_settings (key, value, description, category)
VALUES ('event.flag_format', '"H7CTF{...}"', 'Participant-facing flag format example', 'event')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
