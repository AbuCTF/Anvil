INSERT INTO platform_settings (key, value, description, category) VALUES
    ('event.slug', '"anvil-event"', 'Stable event identifier', 'event'),
    ('event.timezone', '"UTC"', 'Organizer timezone', 'event'),
    ('event.contact_email', '""', 'Public organizer contact', 'event'),
    ('event.rules_url', '""', 'Public rules URL', 'event'),
    ('event.privacy_url', '""', 'Public privacy URL', 'event'),
    ('event.terms_url', '""', 'Public terms URL', 'event'),
    ('event.profile_managed', 'false', 'Whether database event identity overrides deployment defaults', 'event'),
    ('event.setup_completed', 'false', 'Whether the onboarding checklist was completed', 'event'),
    ('branding.logo_key', '""', 'Storage key for the event logo', 'branding'),
    ('branding.logo_mime', '""', 'MIME type for the event logo', 'branding')
ON CONFLICT (key) DO NOTHING;
