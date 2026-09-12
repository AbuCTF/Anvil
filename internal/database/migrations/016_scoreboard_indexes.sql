-- Keep public rankings and score histories index-backed as solve volume grows.
CREATE INDEX IF NOT EXISTS idx_solves_user_solved_at
    ON solves(user_id, solved_at);

CREATE INDEX IF NOT EXISTS idx_users_active_scoreboard
    ON users(total_score DESC, created_at, id)
    WHERE role != 'admin' AND status = 'active';
