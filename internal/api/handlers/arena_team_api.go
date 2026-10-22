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
// Returns the current tick number, its start/end/hold timestamps, and the game phase.
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

	c.JSON(http.StatusOK, h.tickResponse(tick, startedAt, phase))
}

type arenaTickResponse struct {
	Tick      int       `json:"tick"`
	StartedAt time.Time `json:"started_at"`
	EndsAt    time.Time `json:"ends_at"`
	HoldFrom  time.Time `json:"hold_from"`
	Phase     string    `json:"phase"`
}

func (h *GameHandler) tickResponse(tick int, startedAt time.Time, phase game.Phase) arenaTickResponse {
	endsAt := startedAt.Add(h.config.Game.TickInterval)
	return arenaTickResponse{
		Tick:      tick,
		StartedAt: startedAt,
		EndsAt:    endsAt,
		HoldFrom:  endsAt.Add(-h.config.Game.Checker.TailMargin),
		Phase:     string(phase),
	}
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
	Tick           int     `json:"tick"`
	Status         string  `json:"status"` // player vocabulary
	CreditedReason *string `json:"credited_reason,omitempty"`
	Step           *string `json:"step,omitempty"`
	Code           *string `json:"code,omitempty"`
	Message        *string `json:"message,omitempty"`
	FlagFate       *string `json:"flag_fate,omitempty"`
}

// meStoreRow groups tick rows by flag store with per-store point totals.
type meStoreRow struct {
	StoreIndex int         `json:"store_index"`
	StoreName  string      `json:"store_name"`
	Steal      float64     `json:"steal"`
	Hold       float64     `json:"hold"`
	Up         float64     `json:"up"`
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
	ServiceID       string       `json:"service_id"`
	Slug            string       `json:"slug"`
	Name            string       `json:"name"`
	Port            *int         `json:"port"`
	Stores          []meStoreRow `json:"stores"`
	PointsThisHour  mePointsHour `json:"points_this_hour"`
	ActiveAttackers int          `json:"active_attackers"`
}

