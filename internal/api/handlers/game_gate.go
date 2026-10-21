package handlers

// game_gate.go — server-to-server endpoints for the finals deploy pipeline.
// Auth: RequireInternalToken middleware (game_internal_auth.go), applied in server.go.
// Empty token = route group doesn't exist; clients see 404.

import (
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// InternalGameHandler serves the /api/v1/internal/arena/* routes used by the
// model-C deploy pipeline (gate API, deploy events, SLA feed). It is never
// reachable unless game.internal_token is non-empty.
type InternalGameHandler struct {
	config *config.Config
	db     *database.DB
	gate   *game.GateRunner
	logger *zap.Logger
}

func NewInternalGameHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *InternalGameHandler {
	return &InternalGameHandler{
		config: cfg,
		db:     db,
		gate:   game.NewGateRunner(db, 16, cfg.Game.FlagPrefix),
		logger: logger,
	}
}

// Gate runs a full checker suite against a staging copy and returns per-task results.
// POST /api/v1/internal/arena/gate
func (h *InternalGameHandler) Gate(c *gin.Context) {
	var req struct {
		Service  string `json:"service" binding:"required"`
		Address  string `json:"address" binding:"required"`
		Rounds   int    `json:"rounds"`
		TimeoutS int    `json:"timeout_s"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rounds := req.Rounds
	if rounds < 1 {
		rounds = 1
	}

	timeout := time.Duration(req.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	result, err := h.gate.RunGate(c.Request.Context(), req.Service, req.Address, rounds, timeout)
	if err != nil {
		h.logger.Warn("gate run failed", zap.String("service", req.Service), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// Metrics returns a live engine health snapshot.
// GET /api/v1/internal/arena/metrics
func (h *InternalGameHandler) Metrics(c *gin.Context) {
	m, err := game.QueryMetrics(c.Request.Context(), h.db, h.config.Game.TickInterval, 5)
	if err != nil {
		h.logger.Warn("metrics query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}
