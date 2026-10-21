package game

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/jackc/pgx/v5"
)

// State is the finals game switch, platform_settings game.state. Unset keeps the
// quals behavior, where koth_native_enabled alone drives ticking; demo runs with the
// engine enabled, so finals behavior must hang off this setting, never game.enabled.
type State string

const (
	StateLegacy  State = ""
	StateRunning State = "running"
	StatePaused  State = "paused"
	StateStopped State = "stopped"
)

// ParseState normalizes a stored value. Anything unrecognized pauses the game rather
// than guessing, so a typo stops the clock instead of running it.
func ParseState(raw string) State {
	switch s := State(strings.ToLower(strings.TrimSpace(raw))); s {
	case StateLegacy, StateRunning, StatePaused, StateStopped:
		return s
	default:
		return StatePaused
	}
}

// ReadState reads game.state as bare text (value #>> '{}'): a jsonb string scans back
// quoted otherwise, and the compare silently never matches.
func ReadState(ctx context.Context, db *database.DB) (State, error) {
	var raw string
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(value #>> '{}', '') FROM platform_settings WHERE key = 'game.state'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return StateLegacy, nil
	}
	if err != nil {
		return StateLegacy, err
	}
	return ParseState(raw), nil
}

// Phase is why a finals game is or isn't live right now.
type Phase string

const (
	PhaseLive       Phase = "live"
	PhaseNotStarted Phase = "not_started"
	PhasePaused     Phase = "paused"
	PhaseOver       Phase = "over"
)

// phaseAt places a running/paused/stopped game against the event window. Either
// bound may be zero (unset).
func phaseAt(state State, now, start, end time.Time) Phase {
	switch {
	case state == StateStopped:
		return PhaseOver
	case !end.IsZero() && !now.Before(end):
		return PhaseOver
	case !start.IsZero() && now.Before(start):
		return PhaseNotStarted
	case state == StatePaused:
		return PhasePaused
	default:
		return PhaseLive
	}
}

// FinalsPhase reports the finals phase. ok is false in legacy mode (game.state unset),
// where callers keep their quals behavior untouched.
func FinalsPhase(ctx context.Context, db *database.DB) (Phase, bool, error) {
	state, err := ReadState(ctx, db)
	if err != nil || state == StateLegacy {
		return "", false, err
	}
	start, end, err := eventWindow(ctx, db)
	if err != nil {
		return "", true, err
	}
	return phaseAt(state, time.Now(), start, end), true, nil
}

func eventWindow(ctx context.Context, db *database.DB) (time.Time, time.Time, error) {
	var startStr, endStr string
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT value #>> '{}' FROM platform_settings WHERE key = 'event.start_at'), ''),
		        COALESCE((SELECT value #>> '{}' FROM platform_settings WHERE key = 'event.end_at'), '')`,
	).Scan(&startStr, &endStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return parseSettingTime(startStr), parseSettingTime(endStr), nil
}

func parseSettingTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
