package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// teamIdentity extracts the game team for a request. Accepts either
// X-Team-Token (farm/script path) or the standard JWT user (browser/dashboard).
// Returns the team UUID, a masked token (first 8 chars + "..."), and whether the
// caller is a NOP team.
func (h *GameHandler) teamIdentity(c *gin.Context) (teamID uuid.UUID, maskedToken string, isNOP bool, ok bool) {
	// X-Team-Token takes precedence so farm scripts don't need a JWT.
	if tok := c.GetHeader("X-Team-Token"); tok != "" {
		id, nop, found, err := game.TeamForToken(c.Request.Context(), h.db, tok)
		if err != nil || !found {
			return uuid.UUID{}, "", false, false
		}
		mask := tok
		if len(mask) > 8 {
			mask = mask[:8] + "..."
		}
		return id, mask, nop, true
	}
	// fall back to JWT user -> game team
	uid := middleware.GetUserID(c)
	if uid == nil {
		return uuid.UUID{}, "", false, false
	}
	id, found, err := game.TeamForUser(c.Request.Context(), h.db, *uid)
	if err != nil || !found {
		return uuid.UUID{}, "", false, false
	}
	// look up the team's token for masking
	var rawTok *string
	_ = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT token FROM game_teams WHERE id = $1`, id).Scan(&rawTok)
	mask := ""
	if rawTok != nil && len(*rawTok) > 8 {
		mask = (*rawTok)[:8] + "..."
	}
	return id, mask, false, true
}

// Tick serves GET /api/v1/arena/tick.
// Returns the current tick number, its start/end timestamps, and the game phase.
// No auth required (public; used by the status page clock and h7 deploy).
func (h *GameHandler) Tick(c *gin.Context) {
	if h.off(c) {
		return
	}
	phase, ok, err := game.FinalsPhase(c.Request.Context(), h.db)
	if err != nil {
		h.logger.Error("arena/tick: FinalsPhase", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "finals not active"})
		return
	}

	tick, startedAt, err := h.currentTickInfo(c.Request.Context())
	if err != nil {
		h.logger.Error("arena/tick: currentTickInfo", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	interval := h.config.Game.TickInterval
	endsAt := startedAt.Add(interval)
	c.JSON(http.StatusOK, gin.H{
		"tick":       tick,
		"started_at": startedAt,
		"ends_at":    endsAt,
		"phase":      string(phase),
	})
}

func (h *GameHandler) currentTickInfo(ctx context.Context) (int, time.Time, error) {
	var tick int
	var startedAt time.Time
	err := h.db.Pool.QueryRow(ctx,
		`SELECT tick_number, started_at FROM game_ticks ORDER BY tick_number DESC LIMIT 1`,
	).Scan(&tick, &startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, nil
	}
	return tick, startedAt, err
}

type teamInfo struct {
	TeamID      string  `json:"team_id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	TeamNumber  *int    `json:"team_number"`
	VulnboxIP   *string `json:"vulnbox_ip"`
	Token       string  `json:"token"`  // masked
	IsNOP       bool    `json:"is_nop"`
}

