package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// finalsScoreRow is the per-team row in the finals mirror scoreboard.
type finalsScoreRow struct {
	Rank     int                      `json:"rank"`
	TeamID   string                   `json:"team_id"`
	TeamName string                   `json:"team_name"`
	Steal    float64                  `json:"steal"`
	Hold     float64                  `json:"hold"`
	Up       float64                  `json:"up"`
	AD       float64                  `json:"ad"`
	Koth     float64                  `json:"koth"`
	Total    float64                  `json:"total"`
	Services []finalsServiceBreakdown `json:"services"`
}

type finalsServiceBreakdown struct {
	Slug  string  `json:"slug"`
	Name  string  `json:"name"`
	Steal float64 `json:"steal"`
	Hold  float64 `json:"hold"`
	Up    float64 `json:"up"`
}

// FinalsScoreboard serves GET /api/v1/arena/scoreboard in finals mode.
// Returns mirror scoring standings (steal/hold/up/ad/koth/total) with per-service
// breakdown. Public (no auth). Delayed by PublicViewTick during freeze.
func (h *GameHandler) FinalsScoreboard(c *gin.Context) {
	ctx := c.Request.Context()

	frozen, err := game.IsFrozen(ctx, h.db)
	if err != nil {
		h.logger.Error("finals scoreboard: IsFrozen", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	rows, err := h.finalsScoreboardRows(ctx)
	if err != nil {
		h.logger.Error("finals scoreboard: query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"frozen": frozen,
		"rows":   rows,
	})
}

func (h *GameHandler) finalsScoreboardRows(ctx context.Context) ([]finalsScoreRow, error) {
	// standings query: attack=steal, defense=hold, sla=up, ad=total-ad, koth, total
	sRows, err := h.db.Pool.Query(ctx,
		`SELECT gs.rank, gt.id::text, gt.name,
		        gs.attack, gs.defense, gs.sla, gs.ad, gs.koth, gs.total
		 FROM game_standings gs
		 JOIN game_teams gt ON gt.id = gs.team_id
		 WHERE gt.is_nop = false AND gt.status = 'active'
		 ORDER BY gs.rank ASC NULLS LAST, gt.id`)
	if err != nil {
		return nil, err
	}
	defer sRows.Close()

	rowMap := map[string]*finalsScoreRow{}
	ordered := []string{}
	for sRows.Next() {
		var r finalsScoreRow
		if err := sRows.Scan(&r.Rank, &r.TeamID, &r.TeamName,
			&r.Steal, &r.Hold, &r.Up, &r.AD, &r.Koth, &r.Total); err != nil {
			return nil, err
		}
		r.Services = []finalsServiceBreakdown{}
		rowMap[r.TeamID] = &r
		ordered = append(ordered, r.TeamID)
	}
	if err := sRows.Err(); err != nil {
		return nil, err
	}

	// per-service breakdown from game_score_breakdown
	bRows, err := h.db.Pool.Query(ctx,
		`SELECT sb.team_id::text, sv.slug, sv.name,
		        SUM(sb.steal), SUM(sb.hold), SUM(sb.up)
		 FROM game_score_breakdown sb
		 JOIN game_services sv ON sv.id = sb.service_id
		 GROUP BY sb.team_id, sv.id, sv.slug, sv.name
		 ORDER BY sv.sort_order, sv.name`)
	if err != nil {
		return nil, err
	}
	defer bRows.Close()

	for bRows.Next() {
		var teamID string
		var svc finalsServiceBreakdown
		if err := bRows.Scan(&teamID, &svc.Slug, &svc.Name, &svc.Steal, &svc.Hold, &svc.Up); err != nil {
			return nil, err
		}
		if r, ok := rowMap[teamID]; ok {
			r.Services = append(r.Services, svc)
		}
	}
	if err := bRows.Err(); err != nil {
		return nil, err
	}

	result := make([]finalsScoreRow, 0, len(ordered))
	for _, id := range ordered {
		result = append(result, *rowMap[id])
	}
	return result, nil
}

// kothScoreEntry is one hill in the /arena/koth/scores response.
type kothScoreEntry struct {
	HillID    string  `json:"hill_id"`
	Hill      string  `json:"hill"`
	TeamID    *string `json:"team_id"`
	TeamName  *string `json:"team_name"`
	HeldTicks int     `json:"held_ticks"`
	Score     float64 `json:"score"`
}

// KothScores serves GET /api/v1/arena/koth/scores.
// Returns per-hill current king (from crown events) with round-result totals.
// In finals, koth_hold=0 so all KotH points come from game_koth_results.
func (h *GameHandler) KothScores(c *gin.Context) {
	if h.off(c) {
		return
	}
	ctx := c.Request.Context()

	rows, err := h.db.Pool.Query(ctx,
		`SELECT h.id::text, h.name,
		        crown.team_id::text,
		        gt.name,
		        COALESCE(totals.held_ticks, 0),
		        COALESCE(totals.score, 0)
		 FROM game_koth_hills h
		 LEFT JOIN LATERAL (
		     SELECT team_id
		     FROM game_events
		     WHERE kind = 'crown' AND hill_id = h.id
		     ORDER BY tick_number DESC, id DESC
		     LIMIT 1
		 ) crown ON true
		 LEFT JOIN game_teams gt ON gt.id = crown.team_id
		 LEFT JOIN LATERAL (
		     SELECT SUM(held_ticks) AS held_ticks,
		            SUM(points + bounty) AS score
		     FROM game_koth_results
		     WHERE hill_id = h.id AND team_id = crown.team_id
		 ) totals ON crown.team_id IS NOT NULL
		 WHERE h.enabled = true
		 ORDER BY h.sort_order, h.name`)
	if err != nil {
		h.logger.Error("koth scores: query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	hills := []kothScoreEntry{}
	for rows.Next() {
		var e kothScoreEntry
		var teamIDStr *string
		var held *int
		var score *float64
		if err := rows.Scan(&e.HillID, &e.Hill, &teamIDStr, &e.TeamName, &held, &score); err != nil {
			h.logger.Error("koth scores: scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		e.TeamID = teamIDStr
		if held != nil {
			e.HeldTicks = *held
		}
		if score != nil {
			e.Score = *score
		}
		hills = append(hills, e)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("koth scores: rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hills": hills})
}

// mapSLAStatus translates ENO engine statuses to the player-facing vocabulary.
// DOWN→OFFLINE, FAULTY→MUMBLE, ERROR→ORG_FAULT; all others pass through.
func mapSLAStatus(raw string) string {
	switch raw {
	case "DOWN":
		return "OFFLINE"
	case "FAULTY":
		return "MUMBLE"
	case "ERROR":
		return "ORG_FAULT"
	default:
		return raw
	}
}

// flagFateFromCaptures derives the flag_fate string from capture counts.
func flagFateFromCaptures(realAttackers, nopAttackers int, checkStatus string) *string {
	if checkStatus == "DOWN" || checkStatus == "OFFLINE" {
		return nil
	}
	if checkStatus == "FLAG_NOT_FOUND" {
		s := "unreadable"
		return &s
	}
	var fate string
	switch {
	case realAttackers == 0 && nopAttackers > 0:
		fate = "tide"
	case realAttackers == 0:
		fate = "held"
	case realAttackers == 1:
		fate = "stolen_1"
	case realAttackers <= 3:
		fate = "stolen_2_3"
	case realAttackers <= 7:
		fate = "stolen_4_7"
	default:
		fate = "stolen_8plus"
	}
	return &fate
}

// teamForTokenOrUser resolves a team UUID from X-Team-Token or JWT, reusing
// the common teamIdentity helper on GameHandler.
// Returns (teamID, found, error).
func teamForTokenOrUser(h *GameHandler, c *gin.Context) (uuid.UUID, bool) {
	id, _, _, ok := h.teamIdentity(c)
	return id, ok
}

// checkRowsErr is a convenience wrapper that logs and returns 500 on row error.
func checkRowsErr(c *gin.Context, h *GameHandler, where string, err error) bool {
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	h.logger.Error(where, zap.Error(err))
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	return true
}
