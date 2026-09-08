package handlers

import (
	"net/http"

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

// Scoreboard returns the combined AD + KotH standings.
func (h *GameHandler) Scoreboard(c *gin.Context) {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not active"})
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT t.id, t.name, s.attack, s.defense, s.sla, s.koth, s.total, s.rank
		 FROM game_standings s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false
		 ORDER BY s.rank ASC NULLS LAST`)
	if err != nil {
		h.logger.Error("scoreboard query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
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
	c.JSON(http.StatusOK, gin.H{"standings": standings})
}

type gameHill struct {
	HillID     string  `json:"hill_id"`
	Name       string  `json:"name"`
	Controller *string `json:"controller,omitempty"`
}

// Hills returns each hill's current controller for the control-map.
func (h *GameHandler) Hills(c *gin.Context) {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not active"})
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
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
		h.logger.Error("hills query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
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
	c.JSON(http.StatusOK, gin.H{"hills": hills})
}

// Status returns the current tick and KotH round.
func (h *GameHandler) Status(c *gin.Context) {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not active"})
		return
	}

	ctx := c.Request.Context()
	var tick, round int
	h.db.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&tick)
	h.db.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(round_number), 0) FROM game_koth_rounds`).Scan(&round)

	c.JSON(http.StatusOK, gin.H{
		"tick":                  tick,
		"round":                 round,
		"tick_interval_seconds": int(h.config.Game.TickInterval.Seconds()),
	})
}
