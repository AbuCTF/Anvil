package game

import (
	"context"
	"strconv"
)

// Native-teams KotH bridge (migration 030). When koth_native_enabled is set, the
// engine scores the REAL teams table instead of the parallel game_teams identity:
// every bought-in team (koth_token set) is mirrored into game_teams with the SAME
// id, so token->team attribution (teamTokens) and the whole pollHills/standings
// machinery work unchanged; game_standings.koth is then folded into teams.koth_score
// for the one unified scoreboard. Collapsing game_teams entirely is a finals cleanup.

func (c *Controller) kothNativeEnabled(ctx context.Context) bool {
	var v string
	if err := c.db.Pool.QueryRow(ctx,
		`SELECT value FROM platform_settings WHERE key = 'koth_native_enabled'`).Scan(&v); err != nil {
		return false
	}
	return v == "true"
}

func (c *Controller) kothScoreCap(ctx context.Context) float64 {
	const fallback = 100000
	var v string
	if err := c.db.Pool.QueryRow(ctx,
		`SELECT value FROM platform_settings WHERE key = 'koth_score_cap'`).Scan(&v); err != nil {
		return fallback
	}
	ceiling, err := strconv.ParseFloat(v, 64)
	if err != nil || ceiling <= 0 {
		return fallback
	}
	return ceiling
}

// syncNativeTeams mirrors every bought-in real team into game_teams with the same id
// (slug = id, token = koth_token), so the KotH loop attributes holds to real teams.
// Idempotent; picks up new buy-ins each tick.
func (c *Controller) syncNativeTeams(ctx context.Context) error {
	_, err := c.db.Pool.Exec(ctx,
		`INSERT INTO game_teams (id, name, slug, token, status, is_nop, created_at, updated_at)
		 SELECT t.id, LEFT(COALESCE(NULLIF(t.name, ''), 'team'), 100), t.id::text, t.koth_token,
		        'active', FALSE, NOW(), NOW()
		   FROM teams t
		  WHERE t.koth_token IS NOT NULL
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   token = EXCLUDED.token,
		   status = 'active',
		   updated_at = NOW()`)
	return err
}

// foldNativeScore copies each team's engine-side KotH total (game_standings.koth,
// which recomputeStandings maintains from hold ticks + round rank bonuses) into
// teams.koth_score, capped. POINTS ONLY — it never touches the credit ledger, so
// arena hold-time can't be farmed into jeopardy compute-reach.
func (c *Controller) foldNativeScore(ctx context.Context) error {
	_, err := c.db.Pool.Exec(ctx,
		`UPDATE teams t SET koth_score = LEAST(gs.koth, $1::numeric)
		   FROM game_standings gs
		  WHERE gs.team_id = t.id
		    AND t.koth_token IS NOT NULL`, c.kothScoreCap(ctx))
	return err
}
