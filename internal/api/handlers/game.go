package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type GameHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger

	stateMu    sync.Mutex
	stateCache gameStateCacheEntry
	flightMu   sync.Mutex
	flight     *gameStateFlight
	stateLoad  func(context.Context) ([]byte, string, error)
}

type gameStateCacheEntry struct {
	body      []byte
	etag      string
	expiresAt time.Time
}

type gameStateFlight struct {
	done chan struct{}
	err  error
}

type gameStateQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const (
	arenaLiveCacheTTL     = 5 * time.Second
	arenaIdleCacheTTL     = 15 * time.Second
	arenaStaleTTL         = 15 * time.Second
	arenaQueryTimeout     = 5 * time.Second
	arenaHistoryTickLimit = 500
)

func NewGameHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *GameHandler {
	return &GameHandler{config: cfg, db: db, logger: logger}
}

func writeGameState(c *gin.Context, body []byte, etag string, ttl time.Duration, cacheStatus string) {
	seconds := max(0, int((ttl+time.Second-1)/time.Second))
	c.Header("Cache-Control", "public, max-age="+strconv.Itoa(seconds)+", stale-if-error=10")
	c.Header("ETag", etag)
	c.Header("X-Anvil-Cache", cacheStatus)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func writeStaleGameState(c *gin.Context, entry gameStateCacheEntry) {
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", entry.etag)
	c.Header("X-Anvil-Cache", "STALE")
	c.Data(http.StatusOK, "application/json; charset=utf-8", entry.body)
}

func encodeGameState(payload gin.H) ([]byte, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, fmt.Sprintf(`"%x"`, sum), nil
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

func (h *GameHandler) off(c *gin.Context) bool {
	if !h.config.Game.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "game not active"})
		return true
	}
	return false
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

