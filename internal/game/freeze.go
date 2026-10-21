package game

import (
	"context"
	"errors"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/jackc/pgx/v5"
)

// publicViewDelay is the tick lag applied to the public board so attackers
// cannot exploit live standings to time their submissions.
const publicViewDelay = 2

// PublicViewTick returns the tick number to use for public board queries:
// the current closed tick minus publicViewDelay. Returns (0, false, nil)
// in legacy mode or when there are not yet enough closed ticks.
func PublicViewTick(ctx context.Context, db *database.DB) (int, bool, error) {
	state, err := ReadState(ctx, db)
	if err != nil {
		return 0, false, err
	}
	if state == StateLegacy {
		return 0, false, nil
	}
	var current int
	err = db.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks WHERE status = 'closed'`,
	).Scan(&current)
	if err != nil {
		return 0, false, err
	}
	delayed := current - publicViewDelay
	if delayed < 0 {
		delayed = 0
	}
	return delayed, true, nil
}

// IsFrozen returns true when the current time has passed the
// platform_settings key 'event.freeze_at'. During the freeze window the
// public board stops updating. Returns false when the key is unset.
func IsFrozen(ctx context.Context, db *database.DB) (bool, error) {
	var raw string
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(value #>> '{}', '') FROM platform_settings WHERE key = 'event.freeze_at'`,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || raw == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if parseSettingTime(raw).IsZero() {
		return false, nil
	}
	// use postgres NOW() to avoid local-clock skew
	var frozen bool
	err = db.Pool.QueryRow(ctx, `SELECT NOW() >= $1::timestamptz`, raw).Scan(&frozen)
	return frozen, err
}
