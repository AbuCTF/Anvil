-- Unified in-app communication stream. Organizer announcements and immutable
-- platform events share one authorization path and one receipt model.
CREATE TABLE IF NOT EXISTS notification_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    kind VARCHAR(20) NOT NULL CHECK (kind IN ('announcement', 'event')),
    event_type VARCHAR(80) NOT NULL,
    title VARCHAR(160) NOT NULL,
    body TEXT NOT NULL,
    severity VARCHAR(20) NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info', 'success', 'warning', 'critical')),
    audience VARCHAR(20) NOT NULL DEFAULT 'all'
        CHECK (audience IN ('all', 'participants', 'staff', 'team', 'user')),
    team_id UUID REFERENCES teams(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    href VARCHAR(1000),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    dedup_key VARCHAR(255),
    publish_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    cancelled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at IS NULL OR expires_at > publish_at),
    CHECK (
        (audience = 'team' AND team_id IS NOT NULL AND user_id IS NULL) OR
        (audience = 'user' AND user_id IS NOT NULL AND team_id IS NULL) OR
        (audience IN ('all', 'participants', 'staff') AND team_id IS NULL AND user_id IS NULL)
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_items_dedup
    ON notification_items(dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notification_items_active
    ON notification_items(publish_at DESC)
    WHERE cancelled_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_notification_items_team
    ON notification_items(team_id, publish_at DESC) WHERE team_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notification_items_user
    ON notification_items(user_id, publish_at DESC) WHERE user_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS notification_receipts (
    item_id UUID NOT NULL REFERENCES notification_items(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at TIMESTAMPTZ,
    dismissed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (item_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_notification_receipts_user
    ON notification_receipts(user_id, read_at);

COMMENT ON TABLE notification_items IS
    'Authoritative in-app stream and transactional outbox for announcements and platform events';
COMMENT ON COLUMN notification_items.dedup_key IS
    'Stable producer identity; retries with the same key cannot duplicate a platform event';
