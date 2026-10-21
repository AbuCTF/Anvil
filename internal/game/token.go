package game

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GenerateTeamToken returns a new 32-byte (64 hex char) team submit token.
func GenerateTeamToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// TeamForToken resolves an active non-NOP game team by its submit token.
// Returns teamID, isNOP, found, error. NOP teams are returned with isNOP=true
// so the caller can decide how to handle their submissions.
func TeamForToken(ctx context.Context, db *database.DB, token string) (uuid.UUID, bool, bool, error) {
	var teamID uuid.UUID
	var isNOP bool
	err := db.Pool.QueryRow(ctx,
		`SELECT id, is_nop FROM game_teams WHERE token = $1 AND status = 'active'`, token,
	).Scan(&teamID, &isNOP)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, false, false, nil
	}
	if err != nil {
		return uuid.UUID{}, false, false, err
	}
	return teamID, isNOP, true, nil
}

// RotateTeamToken generates a fresh token for the given team and persists it.
// Returns the new token value.
func RotateTeamToken(ctx context.Context, db *database.DB, teamID uuid.UUID) (string, error) {
	tok, err := GenerateTeamToken()
	if err != nil {
		return "", err
	}
	tag, err := db.Pool.Exec(ctx,
		`UPDATE game_teams SET token = $1 WHERE id = $2`, tok, teamID)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", errors.New("team not found")
	}
	return tok, nil
}
