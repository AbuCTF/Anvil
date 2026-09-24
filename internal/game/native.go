package game

import (
	"context"
	"strconv"

	"github.com/google/uuid"
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

// syncNativeTeams mirrors every real team that has entered ANY arena into game_teams
// with the same id, so the KotH loop's controller_team_id (FK -> game_teams) and the
// standings machinery attribute holds to real teams. game_teams.token is unused in
// native mode — hill control is resolved per challenge from koth_entries (entryTokens),
// not this column. Idempotent; picks up new buy-ins each tick.
func (c *Controller) syncNativeTeams(ctx context.Context) error {
	_, err := c.db.Pool.Exec(ctx,
		`INSERT INTO game_teams (id, name, slug, status, is_nop, created_at, updated_at)
		 SELECT t.id, LEFT(COALESCE(NULLIF(t.name, ''), 'team'), 100), t.id::text,
		        'active', FALSE, NOW(), NOW()
		   FROM teams t
		  WHERE EXISTS (SELECT 1 FROM koth_entries e WHERE e.team_id = t.id)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   status = 'active',
		   updated_at = NOW()`)
	return err
}

// entryTokens maps each team's opaque token to its team id for ONE arena (challenge).
// Scoped per challenge so a token planted on the wrong arena is never attributed.
func (c *Controller) entryTokens(ctx context.Context, challengeID uuid.UUID) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID)
	rows, err := c.db.Pool.Query(ctx,
		`SELECT token, team_id FROM koth_entries WHERE challenge_id = $1`, challengeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var token string
		var id uuid.UUID
		if err := rows.Scan(&token, &id); err != nil {
			return nil, err
		}
		out[token] = id
	}
	return out, rows.Err()
}

// foldNativeScore copies each mirrored team's engine-side KotH total (game_standings.koth,
// which recomputeStandings maintains from hold ticks + round rank bonuses SUMMED across
// every arena) into teams.koth_score, capped ONCE. The single cap over the summed total
// keeps KotH a bounded bonus tier no matter how many arenas run. POINTS ONLY — it never
// touches the credit ledger, so arena hold-time can't be farmed into jeopardy compute-reach.
func (c *Controller) foldNativeScore(ctx context.Context) error {
	_, err := c.db.Pool.Exec(ctx,
		`UPDATE teams t SET koth_score = LEAST(gs.koth, $1::numeric)
		   FROM game_standings gs
		  WHERE gs.team_id = t.id`, c.kothScoreCap(ctx))
	return err
}
