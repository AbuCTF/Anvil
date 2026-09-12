-- Per-player history reads filter by user before ordering score events.
CREATE INDEX IF NOT EXISTS idx_hint_unlocks_user_unlocked_at
    ON hint_unlocks(user_id, unlocked_at)
    WHERE user_id IS NOT NULL;
