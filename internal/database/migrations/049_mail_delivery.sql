CREATE TABLE IF NOT EXISTS mail_providers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    host VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL,
    username VARCHAR(255),
    password_ciphertext BYTEA NOT NULL,
    security VARCHAR(20) NOT NULL,
    from_name VARCHAR(100) NOT NULL,
    from_address VARCHAR(255) NOT NULL,
    reply_to VARCHAR(255),
    priority INTEGER NOT NULL DEFAULT 10,
    daily_limit INTEGER,
    hourly_limit INTEGER,
    minute_limit INTEGER,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_healthy BOOLEAN NOT NULL DEFAULT FALSE,
    failure_count INTEGER NOT NULL DEFAULT 0,
    circuit_open_until TIMESTAMPTZ,
    last_error_code VARCHAR(80),
    last_error_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT mail_providers_port CHECK (port BETWEEN 1 AND 65535),
    CONSTRAINT mail_providers_security CHECK (security IN ('starttls', 'tls')),
    CONSTRAINT mail_providers_priority CHECK (priority BETWEEN 0 AND 1000),
    CONSTRAINT mail_providers_daily_limit CHECK (daily_limit IS NULL OR daily_limit > 0),
    CONSTRAINT mail_providers_hourly_limit CHECK (hourly_limit IS NULL OR hourly_limit > 0),
    CONSTRAINT mail_providers_minute_limit CHECK (minute_limit IS NULL OR minute_limit > 0)
);

CREATE INDEX IF NOT EXISTS idx_mail_providers_active_priority ON mail_providers(priority, id) WHERE is_active;

CREATE TABLE IF NOT EXISTS mail_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    slug VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(160) NOT NULL,
    description TEXT,
    subject VARCHAR(500) NOT NULL,
    body_html TEXT NOT NULL,
    body_text TEXT,
    variables TEXT[] NOT NULL DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mail_deliveries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    recipient VARCHAR(255) NOT NULL,
    template_slug VARCHAR(64) REFERENCES mail_templates(slug) ON DELETE SET NULL,
    payload_ciphertext BYTEA,
    status VARCHAR(20) NOT NULL DEFAULT 'queued',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    provider_id UUID REFERENCES mail_providers(id) ON DELETE SET NULL,
    provider_name VARCHAR(100),
    error_code VARCHAR(80),
    message_id VARCHAR(500),
    not_before TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT mail_deliveries_status CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'cancelled')),
    CONSTRAINT mail_deliveries_attempts CHECK (attempts >= 0 AND attempts <= max_attempts),
    CONSTRAINT mail_deliveries_max_attempts CHECK (max_attempts BETWEEN 1 AND 10)
);

CREATE INDEX IF NOT EXISTS idx_mail_deliveries_queue ON mail_deliveries(not_before, created_at) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_mail_deliveries_recent ON mail_deliveries(created_at DESC);

INSERT INTO mail_templates (slug, name, description, subject, body_html, body_text, variables)
VALUES
    ('account_activation', 'Account activation', 'Sent when an organizer provisions a participant account.', 'Activate your {{event_name}} account', '<p>Hello {{participant_name}},</p><p>An account has been created for you on {{event_name}}.</p><p><a href="{{activation_url}}">Set your password</a></p><p>Your username is <strong>{{username}}</strong>. This link expires at {{expires_at}}.</p>', E'Hello {{participant_name}},\n\nSet your password for {{event_name}}: {{activation_url}}\nUsername: {{username}}\nThis link expires at {{expires_at}}.', ARRAY['participant_name', 'event_name', 'activation_url', 'username', 'expires_at']),
    ('password_reset', 'Password reset', 'Sent after a participant requests a password reset.', 'Reset your {{event_name}} password', '<p>Hello {{participant_name}},</p><p><a href="{{reset_url}}">Reset your password</a></p><p>This link expires at {{expires_at}}.</p>', E'Hello {{participant_name}},\n\nReset your password: {{reset_url}}\nThis link expires at {{expires_at}}.', ARRAY['participant_name', 'event_name', 'reset_url', 'expires_at']),
    ('welcome', 'Welcome', 'Sent after account activation.', 'Welcome to {{event_name}}', '<p>Hello {{participant_name}},</p><p>Your account for {{event_name}} is ready.</p><p>Sign in at <a href="{{login_url}}">{{login_url}}</a>.</p>', E'Hello {{participant_name}},\n\nYour {{event_name}} account is ready. Sign in: {{login_url}}', ARRAY['participant_name', 'event_name', 'login_url']),
    ('event_reminder', 'Event reminder', 'Sent before the event starts.', '{{event_name}} starts at {{event_start}}', '<p>Hello {{participant_name}},</p><p>{{event_name}} starts at {{event_start}}.</p><p><a href="{{login_url}}">Open the platform</a></p>', E'Hello {{participant_name}},\n\n{{event_name}} starts at {{event_start}}. Open the platform: {{login_url}}', ARRAY['participant_name', 'event_name', 'event_start', 'login_url']),
    ('event_update', 'Event update', 'Operational update sent by an organizer.', '{{event_name}} update: {{update_title}}', '<p>Hello {{participant_name}},</p><h2>{{update_title}}</h2><p>{{update_body}}</p><p>Support: {{support_email}}</p>', E'Hello {{participant_name}},\n\n{{update_title}}\n\n{{update_body}}\n\nSupport: {{support_email}}', ARRAY['participant_name', 'event_name', 'update_title', 'update_body', 'support_email'])
ON CONFLICT (slug) DO NOTHING;
