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
)

// TeamForUser returns the game team a user belongs to, if any.
func TeamForUser(ctx context.Context, db *database.DB, userID uuid.UUID) (uuid.UUID, bool, error) {
	var teamID uuid.UUID
	err := db.Pool.QueryRow(ctx,
		`SELECT tm.team_id FROM game_team_members tm
		 JOIN game_teams t ON t.id = tm.team_id
		 WHERE tm.user_id = $1 AND t.status = 'active' AND t.is_nop = FALSE
		 ORDER BY tm.created_at, tm.team_id LIMIT 1`, userID).Scan(&teamID)
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
	)
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, service_id, valid_until_tick
		 FROM game_flags WHERE flag = $1 AND planted_at IS NOT NULL`, flag).
		Scan(&flagID, &owner, &service, &validUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmitInvalid, nil
	}
	if err != nil {
		return "", err
	}
	if owner == attacker {
		return SubmitOwnFlag, nil
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
