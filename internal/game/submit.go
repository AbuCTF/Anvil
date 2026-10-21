package game

import (
	"context"
	"errors"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SubmitOutcome string

const (
	SubmitAccepted  SubmitOutcome = "accepted"
	SubmitInvalid   SubmitOutcome = "invalid"
	SubmitOwnFlag   SubmitOutcome = "own"
	SubmitExpired   SubmitOutcome = "expired"
	SubmitDuplicate SubmitOutcome = "duplicate"
	// the nop team is a test target: its flags prove an exploit works and score nothing
	SubmitNop SubmitOutcome = "nop"
)

// TeamForUser returns the game team a user belongs to, if any. A game team shares its
// id with the platform team it mirrors, so users.team_id is the source of truth; the
// game_team_members rows are the older hand-wired path and only a fallback.
func TeamForUser(ctx context.Context, db *database.DB, userID uuid.UUID) (uuid.UUID, bool, error) {
	var teamID uuid.UUID
	err := db.Pool.QueryRow(ctx,
		`SELECT id FROM (
		   SELECT t.id, 0 AS pref, NULL::timestamptz AS joined FROM game_teams t
		   WHERE t.status = 'active' AND t.is_nop = FALSE
		     AND t.id = (SELECT team_id FROM users WHERE id = $1)
		   UNION ALL
		   SELECT tm.team_id, 1, tm.created_at FROM game_team_members tm
		   JOIN game_teams t ON t.id = tm.team_id
		   WHERE tm.user_id = $1 AND t.status = 'active' AND t.is_nop = FALSE
		 ) c ORDER BY pref, joined, id LIMIT 1`, userID).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, false, nil
	}
	if err != nil {
		return uuid.UUID{}, false, err
	}
	return teamID, true, nil
}

// SubmitFlag validates a submitted flag for the attacking team and records a
// capture. It rejects an unknown flag, the team's own flag, one past its
// window, and one already submitted. Score updates on the next recompute.
func SubmitFlag(ctx context.Context, db *database.DB, attacker uuid.UUID, flag string) (SubmitOutcome, error) {
	var (
		flagID     uuid.UUID
		owner      uuid.UUID
		service    uuid.UUID
		validUntil int
		ownerNop   bool
	)
	err := db.Pool.QueryRow(ctx,
		`SELECT f.id, f.team_id, f.service_id, f.valid_until_tick, t.is_nop
		 FROM game_flags f JOIN game_teams t ON t.id = f.team_id
		 WHERE f.flag = $1 AND f.planted_at IS NOT NULL`, flag).
		Scan(&flagID, &owner, &service, &validUntil, &ownerNop)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmitInvalid, nil
	}
	if err != nil {
		return "", err
	}
	if owner == attacker {
		return SubmitOwnFlag, nil
	}
	if ownerNop {
		return SubmitNop, nil
	}

	var currentTick int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&currentTick); err != nil {
		return "", err
	}
	if currentTick > validUntil {
		return SubmitExpired, nil
	}

	tag, err := db.Pool.Exec(ctx,
		`INSERT INTO game_captures (tick_number, attacker_team_id, victim_team_id, service_id, flag_id)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (attacker_team_id, flag_id) DO NOTHING`,
		currentTick, attacker, owner, service, flagID)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return SubmitDuplicate, nil
	}
	return SubmitAccepted, nil
}
