package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type GameHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewGameHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *GameHandler {
	return &GameHandler{config: cfg, db: db, logger: logger}
}

type submitFlagRequest struct {
	Flag string `json:"flag" binding:"required"`
}

// SubmitFlag records a stolen flag for the caller's team.
func (h *GameHandler) SubmitFlag(c *gin.Context) {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not active"})
		return
	}

	userID := middleware.GetUserID(c)
	if userID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req submitFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag required"})
		return
	}

	ctx := c.Request.Context()
	teamID, ok, err := game.TeamForUser(ctx, h.db, *userID)
	if err != nil {
		h.logger.Error("submit: team lookup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "not on a team"})
		return
	}

	outcome, err := game.SubmitFlag(ctx, h.db, teamID, req.Flag)
	if err != nil {
		h.logger.Error("submit: record capture", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	switch outcome {
	case game.SubmitAccepted:
		c.JSON(http.StatusOK, gin.H{"status": "accepted"})
	case game.SubmitDuplicate:
		c.JSON(http.StatusOK, gin.H{"status": "duplicate", "message": "already submitted"})
	case game.SubmitOwnFlag:
		c.JSON(http.StatusBadRequest, gin.H{"status": "rejected", "message": "own flag"})
	case game.SubmitExpired:
		c.JSON(http.StatusBadRequest, gin.H{"status": "rejected", "message": "flag expired"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"status": "rejected", "message": "invalid flag"})
	}
}

func (h *GameHandler) off(c *gin.Context) bool {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not active"})
		return true
	}
	return false
}

type gameStanding struct {
	Rank    *int    `json:"rank,omitempty"`
	TeamID  string  `json:"team_id"`
	Team    string  `json:"team"`
	Attack  float64 `json:"attack"`
	Defense float64 `json:"defense"`
	SLA     float64 `json:"sla"`
	Koth    float64 `json:"koth"`
	Total   float64 `json:"total"`
}

func (h *GameHandler) standingsData(ctx context.Context) ([]gameStanding, error) {
	rows, err := h.db.Pool.Query(ctx,
		`SELECT t.id, t.name, s.attack, s.defense, s.sla, s.koth, s.total, s.rank
		 FROM game_standings s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false
		 ORDER BY s.rank ASC NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	standings := []gameStanding{}
	for rows.Next() {
		var e gameStanding
		var id uuid.UUID
		if err := rows.Scan(&id, &e.Team, &e.Attack, &e.Defense, &e.SLA, &e.Koth, &e.Total, &e.Rank); err != nil {
			continue
		}
		e.TeamID = id.String()
		standings = append(standings, e)
	}
	return standings, nil
}

// Scoreboard returns the combined AD + KotH standings.
func (h *GameHandler) Scoreboard(c *gin.Context) {
	if h.off(c) {
		return
	}
	standings, err := h.standingsData(c.Request.Context())
	if err != nil {
		h.logger.Error("scoreboard query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"standings": standings})
}

type gameHill struct {
	HillID     string  `json:"hill_id"`
	Name       string  `json:"name"`
	Controller *string `json:"controller,omitempty"`
}

func (h *GameHandler) hillsData(ctx context.Context) ([]gameHill, error) {
	rows, err := h.db.Pool.Query(ctx,
		`SELECT h.id, h.name, t.name
		 FROM game_koth_hills h
		 LEFT JOIN LATERAL (
		   SELECT controller_team_id FROM game_koth_control
		   WHERE hill_id = h.id ORDER BY tick_number DESC LIMIT 1
		 ) kc ON true
		 LEFT JOIN game_teams t ON t.id = kc.controller_team_id
		 WHERE h.enabled = true
		 ORDER BY h.sort_order, h.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hills := []gameHill{}
	for rows.Next() {
		var e gameHill
		var id uuid.UUID
		if err := rows.Scan(&id, &e.Name, &e.Controller); err != nil {
			continue
		}
		e.HillID = id.String()
		hills = append(hills, e)
	}
	return hills, nil
}

// Hills returns each hill's current controller for the control-map.
func (h *GameHandler) Hills(c *gin.Context) {
	if h.off(c) {
		return
	}
	hills, err := h.hillsData(c.Request.Context())
	if err != nil {
		h.logger.Error("hills query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hills": hills})
}

func (h *GameHandler) tickRound(ctx context.Context) (int, int) {
	var tick, round int
	h.db.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&tick)
	h.db.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(round_number), 0) FROM game_koth_rounds`).Scan(&round)
	return tick, round
}

func (h *GameHandler) statusPayload(ctx context.Context) gin.H {
	tick, round := h.tickRound(ctx)
	return gin.H{
		"tick":                  tick,
		"round":                 round,
		"tick_interval_seconds": int(h.config.Game.TickInterval.Seconds()),
	}
}

// Status returns the current tick and KotH round.
func (h *GameHandler) Status(c *gin.Context) {
	if h.off(c) {
		return
	}
	c.JSON(http.StatusOK, h.statusPayload(c.Request.Context()))
}

type historyPoint struct {
	Tick  int     `json:"x"`
	Total float64 `json:"y"`
}

type historySeries struct {
	TeamID string         `json:"team_id"`
	Team   string         `json:"team"`
	Points []historyPoint `json:"points"`
}

func (h *GameHandler) historyData(ctx context.Context) ([]*historySeries, error) {
	rows, err := h.db.Pool.Query(ctx,
		`SELECT t.id, t.name, s.tick_number, s.total
		 FROM game_score_snapshots s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false
		 ORDER BY t.name, s.tick_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := []string{}
	byTeam := map[string]*historySeries{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var tick int
		var total float64
		if err := rows.Scan(&id, &name, &tick, &total); err != nil {
			continue
		}
		key := id.String()
		s := byTeam[key]
		if s == nil {
			s = &historySeries{TeamID: key, Team: name}
			byTeam[key] = s
			order = append(order, key)
		}
		s.Points = append(s.Points, historyPoint{Tick: tick, Total: total})
	}

	series := make([]*historySeries, 0, len(order))
	for _, k := range order {
		series = append(series, byTeam[k])
	}
	return series, nil
}