func (h *GameHandler) standingsData(ctx context.Context, query gameStateQuerier) ([]gameStanding, error) {
	rows, err := query.Query(ctx,
		`SELECT t.id, t.name, s.attack, s.defense, s.sla, s.koth, s.total, s.rank
		 FROM game_standings s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false AND t.status = 'active'
		 ORDER BY s.rank ASC NULLS LAST, t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	standings := []gameStanding{}
	for rows.Next() {
		var e gameStanding
		var id uuid.UUID
		if err := rows.Scan(&id, &e.Team, &e.Attack, &e.Defense, &e.SLA, &e.Koth, &e.Total, &e.Rank); err != nil {
			return nil, err
		}
		e.TeamID = id.String()
		standings = append(standings, e)
	}
	return standings, rows.Err()
}

// Scoreboard returns the combined AD + KotH standings.
func (h *GameHandler) Scoreboard(c *gin.Context) {
	if h.off(c) {
		return
	}
	standings, err := h.standingsData(c.Request.Context(), h.db.Pool)
	if err != nil {
		h.logger.Error("scoreboard query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"standings": standings})
}

type gameHill struct {
	HillID     string  `json:"hill_id"`
	Name       string  `json:"name"`
	Controller *string `json:"controller,omitempty"`
}

func (h *GameHandler) hillsData(ctx context.Context, query gameStateQuerier) ([]gameHill, error) {
	rows, err := query.Query(ctx,
		`SELECT h.id, h.name, t.name
		 FROM game_koth_hills h
		 LEFT JOIN LATERAL (
		   SELECT controller_team_id FROM game_koth_control
		   WHERE hill_id = h.id ORDER BY tick_number DESC LIMIT 1
		 ) kc ON true
		 LEFT JOIN game_teams t ON t.id = kc.controller_team_id
		 WHERE h.enabled = true
		 ORDER BY h.sort_order, h.name, h.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hills := []gameHill{}
	for rows.Next() {
		var e gameHill
		var id uuid.UUID
		if err := rows.Scan(&id, &e.Name, &e.Controller); err != nil {
			return nil, err
		}
		e.HillID = id.String()
		hills = append(hills, e)
	}
	return hills, rows.Err()
}

// Hills returns each hill's current controller for the control-map.
func (h *GameHandler) Hills(c *gin.Context) {
	if h.off(c) {
		return
	}
	hills, err := h.hillsData(c.Request.Context(), h.db.Pool)
	if err != nil {
		h.logger.Error("hills query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hills": hills})
}

func (h *GameHandler) tickRound(ctx context.Context, query gameStateQuerier) (int, int, error) {
	var tick, round int
	if err := query.QueryRow(ctx, `SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&tick); err != nil {
		return 0, 0, err
	}
	if err := query.QueryRow(ctx, `SELECT COALESCE(MAX(round_number), 0) FROM game_koth_rounds`).Scan(&round); err != nil {
		return 0, 0, err
	}
	return tick, round, nil
}

func (h *GameHandler) statusPayload(ctx context.Context, query gameStateQuerier) (gin.H, error) {
	tick, round, err := h.tickRound(ctx, query)
	if err != nil {
		return nil, err
	}
	return gin.H{
		"tick":                  tick,
		"round":                 round,
		"tick_interval_seconds": int(h.config.Game.TickInterval.Seconds()),
	}, nil
}

// Status returns the current tick and KotH round.
func (h *GameHandler) Status(c *gin.Context) {
	if h.off(c) {
		return
	}
	payload, err := h.statusPayload(c.Request.Context(), h.db.Pool)
	if err != nil {
		h.logger.Error("status query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, payload)
}

type historyPoint struct {
	Tick  int     `json:"x"`
	Total float64 `json:"y"`
}

type historySeries struct {
	TeamID string         `json:"team_id"`
	Team   string         `json:"team"`
	Points []historyPoint `json:"points"`
}

func (h *GameHandler) historyData(ctx context.Context, query gameStateQuerier) ([]*historySeries, error) {
	rows, err := query.Query(ctx,
		`SELECT t.id, t.name, s.tick_number, s.total
		 FROM game_score_snapshots s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false AND t.status = 'active'
		   AND s.tick_number > (SELECT COALESCE(MAX(tick_number), 0) - $1 FROM game_score_snapshots)
		 ORDER BY t.name, t.id, s.tick_number`, arenaHistoryTickLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := []string{}
	byTeam := map[string]*historySeries{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var tick int
		var total float64
		if err := rows.Scan(&id, &name, &tick, &total); err != nil {
			return nil, err
		}
		key := id.String()
		s := byTeam[key]
		if s == nil {
			s = &historySeries{TeamID: key, Team: name}
			byTeam[key] = s
			order = append(order, key)
		}
		s.Points = append(s.Points, historyPoint{Tick: tick, Total: total})
	}

	series := make([]*historySeries, 0, len(order))
	for _, k := range order {
		series = append(series, byTeam[k])
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return series, nil
}

// History returns each team's score over time for the race chart.
func (h *GameHandler) History(c *gin.Context) {
	if h.off(c) {
		return
	}
	series, err := h.historyData(c.Request.Context(), h.db.Pool)
	if err != nil {
		h.logger.Error("history query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"series": series})
}

type matrixService struct {
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Tier      string `json:"tier"`
}

type matrixCell struct {
	Status    string `json:"status"`
	LatencyMs *int   `json:"latency_ms,omitempty"`
}

type matrixRow struct {
	TeamID string       `json:"team_id"`
	Team   string       `json:"team"`
	Rank   *int         `json:"rank,omitempty"`
	Cells  []matrixCell `json:"cells"`
}

func (h *GameHandler) matrixData(ctx context.Context, query gameStateQuerier) ([]matrixService, []matrixRow, error) {
	svcRows, err := query.Query(ctx,
		`SELECT id, name, category, tier FROM game_services WHERE enabled = true ORDER BY sort_order, name, id`)
	if err != nil {
		return nil, nil, err
	}
	services := []matrixService{}
	for svcRows.Next() {
		var s matrixService
		var id uuid.UUID
		if err := svcRows.Scan(&id, &s.Name, &s.Category, &s.Tier); err != nil {
			svcRows.Close()
			return nil, nil, err
		}
		s.ServiceID = id.String()
		services = append(services, s)
	}
	if err := svcRows.Err(); err != nil {
		svcRows.Close()
		return nil, nil, err
	}
	svcRows.Close()

	slaRows, err := query.Query(ctx,
		`SELECT DISTINCT ON (team_id, service_id) team_id, service_id, status, latency_ms
		 FROM game_sla_checks ORDER BY team_id, service_id, tick_number DESC`)
	if err != nil {
		return nil, nil, err
	}
	latest := map[string]matrixCell{}
	for slaRows.Next() {
		var team, svc uuid.UUID
		var cell matrixCell
		if err := slaRows.Scan(&team, &svc, &cell.Status, &cell.LatencyMs); err != nil {
			slaRows.Close()
			return nil, nil, err
		}
		latest[team.String()+"|"+svc.String()] = cell
	}
	if err := slaRows.Err(); err != nil {
		slaRows.Close()
		return nil, nil, err
	}
	slaRows.Close()

	teamRows, err := query.Query(ctx,
		`SELECT t.id, t.name, s.rank FROM game_teams t
		 JOIN game_standings s ON s.team_id = t.id
		 WHERE t.is_nop = false AND t.status = 'active' ORDER BY s.rank ASC NULLS LAST, t.id`)
	if err != nil {
		return nil, nil, err
	}
	defer teamRows.Close()

	rows := []matrixRow{}
	for teamRows.Next() {
		var r matrixRow
		var id uuid.UUID
		if err := teamRows.Scan(&id, &r.Team, &r.Rank); err != nil {
			return nil, nil, err
		}
		r.TeamID = id.String()
		r.Cells = make([]matrixCell, len(services))
		for i, s := range services {
			if cell, ok := latest[r.TeamID+"|"+s.ServiceID]; ok {
				r.Cells[i] = cell
			} else {
				r.Cells[i] = matrixCell{Status: "UNKNOWN"}
			}
		}
		rows = append(rows, r)
	}
	return services, rows, teamRows.Err()
}

// Services returns the teams x services SLA matrix, rows ordered by rank.
func (h *GameHandler) Services(c *gin.Context) {
	if h.off(c) {
		return
	}
	services, rows, err := h.matrixData(c.Request.Context(), h.db.Pool)
	if err != nil {
		h.logger.Error("matrix query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"services": services, "rows": rows})
}

type gameEvent struct {
	ID       string `json:"id"`
	Tick     int    `json:"tick"`
	Attacker string `json:"attacker"`
	Victim   string `json:"victim"`
	Service  string `json:"service"`
	At       int64  `json:"at"`
}

func (h *GameHandler) eventsData(ctx context.Context, query gameStateQuerier) ([]gameEvent, error) {
	rows, err := query.Query(ctx,
		`SELECT cp.id, cp.tick_number, a.name, v.name, s.name, cp.submitted_at
		 FROM game_captures cp
		 JOIN game_teams a ON a.id = cp.attacker_team_id
		 JOIN game_teams v ON v.id = cp.victim_team_id
		 JOIN game_services s ON s.id = cp.service_id
		 ORDER BY cp.submitted_at DESC, cp.id DESC LIMIT 40`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []gameEvent{}
	for rows.Next() {
		var e gameEvent
		var id uuid.UUID
		var at time.Time
		if err := rows.Scan(&id, &e.Tick, &e.Attacker, &e.Victim, &e.Service, &at); err != nil {
			return nil, err
		}
		e.ID = id.String()
		e.At = at.Unix()
		events = append(events, e)
	}
	return events, rows.Err()
}

// Events returns the most recent flag captures for the live event feed.
func (h *GameHandler) Events(c *gin.Context) {
	if h.off(c) {
		return
	}
	events, err := h.eventsData(c.Request.Context(), h.db.Pool)
	if err != nil {
		h.logger.Error("events query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *GameHandler) cachedState(now time.Time, allowExpired bool) (gameStateCacheEntry, bool) {
	h.stateMu.Lock()
	entry := h.stateCache
	h.stateMu.Unlock()
	if len(entry.body) == 0 || (!allowExpired && !now.Before(entry.expiresAt)) ||
		(allowExpired && !now.Before(entry.expiresAt.Add(arenaStaleTTL))) {
		return gameStateCacheEntry{}, false
	}
	return entry, true
}

func (h *GameHandler) serveStaleState(c *gin.Context) bool {
	entry, ok := h.cachedState(time.Now(), true)
	if !ok {
		return false
	}
	writeStaleGameState(c, entry)
	return true
}

func respondGameStateError(c *gin.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "arena state request timed out"})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to load arena state"})
}

func (h *GameHandler) serveCachedState(c *gin.Context) bool {
	now := time.Now()
	entry, ok := h.cachedState(now, false)
	if !ok {
		return false
	}
	writeGameState(c, entry.body, entry.etag, entry.expiresAt.Sub(now), "HIT")
	return true
}

// beginStateFill makes one request refresh the snapshot while concurrent
// viewers wait on a context-aware channel instead of a response-wide mutex.
func (h *GameHandler) beginStateFill(c *gin.Context) bool {
	for {
		h.flightMu.Lock()
		if h.flight == nil {
			h.flight = &gameStateFlight{done: make(chan struct{})}
			h.flightMu.Unlock()
			return true
		}
		flight := h.flight
		done := flight.done
		h.flightMu.Unlock()

		select {
		case <-done:
			if h.serveCachedState(c) {
				return false
			}
			if h.serveStaleState(c) {
				return false
			}
			if flight.err != nil {
				respondGameStateError(c, flight.err)
				return false
			}
		case <-c.Request.Context().Done():
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "arena state request timed out"})
			return false
		}
	}
}

func (h *GameHandler) finishStateFill(err error) {
	h.flightMu.Lock()
	flight := h.flight
	if flight != nil {
		flight.err = err
		close(flight.done)
	}
	h.flight = nil
	h.flightMu.Unlock()
}

func (h *GameHandler) loadLiveState(ctx context.Context) ([]byte, string, error) {
	if h.stateLoad != nil {
		return h.stateLoad(ctx)
	}
	tx, err := h.db.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, "", fmt.Errorf("begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	standings, err := h.standingsData(ctx, tx)
	if err != nil {
		return nil, "", fmt.Errorf("standings: %w", err)
	}
	hills, err := h.hillsData(ctx, tx)
	if err != nil {
		return nil, "", fmt.Errorf("hills: %w", err)
	}
	history, err := h.historyData(ctx, tx)
	if err != nil {
		return nil, "", fmt.Errorf("history: %w", err)
	}
	services, rows, err := h.matrixData(ctx, tx)
	if err != nil {
		return nil, "", fmt.Errorf("matrix: %w", err)
	}
	events, err := h.eventsData(ctx, tx)
	if err != nil {
		return nil, "", fmt.Errorf("events: %w", err)
	}
	status, err := h.statusPayload(ctx, tx)
	if err != nil {
		return nil, "", fmt.Errorf("status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit snapshot: %w", err)
	}

	return encodeGameState(gin.H{
		"active":    true,
		"status":    status,
		"hills":     hills,
		"standings": standings,
		"services":  services,
		"rows":      rows,
		"events":    events,
		"history":   history,
	})
}

// State returns the whole arena snapshot in one response, so each viewer polls
// a single endpoint instead of fanning out across six.
func (h *GameHandler) State(c *gin.Context) {
	if !h.config.Game.Enabled {
		body, etag, err := encodeGameState(gin.H{
			"active":    false,
			"status":    gin.H{"tick": 0, "round": 0, "tick_interval_seconds": int(h.config.Game.TickInterval.Seconds())},
			"hills":     []gameHill{},
			"standings": []gameStanding{},
			"services":  []matrixService{},
			"rows":      []matrixRow{},
			"events":    []gameEvent{},
			"history":   []*historySeries{},
		})
		if err != nil {
			h.logger.Error("encode inactive game state", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		writeGameState(c, body, etag, arenaIdleCacheTTL, "IDLE")
		return
	}
	if h.serveCachedState(c) || !h.beginStateFill(c) {
		return
	}
	fillOpen := true
	defer func() {
		if fillOpen {
			h.finishStateFill(errors.New("arena state fill interrupted"))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), arenaQueryTimeout)
	defer cancel()
	body, etag, err := h.loadLiveState(ctx)
	if err != nil {
		h.logger.Error("load arena state", zap.Error(err))
		h.finishStateFill(err)
		fillOpen = false
		if h.serveStaleState(c) {
			return
		}
		respondGameStateError(c, err)
		return
	}

	entry := gameStateCacheEntry{body: body, etag: etag, expiresAt: time.Now().Add(arenaLiveCacheTTL)}
	h.stateMu.Lock()
	h.stateCache = entry
	h.stateMu.Unlock()
	h.finishStateFill(nil)
	fillOpen = false
	writeGameState(c, body, etag, arenaLiveCacheTTL, "MISS")
}
