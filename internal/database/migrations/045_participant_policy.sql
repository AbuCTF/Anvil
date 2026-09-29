INSERT INTO platform_settings (key, value, description, category) VALUES
    ('participants.team_creation', '"open"', 'Who may create participant teams', 'participants'),
    ('participants.team_join', '"code"', 'Whether participants may join teams by code', 'participants'),
    ('participants.default_team_size', '4', 'Default member limit for new teams', 'participants'),
    ('participants.max_teams', '0', 'Maximum participant teams, zero for unlimited', 'participants'),
    ('participants.allowed_email_domains', '""', 'Comma-separated email domain allowlist', 'participants'),
    ('notifications.sound_allowed', 'false', 'Whether browsers may opt into notification sounds', 'communications'),
    ('branding.accent', '"cyan"', 'Accessible event accent palette', 'branding')
ON CONFLICT (key) DO NOTHING;
