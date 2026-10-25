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
	Jeopardy *float64                 `json:"jeopardy,omitempty"`
	Total    float64                  `json:"total"`
	Share    *scoreShare              `json:"share,omitempty"`
	Services []finalsServiceBreakdown `json:"services"`
}

// scoreWeights is the live finals blend, served so boards label it from config
type scoreWeights struct {
	AD       float64 `json:"ad"`
	Koth     float64 `json:"koth"`
	Jeopardy float64 `json:"jeopardy"`
}

// scoreShare is a team's total cut into what each board earned; the parts add up to total
type scoreShare struct {
	AD       float64  `json:"ad"`
	Koth     float64  `json:"koth"`
	Jeopardy *float64 `json:"jeopardy,omitempty"`
}

// scoreWeights is nil outside the mirror formula: legacy totals are plain sums, not blends
func (h *GameHandler) scoreWeights() *scoreWeights {
	if h.config == nil || h.config.Game.Scoring.Formula != "mirror" {
		return nil
	}
	sc := h.config.Game.Scoring
	return &scoreWeights{AD: sc.WeightAD, Koth: sc.WeightKoth, Jeopardy: sc.WeightJeopardy}
}

// shares splits totals over one field, which must be the mirror's own (every active
// non-nop standing) or the floors drift from the ones the total was built on
func (h *GameHandler) shares(ad, koth, total []float64) []scoreShare {
	w := h.scoreWeights()
	if w == nil {
		return nil
	}
	adS, koS, jeS := game.Shares(w.AD, w.Koth, ad, koth, total)
	out := make([]scoreShare, len(ad))
	for i := range out {
		out[i] = scoreShare{AD: adS[i], Koth: koS[i]}
		if w.Jeopardy > 0 {
			out[i].Jeopardy = &jeS[i]
		}
	}
	return out
}

// attachShares fills the share on a (possibly filtered) standings list from the full field
func (h *GameHandler) attachShares(ctx context.Context, query gameStateQuerier, standings []gameStanding) error {
	if h.scoreWeights() == nil || len(standings) == 0 {
		return nil
	}
	rows, err := query.Query(ctx,
		`SELECT s.team_id::text, s.ad, s.koth, s.total
		 FROM game_standings s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false AND t.status = 'active'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	var ad, koth, total []float64
	for rows.Next() {
		var id string
		var a, k, tot float64
		if err := rows.Scan(&id, &a, &k, &tot); err != nil {
			return err
		}
		ids, ad, koth, total = append(ids, id), append(ad, a), append(koth, k), append(total, tot)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	byTeam := make(map[string]scoreShare, len(ids))
	for i, s := range h.shares(ad, koth, total) {
		byTeam[ids[i]] = s
	}
	for i := range standings {
		if s, ok := byTeam[standings[i].TeamID]; ok {
			standings[i].Share = &s
		}
	}
	return nil
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
// breakdown. Public while live; during a freeze only an authenticated admin may
// read the exact rows for the reveal console.
func (h *GameHandler) FinalsScoreboard(c *gin.Context) {
	ctx := c.Request.Context()

	snapshot, err := loadArenaFreezeSnapshot(ctx, h.db.Pool)
	if err != nil {
		h.logger.Error("finals scoreboard: freeze", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if snapshot.Frozen && c.GetString("role") != "admin" {
		rows, err := h.finalsScoreboardSnapshotRows(ctx, snapshot.Tick)
		if err != nil {
			h.logger.Error("finals scoreboard: snapshot query", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Header("Cache-Control", "private, no-store")
		c.JSON(http.StatusOK, gin.H{
			"frozen":  true,
			"rows":    rows,
			"weights": h.scoreWeights(),
		})
		return
	}

	rows, err := h.finalsScoreboardRows(ctx)
	if err != nil {
		h.logger.Error("finals scoreboard: query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if snapshot.Frozen {
		c.Header("Cache-Control", "private, no-store")
	}
	c.JSON(http.StatusOK, gin.H{
		"frozen":  snapshot.Frozen,
		"rows":    rows,
		"weights": h.scoreWeights(),
	})
}

func (h *GameHandler) finalsScoreboardSnapshotRows(ctx context.Context, tick int) ([]finalsScoreRow, error) {
	rows, err := h.db.Pool.Query(ctx,
		`SELECT COALESCE(s.rank, 0), gt.id::text, gt.name, s.total
		 FROM game_score_snapshots s
		 JOIN game_teams gt ON gt.id = s.team_id
		 WHERE s.tick_number = $1 AND gt.is_nop = false AND gt.status = 'active'
		   AND `+publicGameTeamSQL("gt")+`
		 ORDER BY s.rank ASC NULLS LAST, gt.id`, tick)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []finalsScoreRow{}
	for rows.Next() {
		var row finalsScoreRow
		if err := rows.Scan(&row.Rank, &row.TeamID, &row.TeamName, &row.Total); err != nil {
			return nil, err
		}
		row.Services = []finalsServiceBreakdown{}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (h *GameHandler) finalsScoreboardRows(ctx context.Context) ([]finalsScoreRow, error) {
	// standings query: attack=steal, defense=hold, sla=up, ad=total-ad, koth, total
	sRows, err := h.db.Pool.Query(ctx,
		`SELECT gs.rank, gt.id::text, gt.name,
		        gs.attack, gs.defense, gs.sla, gs.ad, gs.koth, `+h.jeopardyColumn("gt")+`, gs.total
		 FROM game_standings gs
		 JOIN game_teams gt ON gt.id = gs.team_id
		 WHERE gt.is_nop = false AND gt.status = 'active' AND `+publicGameTeamSQL("gt")+`
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
			&r.Steal, &r.Hold, &r.Up, &r.AD, &r.Koth, &r.Jeopardy, &r.Total); err != nil {
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
	ad, koth, total := make([]float64, 0, len(ordered)), make([]float64, 0, len(ordered)), make([]float64, 0, len(ordered))
	for _, id := range ordered {
		r := rowMap[id]
		result = append(result, *r)
		ad, koth, total = append(ad, r.AD), append(koth, r.Koth), append(total, r.Total)
	}
	// every active non-nop standing is here, the same field the mirror ranked
	for i, s := range h.shares(ad, koth, total) {
		result[i].Share = &s
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
	blinded := h.scoresBlinded(c)

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
		if blinded {
			// the king stays visible (the hill shows it), the points wait for the reveal
			held, score = nil, nil
		}
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
	if blinded {
		c.Header("Cache-Control", "private, no-store")
	}
	c.JSON(http.StatusOK, gin.H{"hills": hills, "frozen": blinded})
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