// MeStatus serves GET /api/v1/arena/me/status?since=<tick>.
// Returns a hierarchical services→stores→ticks view of effective SLA and
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

	// Q1: effective SLA per (service, tick) via the same mirror-credit logic
	slaTicks, err := game.EffectiveSLA(ctx, h.db, teamID, since, h.config.Game.Scoring.MassFaultTeams)
	if err != nil {
		h.logger.Error("arena/me/status: EffectiveSLA", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// index: (serviceID, tick) → SLATick; collect ordered service IDs
	type slaKey struct {
		svcID uuid.UUID
		tick  int
	}
	slaByKey := map[slaKey]game.SLATick{}
	svcIDSet := map[uuid.UUID]bool{}
	var svcOrder []uuid.UUID
	for _, t := range slaTicks {
		slaByKey[slaKey{t.ServiceID, t.Tick}] = t
		if !svcIDSet[t.ServiceID] {
			svcIDSet[t.ServiceID] = true
			svcOrder = append(svcOrder, t.ServiceID)
		}
	}

	// load service metadata for the services that have sla records
	type svcMeta struct {
		id         uuid.UUID
		slug, name string
		port       *int
		flagStores int
	}
	svcMetas := map[uuid.UUID]svcMeta{}
	if len(svcOrder) > 0 {
		mRows, err := h.db.Pool.Query(ctx,
			`SELECT id, name, slug, port, flag_stores
			 FROM game_services WHERE id = ANY($1) AND enabled = true`, svcOrder)
		if err != nil {
			h.logger.Error("arena/me/status: service meta", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		defer mRows.Close()
		for mRows.Next() {
			var m svcMeta
			if err := mRows.Scan(&m.id, &m.name, &m.slug, &m.port, &m.flagStores); err != nil {
				h.logger.Error("arena/me/status: service meta scan", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			svcMetas[m.id] = m
		}
		if err := mRows.Err(); err != nil {
			h.logger.Error("arena/me/status: service meta rows", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
	}

	// build per-service tick lists from sla records
	// storesByService: serviceID → storeIndex → []meTickRow (status only, fate added later)
	type storeKey struct {
		svcID      uuid.UUID
		storeIndex int
	}
	storeTicks := map[storeKey][]meTickRow{}
	for _, t := range slaTicks {
		meta, ok := svcMetas[t.ServiceID]
		if !ok {
			continue // service disabled or not found
		}
		mapped := mapSLAStatus(string(t.Effective))
		var credited *string
		if t.Credited != "" {
			s := t.Credited
			credited = &s
		}
		var step, code, message *string
		if t.Step != "" {
			s := t.Step
			step = &s
		}
		if t.Code != "" {
			s := t.Code
			code = &s
		}
		if t.Message != "" {
			s := t.Message
			message = &s
		}
		tr := meTickRow{
			Tick:           t.Tick,
			Status:         mapped,
			CreditedReason: credited,
			Step:           step,
			Code:           code,
			Message:        message,
		}
		// duplicate tick row across all stores for this service
		for si := 0; si < meta.flagStores; si++ {
			k := storeKey{t.ServiceID, si}
			storeTicks[k] = append(storeTicks[k], tr)
		}
	}

	// Q2: flag capture counts per (service, store, tick) for this team as victim
	type captureKey struct {
		svcID      uuid.UUID
		storeIndex int
		tick       int
	}
	type captureVal struct{ attackers, nop int }
	captures := map[captureKey]captureVal{}

	capRows, err := h.db.Pool.Query(ctx, `
		SELECT gf.service_id, gf.store_index, gf.tick_number,
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
		if err := capRows.Scan(&key.svcID, &key.storeIndex, &key.tick,
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

	// Q3: per-store totals from game_score_breakdown
	type brkKey = storeKey
	type brkVal struct{ steal, hold, up float64 }
	storeBreakdown := map[brkKey]brkVal{}

	brkRows, err := h.db.Pool.Query(ctx,
		`SELECT service_id, store_index, steal, hold, up
		 FROM game_score_breakdown WHERE team_id = $1`, teamID)
	if err != nil {
		h.logger.Error("arena/me/status: breakdown query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer brkRows.Close()
	for brkRows.Next() {
		var k brkKey
		var v brkVal
		if err := brkRows.Scan(&k.svcID, &k.storeIndex, &v.steal, &v.hold, &v.up); err != nil {
			h.logger.Error("arena/me/status: breakdown scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		storeBreakdown[k] = v
	}
	if err := brkRows.Err(); err != nil {
		h.logger.Error("arena/me/status: breakdown rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Q4: active attackers per service (distinct attackers in last 5 closed ticks)
	activeAttackers := map[uuid.UUID]int{}
	atkRows, err := h.db.Pool.Query(ctx, `
		SELECT gc.service_id, COUNT(DISTINCT gc.attacker_team_id)
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
		var svcID uuid.UUID
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
		meta, ok := svcMetas[svcID]
		if !ok {
			continue
		}
		// service-level points = sum over stores
		var totalSteal, totalHold, totalUp float64
		for si := 0; si < meta.flagStores; si++ {
			b := storeBreakdown[storeKey{svcID, si}]
			totalSteal += b.steal
			totalHold += b.hold
			totalUp += b.up
		}
		row := meServiceRow{
			ServiceID: svcID.String(),
			Slug:      meta.slug,
			Name:      meta.name,
			Port:      meta.port,
			PointsThisHour: mePointsHour{
				Steal: totalSteal,
				Hold:  totalHold,
				Up:    totalUp,
			},
			ActiveAttackers: activeAttackers[svcID],
		}

		for si := 0; si < meta.flagStores; si++ {
			k := storeKey{svcID, si}
			ticks := storeTicks[k]
			// wire flag_fate into each tick row
			for i, t := range ticks {
				ck := captureKey{svcID: svcID, storeIndex: si, tick: t.Tick}
				cv := captures[ck]
				realAtk := cv.attackers - cv.nop
				ticks[i].FlagFate = flagFateFromCaptures(realAtk, cv.nop, t.Status)
			}
			brk := storeBreakdown[k]
			store := meStoreRow{
				StoreIndex: si,
				StoreName:  storeName(si),
				Steal:      brk.steal,
				Hold:       brk.hold,
				Up:         brk.up,
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
