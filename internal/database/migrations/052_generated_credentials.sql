ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT FALSE;

INSERT INTO mail_templates (slug, name, description, subject, body_html, body_text, variables)
VALUES (
    'account_credentials',
    'Account credentials',
    'Sent when an organizer provisions a participant with a temporary password.',
    'Your {{event_name}} sign-in details',
    '<p>Hello {{participant_name}},</p><p>Your {{event_name}} account is ready.</p><p>Username: <strong>{{username}}</strong><br>Temporary password: <strong>{{temporary_password}}</strong></p><p><a href="{{login_url}}">Sign in</a> and choose a new password before continuing.</p>',
    E'Hello {{participant_name}},\n\nYour {{event_name}} account is ready.\nSign in: {{login_url}}\nUsername: {{username}}\nTemporary password: {{temporary_password}}\n\nYou must choose a new password before continuing.',
    ARRAY['participant_name', 'event_name', 'login_url', 'username', 'temporary_password']
)
ON CONFLICT (slug) DO NOTHING;
