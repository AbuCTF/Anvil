package handlers

import (
	"net/http"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
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
