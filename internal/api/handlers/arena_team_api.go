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

// meTickRow is one tick entry inside a store row in /arena/me/status.
type meTickRow struct {
	Tick     int      `json:"tick"`
	Status   string   `json:"status"`  // player vocabulary
	Step     *string  `json:"step"`
	Code     *string  `json:"code"`
	Message  *string  `json:"message"`
	FlagFate *string  `json:"flag_fate"`
	Steal    float64  `json:"steal"`
	Hold     float64  `json:"hold"`
	Up       float64  `json:"up"`
}

// meStoreRow groups tick rows by flag store.
type meStoreRow struct {
	StoreIndex int         `json:"store_index"`
	StoreName  string      `json:"store_name"`
	Ticks      []meTickRow `json:"ticks"`
}

// mePointsHour is the per-service aggregate for the rolling window.
type mePointsHour struct {
	Steal float64 `json:"steal"`
	Hold  float64 `json:"hold"`
	Up    float64 `json:"up"`
}

// meServiceRow is one service in the /arena/me/status response.
type meServiceRow struct {
	Slug            string       `json:"slug"`
	Name            string       `json:"name"`
	Port            *int         `json:"port"`
	Stores          []meStoreRow `json:"stores"`
	PointsThisHour  mePointsHour `json:"points_this_hour"`
	ActiveAttackers int          `json:"active_attackers"`
}

