package handlers

import (
	"net/http"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// flagResult is the per-flag response shape for PUT /api/v1/arena/flags.
type flagResult struct {
	Flag   string `json:"flag"`
	Status string `json:"status"`
	Msg    string `json:"msg"`
}

// BulkSubmitFlags handles PUT /api/v1/arena/flags.
//
// Authenticated by the TeamTokenAuth middleware (X-Team-Token header), not by
// the standard JWT middleware – this is the farm exploit submission path and
// must support high-throughput pipelined clients (S4DFarm, DestructiveFarm,
// etc.). Up to 500 flags per request; each flag gets an independent result.
func (h *GameHandler) BulkSubmitFlags(c *gin.Context) {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "arena not enabled"})
		return
	}

	teamID := middleware.GetGameTeamID(c)
	if teamID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "X-Team-Token required"})
		return
	}

	var flags []string
	if err := c.ShouldBindJSON(&flags); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be a JSON array of flag strings"})
		return
	}
	if len(flags) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be a non-empty JSON array of flag strings"})
		return
	}
	if len(flags) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at most 500 flags per request"})
		return
	}

	// NOP teams: accept the request but don't record any captures.
	if middleware.GetGameTeamIsNOP(c) {
		results := make([]flagResult, len(flags))
		for i, f := range flags {
			results[i] = flagResult{Flag: f, Status: string(game.SubmitNop), Msg: "nop team"}
		}
		c.JSON(http.StatusOK, results)
		return
	}

	phase, ok, err := game.FinalsPhase(c.Request.Context(), h.db)
	if err != nil {
		h.logger.Error("bulk submit: FinalsPhase", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !ok || phase != game.PhaseLive {
		msg := phaseMessage(phase)
		results := make([]flagResult, len(flags))
		for i, f := range flags {
			results[i] = flagResult{Flag: f, Status: string(game.SubmitNotLive), Msg: msg}
		}
		c.JSON(http.StatusOK, results)
		return
	}

	results := make([]flagResult, len(flags))
	for i, flag := range flags {
		outcome, err := game.SubmitFlag(c.Request.Context(), h.db, *teamID, flag)
		if err != nil {
			h.logger.Error("bulk submit: SubmitFlag", zap.Error(err), zap.String("flag", flag))
			results[i] = flagResult{Flag: flag, Status: "error", Msg: "internal error"}
			continue
		}
		results[i] = flagResult{Flag: flag, Status: string(outcome), Msg: submitMsg(outcome)}
	}
	c.JSON(http.StatusOK, results)
}

// submitMsg maps a SubmitOutcome to the message substring required by farm
// clients (see 01-ad-formats.md §4.4).
func submitMsg(o game.SubmitOutcome) string {
	switch o {
	case game.SubmitAccepted:
		return "accepted"
	case game.SubmitDuplicate:
		return "already submitted"
	case game.SubmitOwnFlag:
		return "your own flag"
	case game.SubmitExpired:
		return "too old"
	case game.SubmitInvalid:
		return "invalid flag"
	case game.SubmitNop:
		return "nop team"
	case game.SubmitNotLive:
		return "game not live"
	default:
		return string(o)
	}
}
