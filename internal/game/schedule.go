package game

import (
	"context"
	"fmt"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/jackc/pgx/v5"
)

// applyHillWindows opens and closes scheduled hills for this tick. A hill with any
// window is enabled exactly while one of its windows covers now; each flip is a
// public event for the war room.
func (c *Controller) applyHillWindows(ctx context.Context, tick int, now time.Time) error {
	rows, err := c.db.Pool.Query(ctx,
		`UPDATE game_koth_hills h SET enabled = w.open
		 FROM (
		   SELECT hill_id, bool_or(opens_at <= $1 AND $1 < closes_at) AS open
		   FROM game_koth_windows GROUP BY hill_id
		 ) w
		 WHERE h.id = w.hill_id AND h.enabled IS DISTINCT FROM w.open
		 RETURNING h.id, w.open`, now)
	if err != nil {
		return fmt.Errorf("apply hill windows: %w", err)
	}
	type flip struct {
		hill string
		open bool
	}
	var flips []flip
	for rows.Next() {
		var f flip
		if err := rows.Scan(&f.hill, &f.open); err != nil {
			rows.Close()
			return err
		}
		flips = append(flips, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range flips {
		kind := "hill_close"
		if f.open {
			kind = "hill_open"
		}
		if _, err := c.db.Pool.Exec(ctx,
			`INSERT INTO game_events (kind, tick_number, hill_id) VALUES ($1, $2, $3)`, kind, tick, f.hill); err != nil {
			return err
		}
	}
	return nil
}

// ExtendGame shifts every future scheduled moment by d in one transaction: the end
// and freeze of the event, service releases, outbreak fallback slots, hill windows
// and the running koth round. Used after a pause so nothing fires early.
func ExtendGame(ctx context.Context, db *database.DB, d time.Duration) error {
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now()
	secs := d.Seconds()

	for _, key := range []string{"event.end_at", "event.freeze_at"} {
		var raw string
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(value #>> '{}', '') FROM platform_settings WHERE key = $1 FOR UPDATE`, key).Scan(&raw)
		if err == pgx.ErrNoRows || raw == "" {
			continue
		}
		if err != nil {
			return err
		}
		at, perr := time.Parse(time.RFC3339, raw)
		if perr != nil || at.Before(now) {
			continue // unset, unparsable or already past: nothing to move
		}
		if _, err := tx.Exec(ctx,
			`UPDATE platform_settings SET value = to_jsonb($2::text), updated_at = NOW() WHERE key = $1`,
			key, at.Add(d).UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`UPDATE game_services SET release_at = release_at + make_interval(secs => $2) WHERE release_at > $1`,
		`UPDATE game_services SET advisory_at = advisory_at + make_interval(secs => $2) WHERE advisory_at > $1`,
		`UPDATE game_koth_windows SET opens_at = opens_at + make_interval(secs => $2) WHERE opens_at > $1`,
		`UPDATE game_koth_windows SET closes_at = closes_at + make_interval(secs => $2) WHERE closes_at > $1`,
		`UPDATE game_koth_rounds SET planned_end = planned_end + make_interval(secs => $2)
		 WHERE status = 'running' AND planned_end > $1::timestamptz - interval '1 day'`,
	} {
		if _, err := tx.Exec(ctx, q, now, secs); err != nil {
			return fmt.Errorf("extend: %w", err)
		}
	}
	return tx.Commit(ctx)
}
