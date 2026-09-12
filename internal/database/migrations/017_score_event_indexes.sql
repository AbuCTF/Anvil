-- Hint deductions participate in score histories and historical rank deltas.
CREATE INDEX IF NOT EXISTS idx_hint_unlocks_unlocked_user
    ON hint_unlocks(unlocked_at, user_id)
    WHERE user_id IS NOT NULL;
