package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// GameInternalHandler serves the server-to-server finals calls from the model c
// pipeline (CTF26/handoffs/finals-build/INTERNAL-API.md sections 2 and 3).
type GameInternalHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewGameInternalHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *GameInternalHandler {
	return &GameInternalHandler{config: cfg, db: db, logger: logger}
}

type deployReq struct {
	TeamID       string     `json:"team_id" binding:"required"`
	Service      string     `json:"service" binding:"required"`
	Kind         string     `json:"kind" binding:"required"`
	ImageDigest  string     `json:"image_digest"`
	At           *time.Time `json:"at"`
	OrgInitiated bool       `json:"org_initiated"`
	DiffFiles    *int       `json:"diff_files"`
	DiffLines    *int       `json:"diff_lines"`
	DiffBytes    *int       `json:"diff_bytes"`
}

func (h *GameInternalHandler) serviceID(c *gin.Context, slug string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT id FROM game_services WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown service"})
		return id, false
	}
	if err != nil {
		h.logger.Error("internal: service lookup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return id, false
	}
	return id, true
}

// Deploy records a pipeline event. Deploy pricing and digest stamping read it.
func (h *GameInternalHandler) Deploy(c *gin.Context) {
	var req deployReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_id, service, kind required"})
		return
	}
	team, err := uuid.Parse(req.TeamID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_id must be a uuid"})
		return
	}
	switch req.Kind {
	case "deploy", "rollback", "revert", "reset", "fallback_a":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be deploy|rollback|revert|reset|fallback_a"})
		return
	}
	service, ok := h.serviceID(c, strings.TrimSpace(req.Service))
	if !ok {
		return
	}
	at := time.Now()
	if req.At != nil {
		at = *req.At
	}
	ctx := c.Request.Context()
	var id int64
	err = h.db.Pool.QueryRow(ctx,
		`INSERT INTO game_deploys (team_id, service_id, kind, org_initiated, image_digest, tick_number,
		   diff_files, diff_lines, diff_bytes, deployed_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''),
		   (SELECT MAX(tick_number) FROM game_ticks WHERE started_at <= $6),
		   $7, $8, $9, $6)
		 RETURNING id`,
		team, service, req.Kind, req.OrgInitiated, req.ImageDigest, at,
		req.DiffFiles, req.DiffLines, req.DiffBytes).Scan(&id)
	if err != nil {
		h.logger.Error("internal: record deploy", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not record deploy (unknown team?)"})
		return
	}
	var used int
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM game_deploys
		 WHERE team_id = $1 AND service_id = $2 AND kind = 'deploy' AND NOT org_initiated`,
		team, service).Scan(&used); err != nil {
		h.logger.Error("internal: count deploys", zap.Error(err))
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "free_left": max(0, h.config.Game.Scoring.FreeDeploys-used)})
}

// SLA returns a team's effective per-tick verdicts for one service, for auto-revert.
func (h *GameInternalHandler) SLA(c *gin.Context) {
	team, err := uuid.Parse(c.Query("team_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_id must be a uuid"})
		return
	}
	service, ok := h.serviceID(c, c.Query("service"))
	if !ok {
		return
	}
	since, _ := strconv.Atoi(c.Query("since"))
	ticks, err := game.EffectiveSLA(c.Request.Context(), h.db, team, since, h.config.Game.Scoring.MassFaultTeams)
	if err != nil {
		h.logger.Error("internal: effective sla", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	out := make([]gin.H, 0, len(ticks))
	for _, t := range ticks {
		if t.ServiceID != service {
			continue
		}
		out = append(out, gin.H{"tick": t.Tick, "status": t.Effective, "raw_status": t.Status,
			"message": t.Message, "image_digest": t.ImageDigest})
	}
	c.JSON(http.StatusOK, out)
}
