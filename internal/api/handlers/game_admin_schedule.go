package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type extendReq struct {
	Minutes int `json:"minutes" binding:"required"`
}

// Extend shifts every future scheduled moment after a pause: the end and freeze of
// the event, service releases, outbreak fallback slots, hill windows and the running
// koth round. The outbreak quiet window is wall clock and does not move.
func (h *GameAdminHandler) Extend(c *gin.Context) {
	var req extendReq
	if err := bindArenaAdminJSON(c, &req); err != nil || req.Minutes < 1 || req.Minutes > 24*60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "minutes must be between 1 and 1440"})
		return
	}
	if err := game.ExtendGame(c.Request.Context(), h.db, time.Duration(req.Minutes)*time.Minute); err != nil {
		h.fail(c, "extend game", err)
		return
	}
	h.logger.Info("finals extended", zap.Int("minutes", req.Minutes), zap.String("by", c.GetString("user_id")))
	c.JSON(http.StatusOK, gin.H{"status": "extended", "minutes": req.Minutes})
}

type hillWindow struct {
	OpensAt  time.Time `json:"opens_at"`
	ClosesAt time.Time `json:"closes_at"`
}

type hillWindowsReq struct {
	Windows []hillWindow `json:"windows"`
}

// SetHillWindows replaces a hill's schedule; an empty list hands the hill back to
// its manual enabled flag.
func (h *GameAdminHandler) SetHillWindows(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	var req hillWindowsReq
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "windows required"})
		return
	}
	for _, w := range req.Windows {
		if !w.ClosesAt.After(w.OpensAt) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "each window must close after it opens"})
			return
		}
	}
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.fail(c, "hill windows", err)
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `SELECT 1 FROM game_koth_hills WHERE id = $1`, id)
	if err != nil || tag.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "hill not found"})
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM game_koth_windows WHERE hill_id = $1`, id); err != nil {
		h.fail(c, "hill windows", err)
		return
	}
	for _, w := range req.Windows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO game_koth_windows (hill_id, opens_at, closes_at) VALUES ($1, $2, $3)`,
			id, w.OpensAt, w.ClosesAt); err != nil {
			h.fail(c, "hill windows", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.fail(c, "hill windows", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated", "windows": len(req.Windows)})
}

type serviceOutbreakReq struct {
	Advisories   map[string]string `json:"advisories"`    // store index -> advisory line
	AdvisoryAt   *string           `json:"advisory_at"`   // RFC3339 fallback slot, "" clears
	WaveExploits map[string]int    `json:"wave_exploits"` // store index -> exploit variant
}

// SetServiceOutbreak sets a service's outbreak advisory lines, fallback slot and
// wave exploit variants (contract section 7, INTERNAL-API section 4).
func (h *GameAdminHandler) SetServiceOutbreak(c *gin.Context) {
	id, ok := validatedArenaID(c, "id")
	if !ok {
		return
	}
	var req serviceOutbreakReq
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	for k, v := range req.Advisories {
		if strings.ContainsAny(v, "–—") || len(v) > 300 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "advisory " + k + ": at most 300 chars and no em or en dashes"})
			return
		}
	}
	var advisoryAt *time.Time
	if req.AdvisoryAt != nil {
		var err error
		if advisoryAt, err = parseOptionalTime(*req.AdvisoryAt); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "advisory_at must be RFC3339"})
			return
		}
	}
	adv, _ := json.Marshal(req.Advisories)
	waves, _ := json.Marshal(req.WaveExploits)
	tag, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE game_services SET
		   advisories = CASE WHEN $2 THEN $3::jsonb ELSE advisories END,
		   advisory_at = CASE WHEN $4 THEN $5 ELSE advisory_at END,
		   wave_exploits = CASE WHEN $6 THEN $7::jsonb ELSE wave_exploits END,
		   updated_at = NOW()
		 WHERE id = $1`,
		id, req.Advisories != nil, string(adv), req.AdvisoryAt != nil, advisoryAt, req.WaveExploits != nil, string(waves))
	if err != nil {
		h.fail(c, "service outbreak", err)
		return
	}
	if tag.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}
