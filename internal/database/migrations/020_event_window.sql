-- 020_event_window.sql
-- Public event timing used by the navigation countdown.

INSERT INTO platform_settings (key, value, description, category) VALUES
    ('event.start_at', '""', 'CTF start time as an RFC3339 timestamp', 'general'),
    ('event.end_at', '""', 'CTF end time as an RFC3339 timestamp', 'general')
ON CONFLICT (key) DO NOTHING;