// MeStatus serves GET /api/v1/arena/me/status?since=<tick>.
// Returns a hierarchical services→stores→ticks view of checker results and
// flag fates for the authenticated team.
func (h *GameHandler) MeStatus(c *gin.Context) {
	if h.off(c) {
		return
	}
	teamID, _, _, ok := h.teamIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "team not found; use X-Team-Token or a team JWT"})
		return
	}

	since := parseSince(c.Query("since"))
	ctx := c.Request.Context()

	// Q1: one row per (service, store_index, tick) = worst-status checker task
	type checkerRow struct {
		serviceID   string
		serviceName string
		serviceSlug string
		servicePort *int
		flagStores  int
		storeIndex  int
		tick        int
		status      *string
		method      string
		code        *string
		message     *string
	}
	cRows, err := h.db.Pool.Query(ctx, `
		SELECT DISTINCT ON (ct.service_id, ct.variant, ct.tick_number)
		    s.id::text, s.name, s.slug, s.port, s.flag_stores,
		    ct.variant, ct.tick_number,
		    ct.status, ct.method, ct.code, ct.message
		FROM game_checker_tasks ct
		JOIN game_services s ON s.id = ct.service_id
		WHERE ct.team_id = $1 AND ct.tick_number > $2 AND s.enabled = true
		ORDER BY ct.service_id, ct.variant, ct.tick_number DESC,
		    CASE COALESCE(ct.status, '')
		        WHEN 'DOWN'           THEN 0
		        WHEN 'FAULTY'         THEN 1
		        WHEN 'FLAG_NOT_FOUND' THEN 2
		        WHEN 'RECOVERING'     THEN 3
		        WHEN 'ERROR'          THEN 4
		        ELSE 5
		    END ASC`, teamID, since)
	if err != nil {
		h.logger.Error("arena/me/status: checker query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer cRows.Close()

	// build service map: serviceID → service info
	type svcKey struct{ svcID, slug string }
	type svcMeta struct {
		slug, name string
		port       *int
		flagStores int
		// storeIndex → []meTickRow
		stores map[int][]meTickRow
	}
	svcMap := map[string]*svcMeta{}
	svcOrder := []string{}

	for cRows.Next() {
		var r checkerRow
		if err := cRows.Scan(
			&r.serviceID, &r.serviceName, &r.serviceSlug, &r.servicePort, &r.flagStores,
			&r.storeIndex, &r.tick, &r.status, &r.method, &r.code, &r.message,
		); err != nil {
			h.logger.Error("arena/me/status: checker scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if _, exists := svcMap[r.serviceID]; !exists {
			svcMap[r.serviceID] = &svcMeta{
				slug: r.serviceSlug, name: r.serviceName,
				port: r.servicePort, flagStores: r.flagStores,
				stores: map[int][]meTickRow{},
			}
			svcOrder = append(svcOrder, r.serviceID)
		}
		rawStatus := ""
		if r.status != nil {
			rawStatus = *r.status
		}
		mapped := mapSLAStatus(rawStatus)
		var step *string
		if mapped != "OK" && mapped != "RECOVERING" && mapped != "ORG_FAULT" {
			step = &r.method
		}
		t := meTickRow{
			Tick:    r.tick,
			Status:  mapped,
			Step:    step,
			Code:    r.code,
			Message: r.message,
		}
		svcMap[r.serviceID].stores[r.storeIndex] = append(
			svcMap[r.serviceID].stores[r.storeIndex], t,
		)
	}
	if err := cRows.Err(); err != nil {
		h.logger.Error("arena/me/status: checker rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Q2: flag capture counts per (service, store, tick) for this team as victim
	type captureKey struct {
		serviceID  string
		storeIndex int
		tick       int
	}
	type captureVal struct{ attackers, nop int }
	captures := map[captureKey]captureVal{}

	capRows, err := h.db.Pool.Query(ctx, `
		SELECT gf.service_id::text, gf.store_index, gf.tick_number,
		    COUNT(DISTINCT gc.attacker_team_id) AS attackers,
		    COUNT(DISTINCT CASE WHEN gt.is_nop THEN gc.attacker_team_id END) AS nop_count
		FROM game_flags gf
		LEFT JOIN game_captures gc ON gc.flag_id = gf.id
		LEFT JOIN game_teams gt ON gt.id = gc.attacker_team_id
		WHERE gf.team_id = $1 AND gf.tick_number > $2
		GROUP BY gf.service_id, gf.store_index, gf.tick_number`, teamID, since)
	if err != nil {
		h.logger.Error("arena/me/status: capture query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer capRows.Close()
	for capRows.Next() {
		var key captureKey
		var val captureVal
		if err := capRows.Scan(&key.serviceID, &key.storeIndex, &key.tick,
			&val.attackers, &val.nop); err != nil {
			h.logger.Error("arena/me/status: capture scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		captures[key] = val
	}
	if err := capRows.Err(); err != nil {
		h.logger.Error("arena/me/status: capture rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Q3: per-service aggregate steal/hold/up from game_score_breakdown
	type brkKey string
	type brkVal struct{ steal, hold, up float64 }
	breakdown := map[brkKey]brkVal{}

	brkRows, err := h.db.Pool.Query(ctx, `
		SELECT service_id::text, SUM(steal), SUM(hold), SUM(up)
		FROM game_score_breakdown
		WHERE team_id = $1
		GROUP BY service_id`, teamID)
	if err != nil {
		h.logger.Error("arena/me/status: breakdown query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer brkRows.Close()
	for brkRows.Next() {
		var svcID string
		var v brkVal
		if err := brkRows.Scan(&svcID, &v.steal, &v.hold, &v.up); err != nil {
			h.logger.Error("arena/me/status: breakdown scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		breakdown[brkKey(svcID)] = v
	}
	if err := brkRows.Err(); err != nil {
		h.logger.Error("arena/me/status: breakdown rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Q4: active attackers per service (distinct attackers in last 5 closed ticks)
	activeAttackers := map[string]int{}
	atkRows, err := h.db.Pool.Query(ctx, `
		SELECT gc.service_id::text, COUNT(DISTINCT gc.attacker_team_id)
		FROM game_captures gc
		WHERE gc.victim_team_id = $1
		    AND gc.tick_number > (
		        SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks WHERE status = 'closed'
		    ) - 5
		GROUP BY gc.service_id`, teamID)
	if err != nil {
		h.logger.Error("arena/me/status: attackers query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer atkRows.Close()
	for atkRows.Next() {
		var svcID string
		var cnt int
		if err := atkRows.Scan(&svcID, &cnt); err != nil {
			h.logger.Error("arena/me/status: attackers scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		activeAttackers[svcID] = cnt
	}
	if err := atkRows.Err(); err != nil {
		h.logger.Error("arena/me/status: attackers rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// assemble response
	services := make([]meServiceRow, 0, len(svcOrder))
	for _, svcID := range svcOrder {
		meta := svcMap[svcID]
		brk := breakdown[brkKey(svcID)]
		row := meServiceRow{
			Slug: meta.slug,
			Name: meta.name,
			Port: meta.port,
			PointsThisHour: mePointsHour{
				Steal: brk.steal,
				Hold:  brk.hold,
				Up:    brk.up,
			},
			ActiveAttackers: activeAttackers[svcID],
		}

		// build stores in store_index order
		maxStore := meta.flagStores - 1
		for si, ticks := range meta.stores {
			if si > maxStore {
				maxStore = si
			}
			_ = ticks
		}
		for si := 0; si <= maxStore; si++ {
			ticks, _ := meta.stores[si]
			// wire flag_fate into each tick row
			for i, t := range ticks {
				ck := captureKey{serviceID: svcID, storeIndex: si, tick: t.Tick}
				cv := captures[ck]
				realAtk := cv.attackers - cv.nop
				ticks[i].FlagFate = flagFateFromCaptures(realAtk, cv.nop, t.Status)
			}
			store := meStoreRow{
				StoreIndex: si,
				StoreName:  storeName(si),
				Ticks:      ticks,
			}
			if store.Ticks == nil {
				store.Ticks = []meTickRow{}
			}
			row.Stores = append(row.Stores, store)
		}
		if row.Stores == nil {
			row.Stores = []meStoreRow{}
		}
		services = append(services, row)
	}

	c.JSON(http.StatusOK, gin.H{"services": services})
}

// storeName returns a human label for a flag store index.
func storeName(index int) string {
	switch index {
	case 0:
		return "flag 1"
	case 1:
		return "flag 2"
	case 2:
		return "flag 3"
	default:
		return "flag " + strconv.Itoa(index+1)
	}
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
