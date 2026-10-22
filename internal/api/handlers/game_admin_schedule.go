package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type extendReq struct {
	Minutes int `json:"minutes" binding:"required"`
}

type gameStateReq struct {
	State string `json:"state" binding:"required"`
}

func (h *GameAdminHandler) SetState(c *gin.Context) {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusConflict, gin.H{"error": "game engine is disabled"})
		return
	}
	var req gameStateReq
	if err := bindArenaAdminJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state must be running, paused, or stopped"})
		return
	}
	next := game.State(strings.ToLower(strings.TrimSpace(req.State)))
	if next != game.StateRunning && next != game.StatePaused && next != game.StateStopped {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state must be running, paused, or stopped"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.fail(c, "game state", err)
		return
	}
	defer tx.Rollback(ctx)

	var previous string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(value #>> '{}', '') FROM platform_settings WHERE key = 'game.state' FOR UPDATE`,
	).Scan(&previous); err != nil && err != pgx.ErrNoRows {
		h.fail(c, "game state", err)
		return
	}
	actor := adminID(c)
	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_settings (key, value, description, category, updated_by)
		VALUES ('game.state', to_jsonb($1::text), 'Attack-defense game state', 'arena', NULLIF($2, '')::uuid)
		ON CONFLICT (key) DO UPDATE SET
			value = EXCLUDED.value,
			updated_at = NOW(),
			updated_by = EXCLUDED.updated_by`, string(next), actor); err != nil {
		h.fail(c, "game state", err)
		return
	}
	metadata, _ := json.Marshal(gin.H{"previous": previous, "state": next})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (user_id, action, entity_type, new_values, ip_address, user_agent)
		VALUES (NULLIF($1, '')::uuid, 'arena.state_changed', 'arena', $2::jsonb, $3, $4)`,
		actor, string(metadata), c.ClientIP(), c.Request.UserAgent()); err != nil {
		h.fail(c, "game state audit", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.fail(c, "game state", err)
		return
	}

	h.logger.Info("finals state changed", zap.String("previous", previous), zap.String("state", string(next)), zap.String("by", actor))
	response := gin.H{"state": next}
	if phase, _, err := game.FinalsPhase(ctx, h.db); err != nil {
		h.logger.Warn("failed to resolve finals phase after state change", zap.Error(err))
	} else {
		response["phase"] = phase
	}
	c.JSON(http.StatusOK, response)
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
	h.logger.Info("finals extended", zap.Int("minutes", req.Minutes), zap.String("by", adminID(c)))
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

type creditReq struct {
	Service  string `json:"service" binding:"required"` // slug
	TeamID   string `json:"team_id"`                    // empty = every team
	TickFrom int    `json:"tick_from" binding:"required"`
	TickTo   int    `json:"tick_to" binding:"required"`
	Reason   string `json:"reason" binding:"required"`
}

// Credit records an organizer re-credit: those ticks count as ORG_FAULT for the
// scorer and every team's status page from the next recompute on.
func (h *GameAdminHandler) Credit(c *gin.Context) {
	var req creditReq
	if err := bindArenaAdminJSON(c, &req); err != nil || req.TickTo < req.TickFrom || req.TickFrom < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service, tick_from <= tick_to and reason required"})
		return
	}
	reason, err := validateArenaText(req.Reason, "reason", 200)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var team *string
	if t := strings.TrimSpace(req.TeamID); t != "" {
		team = &t
	}
	var id int64
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO game_sla_credits (service_id, team_id, tick_from, tick_to, reason, created_by)
		 SELECT s.id, $2::uuid, $3, $4, $5, NULLIF($6, '')::uuid FROM game_services s WHERE s.slug = $1
		 RETURNING id`,
		req.Service, team, req.TickFrom, req.TickTo, reason, adminID(c)).Scan(&id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown service or team"})
		return
	}
	h.logger.Info("sla re-credit", zap.Int64("id", id), zap.String("service", req.Service),
		zap.Int("from", req.TickFrom), zap.Int("to", req.TickTo), zap.String("reason", reason))
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// Resettle re-scores a finished game from raw rows (after a late re-credit).
func (h *GameAdminHandler) Resettle(c *gin.Context) {
	if err := game.Resettle(c.Request.Context(), h.config.Game, h.db, h.logger); err != nil {
		h.fail(c, "resettle", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "resettled"})
}

func adminID(c *gin.Context) string {
	if uid := middleware.GetUserID(c); uid != nil {
		return uid.String()
	}
	return ""
}