// Me serves GET /api/v1/arena/me.
// Returns the authenticated team's identity, vulnbox IP, and masked token.
// Accepts X-Team-Token or JWT.
func (h *GameHandler) Me(c *gin.Context) {
	if h.off(c) {
		return
	}
	teamID, maskedToken, isNOP, ok := h.teamIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "team not found; use X-Team-Token or a team JWT"})
		return
	}

	var info teamInfo
	info.Token = maskedToken
	info.IsNOP = isNOP
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id::text, name, slug, team_number, vulnbox_ip FROM game_teams WHERE id = $1`, teamID,
	).Scan(&info.TeamID, &info.Name, &info.Slug, &info.TeamNumber, &info.VulnboxIP)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if err != nil {
		h.logger.Error("arena/me: query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, info)
}

// statusServiceTick is one (service, tick) row in the /arena/me/status response.
type statusServiceTick struct {
	ServiceID   string  `json:"service_id"`
	ServiceName string  `json:"service_name"`
	Tick        int     `json:"tick"`
	Status      string  `json:"status"`
	Message     *string `json:"message"`
	LatencyMS   *int    `json:"latency_ms"`
}

// MeStatus serves GET /api/v1/arena/me/status?since=<tick>.
// Returns per-(service, tick) checker results for the authenticated team.
// Scoring fields (steal/hold/up, flag fates) are added once Finals-3 exports
// game.EffectiveSLA (v0.336.0); until then the shape is present but scoring
// fields are omitted.
func (h *GameHandler) MeStatus(c *gin.Context) {
	if h.off(c) {
		return
	}
	teamID, _, _, ok := h.teamIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "team not found; use X-Team-Token or a team JWT"})
		return
	}

	sinceTick := parseSince(c.Query("since"))

	// query the last 30 ticks per service (or since the requested tick)
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT ct.service_id::text, s.name, ct.tick_number,
		        COALESCE(ct.status, 'UNKNOWN') AS status,
		        ct.message, ct.latency_ms
		 FROM game_checker_tasks ct
		 JOIN game_services s ON s.id = ct.service_id
		 WHERE ct.team_id = $1 AND ct.tick_number > $2
		   AND ct.method = 'getflag'
		 ORDER BY s.name, ct.tick_number DESC`, teamID, sinceTick)
	if err != nil {
		h.logger.Error("arena/me/status: query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	var ticks []statusServiceTick
	for rows.Next() {
		var r statusServiceTick
		if err := rows.Scan(
			&r.ServiceID, &r.ServiceName, &r.Tick,
			&r.Status, &r.Message, &r.LatencyMS,
		); err != nil {
			h.logger.Error("arena/me/status: scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		ticks = append(ticks, r)
	}
	c.JSON(http.StatusOK, gin.H{"ticks": ticks})
}

// alertRow is one item in the /arena/me/alerts response.
type alertRow struct {
	ID        int64            `json:"id"`
	Tick      int              `json:"tick"`
	Kind      string           `json:"kind"`
	ServiceID *string          `json:"service_id"`
	Bucket    *int             `json:"bucket"`
	Detail    *json.RawMessage `json:"detail,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

// MeAlerts serves GET /api/v1/arena/me/alerts?since=<tick>.
// Returns bucketed exploitation alerts plus holding/shield-up events for the
// authenticated team. Published one tick late (see E8 alert generator).
func (h *GameHandler) MeAlerts(c *gin.Context) {
	if h.off(c) {
		return
	}
	teamID, _, _, ok := h.teamIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "team not found; use X-Team-Token or a team JWT"})
		return
	}

	sinceTick := parseSince(c.Query("since"))

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, tick_number, kind, service_id::text, bucket, detail, created_at
		 FROM game_team_alerts
		 WHERE team_id = $1 AND tick_number > $2
		 ORDER BY tick_number DESC, id DESC
		 LIMIT 200`, teamID, sinceTick)
	if err != nil {
		h.logger.Error("arena/me/alerts: query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	var alerts []alertRow
	for rows.Next() {
		var a alertRow
		if err := rows.Scan(
			&a.ID, &a.Tick, &a.Kind,
			&a.ServiceID, &a.Bucket, &a.Detail, &a.CreatedAt,
		); err != nil {
			h.logger.Error("arena/me/alerts: scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		alerts = append(alerts, a)
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

// MePcaps is a stub for GET /api/v1/arena/me/pcaps.
func (h *GameHandler) MePcaps(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "pcap listing not yet implemented",
		"hint":  "will return [{tick, url, size_bytes, expires_at}]",
	})
}

// MeVulnboxReboot is a stub for POST /api/v1/arena/me/vulnbox/reboot.
func (h *GameHandler) MeVulnboxReboot(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "vulnbox reboot not yet implemented",
		"hint":  "will trigger OCI instance RESET action for the caller's vulnbox",
	})
}

// MeVulnboxReset is a stub for POST /api/v1/arena/me/vulnbox/reset.
func (h *GameHandler) MeVulnboxReset(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "vulnbox reset not yet implemented",
		"hint":  "will trigger OCI boot-volume restore (rate-limited)",
	})
}

// MeVulnboxResetStatus is a stub for GET /api/v1/arena/me/vulnbox/reset.
func (h *GameHandler) MeVulnboxResetStatus(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "vulnbox reset status not yet implemented",
	})
}

// parseSince is a small helper for reading an optional ?since= query param.
func parseSince(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