// History returns each team's score over time for the race chart.
func (h *GameHandler) History(c *gin.Context) {
	if h.off(c) {
		return
	}
	series, err := h.historyData(c.Request.Context())
	if err != nil {
		h.logger.Error("history query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"series": series})
}

type matrixService struct {
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Tier      string `json:"tier"`
}

type matrixCell struct {
	Status    string `json:"status"`
	LatencyMs *int   `json:"latency_ms,omitempty"`
}

type matrixRow struct {
	TeamID string       `json:"team_id"`
	Team   string       `json:"team"`
	Rank   *int         `json:"rank,omitempty"`
	Cells  []matrixCell `json:"cells"`
}

func (h *GameHandler) matrixData(ctx context.Context) ([]matrixService, []matrixRow, error) {
	svcRows, err := h.db.Pool.Query(ctx,
		`SELECT id, name, category, tier FROM game_services WHERE enabled = true ORDER BY sort_order, name`)
	if err != nil {
		return nil, nil, err
	}
	services := []matrixService{}
	for svcRows.Next() {
		var s matrixService
		var id uuid.UUID
		if err := svcRows.Scan(&id, &s.Name, &s.Category, &s.Tier); err != nil {
			continue
		}
		s.ServiceID = id.String()
		services = append(services, s)
	}
	svcRows.Close()

	slaRows, err := h.db.Pool.Query(ctx,
		`SELECT DISTINCT ON (team_id, service_id) team_id, service_id, status, latency_ms
		 FROM game_sla_checks ORDER BY team_id, service_id, tick_number DESC`)
	if err != nil {
		return nil, nil, err
	}
	latest := map[string]matrixCell{}
	for slaRows.Next() {
		var team, svc uuid.UUID
		var cell matrixCell
		if err := slaRows.Scan(&team, &svc, &cell.Status, &cell.LatencyMs); err != nil {
			continue
		}
		latest[team.String()+"|"+svc.String()] = cell
	}
	slaRows.Close()

	teamRows, err := h.db.Pool.Query(ctx,
		`SELECT t.id, t.name, s.rank FROM game_teams t
		 JOIN game_standings s ON s.team_id = t.id
		 WHERE t.is_nop = false ORDER BY s.rank ASC NULLS LAST`)
	if err != nil {
		return nil, nil, err
	}
	defer teamRows.Close()

	rows := []matrixRow{}
	for teamRows.Next() {
		var r matrixRow
		var id uuid.UUID
		if err := teamRows.Scan(&id, &r.Team, &r.Rank); err != nil {
			continue
		}
		r.TeamID = id.String()
		r.Cells = make([]matrixCell, len(services))
		for i, s := range services {
			if cell, ok := latest[r.TeamID+"|"+s.ServiceID]; ok {
				r.Cells[i] = cell
			} else {
				r.Cells[i] = matrixCell{Status: "UNKNOWN"}
			}
		}
		rows = append(rows, r)
	}
	return services, rows, nil
}

// Services returns the teams x services SLA matrix, rows ordered by rank.
func (h *GameHandler) Services(c *gin.Context) {
	if h.off(c) {
		return
	}
	services, rows, err := h.matrixData(c.Request.Context())
	if err != nil {
		h.logger.Error("matrix query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"services": services, "rows": rows})
}

type gameEvent struct {
	Tick     int    `json:"tick"`
	Attacker string `json:"attacker"`
	Victim   string `json:"victim"`
	Service  string `json:"service"`
	At       int64  `json:"at"`
}

func (h *GameHandler) eventsData(ctx context.Context) ([]gameEvent, error) {
	rows, err := h.db.Pool.Query(ctx,
		`SELECT cp.tick_number, a.name, v.name, s.name, cp.submitted_at
		 FROM game_captures cp
		 JOIN game_teams a ON a.id = cp.attacker_team_id
		 JOIN game_teams v ON v.id = cp.victim_team_id
		 JOIN game_services s ON s.id = cp.service_id
		 ORDER BY cp.submitted_at DESC LIMIT 40`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []gameEvent{}
	for rows.Next() {
		var e gameEvent
		var at time.Time
		if err := rows.Scan(&e.Tick, &e.Attacker, &e.Victim, &e.Service, &at); err != nil {
			continue
		}
		e.At = at.Unix()
		events = append(events, e)
	}
	return events, nil
}

// Events returns the most recent flag captures for the live event feed.
func (h *GameHandler) Events(c *gin.Context) {
	if h.off(c) {
		return
	}
	events, err := h.eventsData(c.Request.Context())
	if err != nil {
		h.logger.Error("events query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

// State returns the whole arena snapshot in one response, so each viewer polls
// a single endpoint instead of fanning out across six.
func (h *GameHandler) State(c *gin.Context) {
	if h.off(c) {
		return
	}
	ctx := c.Request.Context()

	standings, err := h.standingsData(ctx)
	if err != nil {
		h.logger.Error("state standings", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	hills, err := h.hillsData(ctx)
	if err != nil {
		h.logger.Error("state hills", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	history, err := h.historyData(ctx)
	if err != nil {
		h.logger.Error("state history", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	services, rows, err := h.matrixData(ctx)
	if err != nil {
		h.logger.Error("state matrix", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	events, err := h.eventsData(ctx)
	if err != nil {
		h.logger.Error("state events", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    h.statusPayload(ctx),
		"hills":     hills,
		"standings": standings,
		"services":  services,
		"rows":      rows,
		"events":    events,
		"history":   history,
	})
}
