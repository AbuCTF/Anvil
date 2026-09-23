package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	maxScoreboardCacheEntries  = 128
	scoreboardQueryTimeout     = 5 * time.Second
	scoreboardPublicContextKey = "scoreboard_public"
	scoreboardPrivateCacheKey  = "scoreboard_cache_private"
)

type scoreboardCacheEntry struct {
	body      []byte
	etag      string
	expiresAt time.Time
}

type scoreboardCacheFlight struct {
	done    chan struct{}
	waiters int
}

type scoreboardQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (h *ScoreboardHandler) beginReadSnapshot(c *gin.Context) (pgx.Tx, bool) {
	tx, err := h.db.Pool.BeginTx(c.Request.Context(), pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		h.respondQueryError(c, "failed to begin scoreboard snapshot", err)
		return nil, false
	}
	return tx, true
}

func (h *ScoreboardHandler) respondQueryError(c *gin.Context, message string, err error) {
	h.logger.Error(message, zap.Error(err))
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "scoreboard request timed out"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch scoreboard data"})
}

type ScoreboardService struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewScoreboardService(cfg *config.Config, db *database.DB, logger *zap.Logger) *ScoreboardService {
	return &ScoreboardService{config: cfg, db: db, logger: logger}
}

func (h *ScoreboardHandler) scoreboardAvailable(c *gin.Context) bool {
	now := time.Now()
	h.cacheMu.Lock()
	if now.Before(h.availabilityUntil) {
		enabled := h.availability
		isPublic := h.scoreboardPublic
		h.cacheMu.Unlock()
		return h.allowScoreboardRequest(c, enabled, isPublic)
	}
	h.cacheMu.Unlock()
	h.availabilityMu.Lock()
	defer h.availabilityMu.Unlock()

	// another request may have refreshed the setting while this one waited
	now = time.Now()
	h.cacheMu.Lock()
	if now.Before(h.availabilityUntil) {
		enabled := h.availability
		isPublic := h.scoreboardPublic
		h.cacheMu.Unlock()
		return h.allowScoreboardRequest(c, enabled, isPublic)
	}
	h.cacheMu.Unlock()

	enabled := h.config.Platform.ScoreboardEnabled
	isPublic := h.config.Platform.ScoreboardPublic
	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT
			COALESCE((SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'scoreboard_enabled'), $1),
			COALESCE((SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'scoreboard_public'), $2)
	`, enabled, isPublic).Scan(&enabled, &isPublic)
	if err != nil {
		h.logger.Error("failed to read scoreboard setting", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read scoreboard availability"})
		return false
	}
	h.cacheMu.Lock()
	settingsChanged := !h.availabilityUntil.IsZero() &&
		(h.availability != enabled || h.scoreboardPublic != isPublic)
	if settingsChanged {
		clear(h.cache)
	}
	h.availability = enabled
	h.scoreboardPublic = isPublic
	h.availabilityUntil = time.Now().Add(time.Second)
	h.cacheMu.Unlock()
	return h.allowScoreboardRequest(c, enabled, isPublic)
}

func (h *ScoreboardHandler) allowScoreboardRequest(c *gin.Context, enabled, isPublic bool) bool {
	if !enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scoreboard is disabled"})
		return false
	}
	c.Set(scoreboardPublicContextKey, isPublic)
	if !isPublic && !scoreboardRequestAuthenticated(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return false
	}
	return true
}

func scoreboardRequestAuthenticated(c *gin.Context) bool {
	for _, key := range []string{"user_id", "session_id"} {
		if value, ok := c.Get(key); ok {
			if id, valid := value.(uuid.UUID); valid && id != uuid.Nil {
				return true
			}
		}
	}
	return false
}

func limitScoreboardRequest(c *gin.Context) context.CancelFunc {
	ctx, cancel := context.WithTimeout(c.Request.Context(), scoreboardQueryTimeout)
	c.Request = c.Request.WithContext(ctx)
	return cancel
}

func scoreboardCacheControl(c *gin.Context, maxAge time.Duration) string {
	scope := "private"
	if c.GetBool(scoreboardPublicContextKey) && !c.GetBool(scoreboardPrivateCacheKey) {
		scope = "public"
	}
	seconds := max(0, int(maxAge/time.Second))
	return scope + ", max-age=" + strconv.Itoa(seconds)
}

func (h *ScoreboardHandler) serveCachedJSON(c *gin.Context, key string) bool {
	now := time.Now()
	h.cacheMu.Lock()
	entry, ok := h.cache[key]
	if ok && !now.Before(entry.expiresAt) {
		delete(h.cache, key)
		ok = false
	}
	h.cacheMu.Unlock()
	if !ok {
		return false
	}
	c.Header("Cache-Control", scoreboardCacheControl(c, entry.expiresAt.Sub(now)))
	c.Header("ETag", entry.etag)
	c.Header("Vary", "Authorization")
	c.Header("X-Anvil-Cache", "HIT")
	if c.GetHeader("If-None-Match") == entry.etag {
		c.Status(http.StatusNotModified)
		return true
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", entry.body)
	return true
}

// coalesces concurrent misses for the same public payload; a waiter either consumes the freshly cached response or becomes the sole next loader if the previous query failed
func (h *ScoreboardHandler) beginCacheFill(c *gin.Context, key string) bool {
	for {
		h.flightMu.Lock()
		if h.flights == nil {
			h.flights = make(map[string]*scoreboardCacheFlight)
		}
		if flight, ok := h.flights[key]; ok {
			flight.waiters++
			done := flight.done
			h.flightMu.Unlock()
			select {
			case <-done:
				if h.serveCachedJSON(c, key) {
					return false
				}
				continue
			case <-c.Request.Context().Done():
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": "scoreboard request timed out"})
				return false
			}
		}
		h.flights[key] = &scoreboardCacheFlight{done: make(chan struct{})}
		h.flightMu.Unlock()
		return true
	}
}

func (h *ScoreboardHandler) finishCacheFill(key string) {
	h.flightMu.Lock()
	if flight, ok := h.flights[key]; ok {
		delete(h.flights, key)
		close(flight.done)
	}
	h.flightMu.Unlock()
}

func (h *ScoreboardHandler) respondCacheableJSON(
	c *gin.Context,
	key string,
	maxAge time.Duration,
	payload any,
) {
	body, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("failed to encode scoreboard response", zap.String("cache_key", key), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode response"})
		return
	}

	now := time.Now()
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(body))
	h.cacheMu.Lock()
	if h.cache == nil {
		h.cache = make(map[string]scoreboardCacheEntry)
	}
	for cachedKey, entry := range h.cache {
		if !now.Before(entry.expiresAt) {
			delete(h.cache, cachedKey)
		}
	}
	if len(h.cache) < maxScoreboardCacheEntries {
		h.cache[key] = scoreboardCacheEntry{
			body:      body,
			etag:      etag,
			expiresAt: now.Add(maxAge),
		}
	}
	h.cacheMu.Unlock()

	c.Header("Cache-Control", scoreboardCacheControl(c, maxAge))
	c.Header("ETag", etag)
	c.Header("Vary", "Authorization")
	c.Header("X-Anvil-Cache", "MISS")
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

type ScoreboardEntry struct {
	Rank             int     `json:"rank"`
	UserID           string  `json:"user_id"`
	Username         string  `json:"username"`
	DisplayName      *string `json:"display_name,omitempty"`
	TotalScore       int     `json:"total_score"`
	ChallengesSolved int     `json:"challenges_solved"`
	FlagsSolved      int     `json:"flags_solved"`
	LastSolveAt      *string `json:"last_solve_at,omitempty"`
	Country          *string `json:"country,omitempty"`
	Delta            int     `json:"delta"`
	Spark            []int   `json:"spark,omitempty"`
}

// teamScoreboardQuery ranks teams (teams mode) with the same column shape, params ($1 limit, $2 offset, $3 search, $4 sort), and scan order as the user query: id, name (as username), display_name (null), total_score, challenges_solved (fully-completed), flags_solved (distinct flags), last_solve, rank
const teamScoreboardQuery = `
	WITH team_solves AS (
		SELECT DISTINCT u.team_id AS team_id, s.flag_id, f.challenge_id
		FROM solves s
		JOIN users u ON u.id = s.user_id
		JOIN flags f ON f.id = s.flag_id
		WHERE u.team_id IS NOT NULL
	), flag_totals AS (
		SELECT f.challenge_id, COUNT(*)::int AS total_flags
		FROM flags f
		JOIN challenges c ON c.id = f.challenge_id
		WHERE c.status = 'published' AND (c.release_date IS NULL OR c.release_date <= NOW())
		GROUP BY f.challenge_id
	), team_flags AS (
		SELECT team_id, COUNT(*)::int AS flags_solved
		FROM team_solves GROUP BY team_id
	), team_completed AS (
		SELECT team_id, COUNT(*)::int AS challenges_solved FROM (
			SELECT ts.team_id, ts.challenge_id
			FROM team_solves ts
			JOIN flag_totals ft ON ft.challenge_id = ts.challenge_id
			GROUP BY ts.team_id, ts.challenge_id, ft.total_flags
			HAVING COUNT(DISTINCT ts.flag_id) >= ft.total_flags
		) fully GROUP BY team_id
	), last_solves AS (
		SELECT u.team_id, MAX(s.solved_at) AS last_solve
		FROM solves s JOIN users u ON u.id = s.user_id
		WHERE u.team_id IS NOT NULL GROUP BY u.team_id
	), ranked AS (
		-- unified board: jeopardy total_score + capped KotH hold-time (koth_score is
		-- 0 unless the shared-arena engine is active, so this is a no-op otherwise)
		SELECT t.id, t.name, (t.total_score + t.koth_score)::int AS total_score, ls.last_solve,
			ROW_NUMBER() OVER (
				ORDER BY (t.total_score + t.koth_score) DESC, ls.last_solve ASC NULLS LAST,
				         t.created_at ASC, t.id ASC
			) AS rank
		FROM teams t
		LEFT JOIN last_solves ls ON ls.team_id = t.id
	), page_teams AS (
		SELECT * FROM ranked
		WHERE $3 = '' OR name ILIKE '%' || $3 || '%' ESCAPE '\'
		ORDER BY CASE WHEN $4 = 'name' THEN LOWER(name) END, rank
		LIMIT $1 OFFSET $2
	)
	SELECT pt.id, pt.name, NULL::text, pt.total_score,
		COALESCE(tc.challenges_solved, 0),
		COALESCE(tf.flags_solved, 0),
		pt.last_solve, pt.rank
	FROM page_teams pt
	LEFT JOIN team_completed tc ON tc.team_id = pt.id
	LEFT JOIN team_flags tf ON tf.team_id = pt.id
	ORDER BY CASE WHEN $4 = 'name' THEN LOWER(pt.name) END, pt.rank
`

// teamEconomyScoreboardQuery ranks teams by their economy point total (economy mode); same column/scan shape as the user + team queries; challenges/flags "solved" = challenges the team holds under the economy
const teamEconomyScoreboardQuery = `
	WITH scores AS (
		SELECT t.id, t.name,
			COALESCE(ets.points, 0) AS points,
			(SELECT MAX(s.solved_at) FROM solves s JOIN users u ON u.id = s.user_id WHERE u.team_id = t.id) AS last_solve,
			(SELECT COUNT(*)::int FROM economy_challenge_state e WHERE e.team_id = t.id AND e.holds_solve) AS solved
		FROM teams t
		LEFT JOIN economy_team_score ets ON ets.team_id = t.id
	), ranked AS (
		SELECT id, name, points, last_solve, solved,
			ROW_NUMBER() OVER (
				ORDER BY points DESC, last_solve ASC NULLS LAST, name ASC
			) AS rank
		FROM scores
	), page_teams AS (
		SELECT * FROM ranked
		WHERE $3 = '' OR name ILIKE '%' || $3 || '%' ESCAPE '\'
		ORDER BY CASE WHEN $4 = 'name' THEN LOWER(name) END, rank
		LIMIT $1 OFFSET $2
	)
	SELECT pt.id, pt.name, NULL::text, ROUND(pt.points)::int, pt.solved, pt.solved, pt.last_solve, pt.rank
	FROM page_teams pt
	ORDER BY CASE WHEN $4 = 'name' THEN LOWER(pt.name) END, pt.rank
`

func (h *ScoreboardHandler) Get(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}

	page := 1
	if raw := c.Query("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 1_000_000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page must be between 1 and 1000000"})
			return
		}
		page = v
	}
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		limit = v
	}
	if limit > 500 {
		limit = 500
	}
	queryText := strings.TrimSpace(c.Query("q"))
	if len(queryText) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q must be at most 50 characters"})
		return
	}
	sortBy := c.DefaultQuery("sort", "rank")
	if sortBy != "rank" && sortBy != "name" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort must be rank or name"})
		return
	}
	offset := (page - 1) * limit
	queryPattern := escapeScoreboardSearch(queryText)
	cacheKey := "scoreboard:" + strconv.Itoa(page) + ":" + strconv.Itoa(limit) + ":" + sortBy + ":" + queryText
	if h.serveCachedJSON(c, cacheKey) {
		return
	}
	if !h.beginCacheFill(c, cacheKey) {
		return
	}
	defer h.finishCacheFill(cacheKey)
	tx, ok := h.beginReadSnapshot(c)
	if !ok {
		return
	}
	defer tx.Rollback(c.Request.Context())

	// teams mode ranks teams instead of users; the team path is fully isolated (separate count + query, trends skipped) so the user path is untouched
	teamsMode, tmErr := isTeamsMode(c.Request.Context(), h.db)
	if tmErr != nil {
		h.respondQueryError(c, "failed to read teams_mode", tmErr)
		return
	}
	economyMode, emErr := isEconomyMode(c.Request.Context(), h.db)
	if emErr != nil {
		h.respondQueryError(c, "failed to read economy_mode", emErr)
		return
	}
	teamRanked := teamsMode || economyMode // both rank teams, not users

	countQuery := `SELECT COUNT(*), COUNT(*) FILTER (
			 WHERE $1 = '' OR username ILIKE '%' || $1 || '%' ESCAPE '\'
			    OR COALESCE(display_name, '') ILIKE '%' || $1 || '%' ESCAPE '\'
		 )
		 FROM users WHERE role != 'admin' AND status = 'active'`
	if teamRanked {
		countQuery = `SELECT COUNT(*), COUNT(*) FILTER (
			 WHERE $1 = '' OR name ILIKE '%' || $1 || '%' ESCAPE '\'
		 ) FROM teams`
	}
	var totalUsers, matchingUsers int
	if err := tx.QueryRow(c.Request.Context(), countQuery, queryPattern).Scan(&totalUsers, &matchingUsers); err != nil {
		h.respondQueryError(c, "failed to count scoreboard rows", err)
		return
	}
	if offset >= matchingUsers {
		if err := tx.Commit(c.Request.Context()); err != nil {
			h.respondQueryError(c, "failed to commit scoreboard snapshot", err)
			return
		}
		h.respondCacheableJSON(c, cacheKey, 2*time.Second, gin.H{
			"leaderboard":    []ScoreboardEntry{},
			"total_users":    totalUsers,
			"matching_users": matchingUsers,
			"page":           page,
			"limit":          limit,
		})
		return
	}

	query := `
		WITH ranked AS MATERIALIZED (
			SELECT u.id, u.username, u.display_name, u.total_score, u.created_at,
			       ls.last_solve,
			       ROW_NUMBER() OVER (
			           ORDER BY u.total_score DESC, ls.last_solve ASC NULLS LAST,
			                    u.created_at ASC, u.id ASC
			       ) AS rank
			FROM users u
			LEFT JOIN LATERAL (
				SELECT solved_at AS last_solve
				FROM solves
				WHERE user_id = u.id
				ORDER BY solved_at DESC
				LIMIT 1
			) ls ON true
			WHERE u.role != 'admin' AND u.status = 'active'
		), page_users AS MATERIALIZED (
			SELECT * FROM ranked
			WHERE $3 = '' OR username ILIKE '%' || $3 || '%' ESCAPE '\'
			   OR COALESCE(display_name, '') ILIKE '%' || $3 || '%' ESCAPE '\'
			ORDER BY
				CASE WHEN $4 = 'name' THEN LOWER(COALESCE(display_name, username)) END,
				CASE WHEN $4 = 'name' THEN rank END,
				rank
			LIMIT $1 OFFSET $2
		), flag_totals AS (
			SELECT f.challenge_id, COUNT(*)::int AS total_flags
			FROM flags f
			JOIN challenges c ON c.id = f.challenge_id
			WHERE c.status = 'published'
			  AND (c.release_date IS NULL OR c.release_date <= NOW())
			GROUP BY f.challenge_id
		), completed AS (
			SELECT s.user_id, f.challenge_id
			FROM solves s
			JOIN page_users pu ON pu.id = s.user_id
			JOIN flags f ON f.id = s.flag_id
			JOIN flag_totals ft ON ft.challenge_id = f.challenge_id
			GROUP BY s.user_id, f.challenge_id, ft.total_flags
			HAVING ft.total_flags > 0 AND COUNT(DISTINCT s.flag_id) >= ft.total_flags
		), completed_counts AS (
			SELECT user_id, COUNT(*)::int AS challenges_solved
			FROM completed
			GROUP BY user_id
		), solve_stats AS (
			SELECT s.user_id, COUNT(DISTINCT s.flag_id)::int AS flags_solved
			FROM solves s
			JOIN page_users pu ON pu.id = s.user_id
			GROUP BY s.user_id
		)
		SELECT pu.id, pu.username, pu.display_name, pu.total_score,
			COALESCE(cc.challenges_solved, 0),
			COALESCE(ss.flags_solved, 0),
			pu.last_solve, pu.rank
		FROM page_users pu
		LEFT JOIN completed_counts cc ON cc.user_id = pu.id
		LEFT JOIN solve_stats ss ON ss.user_id = pu.id
		ORDER BY
			CASE WHEN $4 = 'name' THEN LOWER(COALESCE(pu.display_name, pu.username)) END,
			pu.rank
	`
	if economyMode {
		query = teamEconomyScoreboardQuery
	} else if teamsMode {
		query = teamScoreboardQuery
	}

	rows, err := tx.Query(c.Request.Context(), query, limit, offset, queryPattern, sortBy)
	if err != nil {
		h.respondQueryError(c, "failed to get scoreboard", err)
		return
	}
	defer rows.Close()

	entries := []ScoreboardEntry{}
	for rows.Next() {
		var entry ScoreboardEntry
		var lastSolve *time.Time

		if err := rows.Scan(&entry.UserID, &entry.Username, &entry.DisplayName, &entry.TotalScore,
			&entry.ChallengesSolved, &entry.FlagsSolved, &lastSolve, &entry.Rank); err != nil {
			h.logger.Error("failed to scan scoreboard row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch scoreboard"})
			return
		}

		if lastSolve != nil {
			formatted := lastSolve.UTC().Format(time.RFC3339)
			entry.LastSolveAt = &formatted
		}

		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		h.respondQueryError(c, "failed while reading scoreboard", err)
		return
	}
	rows.Close()

	// trends (spark + rank delta) key on user_id; skip in teams mode where the entry ids are teams (team trends are a follow-up)
	if !teamRanked {
		if err := h.attachTrends(c.Request.Context(), tx, entries); err != nil {
			h.respondQueryError(c, "failed to attach scoreboard trends", err)
			return
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.respondQueryError(c, "failed to commit scoreboard snapshot", err)
		return
	}

	var frozen bool
	_ = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE((SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'scoreboard_frozen'), false)`,
	).Scan(&frozen)

	h.respondCacheableJSON(c, cacheKey, 2*time.Second, gin.H{
		"leaderboard":    entries,
		"total_users":    totalUsers,
		"matching_users": matchingUsers,
		"page":           page,
		"limit":          limit,
		"frozen":         frozen,
		"economy":        economyMode,
	})
}

func escapeScoreboardSearch(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// fills each entry's per-team sparkline (cumulative score over its solves) and rank delta (movement since the 20th-most-recent solve)
func (h *ScoreboardHandler) attachTrends(ctx context.Context, query scoreboardQuerier, entries []ScoreboardEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		if id, err := uuid.Parse(entry.UserID); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	// sparkline: cumulative score events per user; hint deductions are score events too, so the terminal spark value agrees with the live score unless an admin has manually overridden it
	sparks := map[string][]int{}
	rows, err := query.Query(ctx,
		`SELECT user_id, points
		 FROM (
			 SELECT user_id, points_awarded AS points, solved_at AS event_at, id
			 FROM solves WHERE user_id = ANY($1)
			 UNION ALL
			 SELECT user_id, -points_deducted AS points, unlocked_at AS event_at, id
			 FROM hint_unlocks WHERE user_id = ANY($1)
		 ) events
		 ORDER BY user_id, event_at, id`, ids)
	if err != nil {
		return fmt.Errorf("spark query: %w", err)
	}
	cum := map[string]int{}
	for rows.Next() {
		var uid uuid.UUID
		var p int
		if err := rows.Scan(&uid, &p); err != nil {
			rows.Close()
			return fmt.Errorf("spark scan: %w", err)
		}
		k := uid.String()
		cum[k] += p
		sparks[k] = append(sparks[k], cum[k])
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("spark rows: %w", err)
	}
	rows.Close()

	rankThen, err := h.historicRanks(ctx, query, ids)
	if err != nil {
		return err
	}

	for i := range entries {
		entries[i].Spark = sparks[entries[i].UserID]
		if rt, ok := rankThen[entries[i].UserID]; ok {
			entries[i].Delta = rt - entries[i].Rank
		}
	}
	return nil
}

// ranks the entire active field at the comparison cutoff, then returns only the requested users; keeps deltas correct across the top-500 matrix boundary and applies the same deterministic tie-breakers as now
func (h *ScoreboardHandler) historicRanks(ctx context.Context, query scoreboardQuerier, ids []uuid.UUID) (map[string]int, error) {
	ranks := map[string]int{}
	if len(ids) == 0 {
		return ranks, nil
	}

	var cutoff time.Time
	if err := query.QueryRow(ctx,
		`SELECT solved_at FROM solves ORDER BY solved_at DESC OFFSET 19 LIMIT 1`).Scan(&cutoff); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ranks, nil
		}
		return ranks, fmt.Errorf("historic cutoff query: %w", err)
	}

	rows, err := query.Query(ctx, `
		WITH solve_stats AS (
			SELECT user_id, COALESCE(SUM(points_awarded), 0)::bigint AS points,
			       MAX(solved_at) AS last_solve
			FROM solves
			WHERE solved_at <= $1
			GROUP BY user_id
		), hint_stats AS (
			SELECT user_id, COALESCE(SUM(points_deducted), 0)::bigint AS points
			FROM hint_unlocks
			WHERE unlocked_at <= $1 AND user_id IS NOT NULL
			GROUP BY user_id
		), ranked AS MATERIALIZED (
			SELECT u.id,
			       ROW_NUMBER() OVER (
			           ORDER BY COALESCE(ss.points, 0) - COALESCE(hs.points, 0) DESC,
			                    ss.last_solve ASC NULLS LAST, u.created_at ASC, u.id ASC
			       )::int AS rank
			FROM users u
			LEFT JOIN solve_stats ss ON ss.user_id = u.id
			LEFT JOIN hint_stats hs ON hs.user_id = u.id
			WHERE u.role != 'admin' AND u.status = 'active'
		)
		SELECT id, rank FROM ranked WHERE id = ANY($2)
	`, cutoff, ids)
	if err != nil {
		return ranks, fmt.Errorf("historic rank query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var rank int
		if err := rows.Scan(&id, &rank); err != nil {
			return ranks, fmt.Errorf("historic rank scan: %w", err)
		}
		ranks[id.String()] = rank
	}
	if err := rows.Err(); err != nil {
		return ranks, fmt.Errorf("historic rank rows: %w", err)
	}
	return ranks, nil
}

type sbPoint struct {
	X int64   `json:"x"`
	Y float64 `json:"y"`
}

type sbSeries struct {
	ID     string    `json:"id"`
	Label  string    `json:"label"`
	Points []sbPoint `json:"points"`
}

func (h *ScoreboardHandler) History(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}
	if h.serveCachedJSON(c, "history") {
		return
	}
	if !h.beginCacheFill(c, "history") {
		return
	}
	defer h.finishCacheFill("history")

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		WITH leaders AS MATERIALIZED (
			SELECT u.id, u.username
			FROM users u
			LEFT JOIN LATERAL (
				SELECT solved_at AS last_solve
				FROM solves WHERE user_id = u.id
				ORDER BY solved_at DESC LIMIT 1
			) ls ON true
			WHERE u.role != 'admin' AND u.status = 'active' AND u.total_score > 0
			ORDER BY u.total_score DESC, ls.last_solve ASC NULLS LAST, u.created_at ASC, u.id ASC
			LIMIT 10
		), events AS (
			SELECT s.user_id, s.solved_at AS event_at, s.points_awarded AS points, s.id
			FROM solves s JOIN leaders l ON l.id = s.user_id
			UNION ALL
			SELECT hu.user_id, hu.unlocked_at AS event_at, -hu.points_deducted AS points, hu.id
			FROM hint_unlocks hu JOIN leaders l ON l.id = hu.user_id
		)
		SELECT l.id, l.username, e.event_at, e.points
		FROM leaders l
		JOIN events e ON e.user_id = l.id
		ORDER BY l.id, e.event_at, e.id
	`)
	if err != nil {
		h.logger.Error("scoreboard history query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}
	defer rows.Close()

	order := []string{}
	byUser := map[string]*sbSeries{}
	cum := map[string]float64{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var solvedAt time.Time
		var pts int
		if err := rows.Scan(&id, &name, &solvedAt, &pts); err != nil {
			h.logger.Error("scoreboard history scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
			return
		}
		key := id.String()
		s := byUser[key]
		if s == nil {
			s = &sbSeries{ID: key, Label: name}
			byUser[key] = s
			order = append(order, key)
		}
		cum[key] += float64(pts)
		s.Points = append(s.Points, sbPoint{X: solvedAt.Unix(), Y: cum[key]})
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("scoreboard history rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch history"})
		return
	}

	series := make([]*sbSeries, 0, len(order))
	for _, k := range order {
		series = append(series, byUser[k])
	}
	h.respondCacheableJSON(c, "history", 5*time.Second, gin.H{"series": series})
}

type profileSolve struct {
	Name          string  `json:"name"`
	Slug          string  `json:"slug"`
	Category      *string `json:"category,omitempty"`
	CategoryColor *string `json:"category_color,omitempty"`
	Points        int     `json:"points"`
	SolvedAt      int64   `json:"solved_at"`
}

type profileChallenge struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Category      string `json:"category"`
	CategoryColor string `json:"category_color"`
	Difficulty    string `json:"difficulty"`
	Points        int    `json:"points"`
	AwardedPoints int    `json:"awarded_points"`
	SolvedFlags   int    `json:"solved_flags"`
	TotalFlags    int    `json:"total_flags"`
	Solved        bool   `json:"solved"`
	SolvedAt      *int64 `json:"solved_at,omitempty"`
	BloodRank     int    `json:"blood_rank"`
}

func (h *ScoreboardHandler) Profile(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}
	username := c.Param("username")
	if len(username) == 0 || len(username) > 50 {
		c.JSON(http.StatusNotFound, gin.H{"error": "player not found"})
		return
	}
	privateView := c.GetString("token_type") == "user" &&
		(c.GetString("username") == username || c.GetString("role") == "admin")
	profileCacheKey := "profile:public:" + username
	if privateView {
		profileCacheKey = "profile:private:" + username
		c.Set(scoreboardPrivateCacheKey, true)
	}
	if h.serveCachedJSON(c, profileCacheKey) {
		return
	}
	if !h.beginCacheFill(c, profileCacheKey) {
		return
	}
	defer h.finishCacheFill(profileCacheKey)
	tx, ok := h.beginReadSnapshot(c)
	if !ok {
		return
	}
	defer tx.Rollback(c.Request.Context())

	var userID uuid.UUID
	var totalScore, globalRank int
	err := tx.QueryRow(c.Request.Context(),
		`SELECT id, total_score, global_rank
		 FROM (
			SELECT u.id, u.username, u.total_score,
			       ROW_NUMBER() OVER (
			           ORDER BY u.total_score DESC, ls.last_solve ASC NULLS LAST,
			                    u.created_at ASC, u.id ASC
			       ) AS global_rank
			FROM users u
			LEFT JOIN LATERAL (
				SELECT solved_at AS last_solve
				FROM solves
				WHERE user_id = u.id
				ORDER BY solved_at DESC
				LIMIT 1
			) ls ON true
			WHERE u.status = 'active' AND u.role != 'admin'
		 ) ranked
		 WHERE username = $1`, username).
		Scan(&userID, &totalScore, &globalRank)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "player not found"})
		return
	}
	if err != nil {
		h.respondQueryError(c, "profile user query", err)
		return
	}
	showPartialProgress := false
	if viewerID, ok := c.Get("user_id"); ok {
		if id, valid := viewerID.(uuid.UUID); valid && id == userID {
			showPartialProgress = true
		}
	}
	if role, ok := c.Get("role"); ok && role == "admin" {
		showPartialProgress = true
	}

	rows, err := tx.Query(c.Request.Context(), `
		WITH completed AS (
			SELECT f.challenge_id
			FROM solves solved
			JOIN flags f ON f.id = solved.flag_id
			WHERE solved.user_id = $1
			GROUP BY f.challenge_id
			HAVING COUNT(DISTINCT solved.flag_id) > 0
			   AND COUNT(DISTINCT solved.flag_id) = (
			       SELECT COUNT(*) FROM flags total WHERE total.challenge_id = f.challenge_id
			   )
		)
		SELECT c.name, c.slug, cat.name, cat.color, s.points_awarded, s.solved_at
		FROM solves s
		JOIN flags f ON f.id = s.flag_id
		JOIN challenges c ON c.id = f.challenge_id
		LEFT JOIN categories cat ON cat.id = c.category_id
		WHERE s.user_id = $1
		  AND c.status = 'published'
		  AND (c.release_date IS NULL OR c.release_date <= NOW())
		  AND ($2 OR c.id IN (SELECT challenge_id FROM completed))
		ORDER BY s.solved_at
	`, userID, showPartialProgress)
	if err != nil {
		h.respondQueryError(c, "profile solves query", err)
		return
	}
	defer rows.Close()

	solves := []profileSolve{}
	for rows.Next() {
		var ps profileSolve
		var solvedAt time.Time
		if err := rows.Scan(&ps.Name, &ps.Slug, &ps.Category, &ps.CategoryColor, &ps.Points, &solvedAt); err != nil {
			h.logger.Error("profile solve scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		ps.SolvedAt = solvedAt.Unix()
		solves = append(solves, ps)
	}
	if err := rows.Err(); err != nil {
		h.respondQueryError(c, "profile solve rows", err)
		return
	}
	rows.Close()
	challengeRows, err := tx.Query(c.Request.Context(), `
		WITH published AS (
			SELECT c.id, c.name, c.slug, c.difficulty, c.base_points,
			       COALESCE(cat.name, 'Uncategorized') AS category,
			       COALESCE(cat.color, '#94a3b8') AS category_color,
			       COALESCE(cat.sort_order, 999) AS category_order
			FROM challenges c
			LEFT JOIN categories cat ON cat.id = c.category_id
			WHERE c.status = 'published'
			  AND (c.release_date IS NULL OR c.release_date <= NOW())
		),
		flag_totals AS (
			SELECT f.challenge_id, COUNT(*)::int AS total_flags
			FROM flags f
			JOIN published p ON p.id = f.challenge_id
			GROUP BY f.challenge_id
		),
		user_progress AS (
			SELECT f.challenge_id, COUNT(DISTINCT s.flag_id)::int AS solved_flags,
			       COALESCE(SUM(s.points_awarded), 0)::int AS awarded_points,
			       MAX(s.solved_at) AS completed_at
			FROM solves s
			JOIN flags f ON f.id = s.flag_id
			JOIN published p ON p.id = f.challenge_id
			WHERE s.user_id = $1
			GROUP BY f.challenge_id
		),
		completion_candidates AS (
			SELECT f.challenge_id, s.user_id, MAX(s.solved_at) AS completed_at
			FROM solves s
			JOIN flags f ON f.id = s.flag_id
			JOIN flag_totals ft ON ft.challenge_id = f.challenge_id
			JOIN users u ON u.id = s.user_id
			WHERE u.role != 'admin' AND u.status = 'active'
			GROUP BY f.challenge_id, s.user_id, ft.total_flags
			HAVING ft.total_flags > 0 AND COUNT(DISTINCT s.flag_id) >= ft.total_flags
		),
		ranked_completions AS MATERIALIZED (
			SELECT challenge_id, user_id, completed_at,
			       ROW_NUMBER() OVER (
				   PARTITION BY challenge_id ORDER BY completed_at, user_id
			       )::int AS blood_rank
			FROM completion_candidates
		)
		SELECT p.name, p.slug, p.category, p.category_color, p.difficulty::text,
		       p.base_points, COALESCE(up.awarded_points, 0),
		       COALESCE(up.solved_flags, 0), COALESCE(ft.total_flags, 0),
		       COALESCE(ft.total_flags, 0) > 0
		           AND COALESCE(up.solved_flags, 0) >= COALESCE(ft.total_flags, 0) AS solved,
		       CASE
		           WHEN COALESCE(ft.total_flags, 0) > 0
		            AND COALESCE(up.solved_flags, 0) >= COALESCE(ft.total_flags, 0)
		           THEN up.completed_at
		       END,
		       CASE WHEN rc.blood_rank BETWEEN 1 AND 3 THEN rc.blood_rank ELSE 0 END
		FROM published p
		LEFT JOIN flag_totals ft ON ft.challenge_id = p.id
		LEFT JOIN user_progress up ON up.challenge_id = p.id
		LEFT JOIN ranked_completions rc ON rc.challenge_id = p.id AND rc.user_id = $1
		ORDER BY p.category_order, p.category, p.base_points, p.name
	`, userID)
	if err != nil {
		h.respondQueryError(c, "profile challenge progress query", err)
		return
	}
	defer challengeRows.Close()

	challenges := []profileChallenge{}
	for challengeRows.Next() {
		var pc profileChallenge
		var solvedAt *time.Time
		if err := challengeRows.Scan(
			&pc.Name, &pc.Slug, &pc.Category, &pc.CategoryColor, &pc.Difficulty,
			&pc.Points, &pc.AwardedPoints, &pc.SolvedFlags, &pc.TotalFlags,
			&pc.Solved, &solvedAt, &pc.BloodRank,
		); err != nil {
			h.logger.Error("profile challenge progress scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if solvedAt != nil {
			unix := solvedAt.Unix()
			pc.SolvedAt = &unix
		}
		if !showPartialProgress && !pc.Solved {
			pc.AwardedPoints = 0
			pc.SolvedFlags = 0
			pc.SolvedAt = nil
		}
		challenges = append(challenges, pc)
	}
	if err := challengeRows.Err(); err != nil {
		h.respondQueryError(c, "profile challenge progress rows", err)
		return
	}
	challengeRows.Close()

	challengesSolved := 0
	for _, challenge := range challenges {
		if challenge.Solved {
			challengesSolved++
		}
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.respondQueryError(c, "failed to commit profile snapshot", err)
		return
	}

	h.respondCacheableJSON(c, profileCacheKey, 2*time.Second, gin.H{
		"user": gin.H{
			"username":          username,
			"total_score":       totalScore,
			"challenges_solved": challengesSolved,
			"global_rank":       globalRank,
		},
		"solves":     solves,
		"challenges": challenges,
	})
}

type matrixChallenge struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	CategoryColor string `json:"category_color"`
	Points        int    `json:"points"`
}

type matrixCellSB struct {
	S int `json:"s"` // solved: 0 or 1
	B int `json:"b"` // blood rank: 0 none, 1/2/3 first/second/third
}

type matrixRowSB struct {
	Rank     int            `json:"rank"`
	UserID   string         `json:"user_id"`
	Username string         `json:"username"`
	Name     string         `json:"name"`
	Total    int            `json:"total"`
	Delta    int            `json:"delta"`
	Cells    []matrixCellSB `json:"cells"`
}

// returns the teams x challenges grid: solve state, blood medals, and recent rank movement; columns are challenges grouped by category
func (h *ScoreboardHandler) Matrix(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}
	if h.serveCachedJSON(c, "matrix") {
		return
	}
	if !h.beginCacheFill(c, "matrix") {
		return
	}
	defer h.finishCacheFill("matrix")
	ctx := c.Request.Context()
	tx, ok := h.beginReadSnapshot(c)
	if !ok {
		return
	}
	defer tx.Rollback(ctx)

	chRows, err := tx.Query(ctx, `
		SELECT c.id, c.slug, c.name, COALESCE(cat.name, 'Uncategorized'),
		       COALESCE(cat.color, '#94a3b8'), c.base_points
		FROM challenges c
		LEFT JOIN categories cat ON cat.id = c.category_id
		WHERE c.status = 'published'
		  AND (c.release_date IS NULL OR c.release_date <= NOW())
		ORDER BY COALESCE(cat.sort_order, 999), cat.name NULLS LAST, c.base_points DESC, c.name`)
	if err != nil {
		h.respondQueryError(c, "matrix challenges", err)
		return
	}
	challenges := []matrixChallenge{}
	chIDs := []string{}
	for chRows.Next() {
		var id uuid.UUID
		var mc matrixChallenge
		if err := chRows.Scan(&id, &mc.Slug, &mc.Name, &mc.Category, &mc.CategoryColor, &mc.Points); err != nil {
			chRows.Close()
			h.logger.Error("matrix challenge scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		chIDs = append(chIDs, id.String())
		challenges = append(challenges, mc)
	}
	if err := chRows.Err(); err != nil {
		chRows.Close()
		h.logger.Error("matrix challenge rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	chRows.Close()

	// rows: the leaderboard, same ordering as the list view; the matrix is deliberately capped so a large event can't force every browser to render an unbounded users x challenges table; total_users makes that cap explicit
	userRows, err := tx.Query(ctx, `
		SELECT u.id, u.username, u.display_name, u.total_score, COUNT(*) OVER()
		FROM users u
		WHERE u.role != 'admin' AND u.status = 'active'
		ORDER BY u.total_score DESC,
		         (SELECT solved_at FROM solves WHERE user_id = u.id ORDER BY solved_at DESC LIMIT 1) ASC NULLS LAST,
		         u.created_at ASC, u.id ASC
		LIMIT 500`)
	if err != nil {
		h.respondQueryError(c, "matrix users", err)
		return
	}
	type urow struct {
		id, username, name string
		total              int
	}
	users := []urow{}
	selectedUserIDs := []uuid.UUID{}
	totalUsers := 0
	for userRows.Next() {
		var u urow
		var id uuid.UUID
		var display *string
		if err := userRows.Scan(&id, &u.username, &display, &u.total, &totalUsers); err != nil {
			userRows.Close()
			h.logger.Error("matrix user scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		u.id = id.String()
		selectedUserIDs = append(selectedUserIDs, id)
		if display != nil {
			u.name = *display
		} else {
			u.name = u.username
		}
		users = append(users, u)
	}
	if err := userRows.Err(); err != nil {
		userRows.Close()
		h.logger.Error("matrix user rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	userRows.Close()

	// rank completions globally so blood placement stays correct, but only return rows for matrix users; keeps Go memory bounded at 500 x the published challenge count even with millions of solves
	bloodRows, err := tx.Query(ctx, `
		WITH published AS (
			SELECT id FROM challenges
			WHERE status = 'published'
			  AND (release_date IS NULL OR release_date <= NOW())
		), flag_totals AS (
			SELECT f.challenge_id, COUNT(*)::int AS total_flags
			FROM flags f
			JOIN published p ON p.id = f.challenge_id
			GROUP BY f.challenge_id
		), completions AS (
			SELECT s.user_id, f.challenge_id, MAX(s.solved_at) AS completed_at
			FROM solves s
			JOIN flags f ON f.id = s.flag_id
			JOIN flag_totals ft ON ft.challenge_id = f.challenge_id
			JOIN users u ON u.id = s.user_id
			WHERE u.role != 'admin' AND u.status = 'active'
			GROUP BY s.user_id, f.challenge_id, ft.total_flags
			HAVING ft.total_flags > 0 AND COUNT(DISTINCT s.flag_id) >= ft.total_flags
		), ranked AS MATERIALIZED (
			SELECT user_id, challenge_id,
			       ROW_NUMBER() OVER (
			           PARTITION BY challenge_id ORDER BY completed_at, user_id
			       ) AS blood
			FROM completions
		)
		SELECT user_id, challenge_id, blood
		FROM ranked
		WHERE user_id = ANY($1)
	`, selectedUserIDs)
	if err != nil {
		h.respondQueryError(c, "matrix blood", err)
		return
	}
	blood := map[string]int{}
	for bloodRows.Next() {
		var user, ch uuid.UUID
		var rank int
		if err := bloodRows.Scan(&user, &ch, &rank); err != nil {
			bloodRows.Close()
			h.logger.Error("matrix blood scan", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		blood[user.String()+"|"+ch.String()] = rank
	}
	if err := bloodRows.Err(); err != nil {
		bloodRows.Close()
		h.logger.Error("matrix blood rows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	bloodRows.Close()

	rankThen, err := h.historicRanks(ctx, tx, selectedUserIDs)
	if err != nil {
		h.respondQueryError(c, "matrix historic ranks", err)
		return
	}

	rows := make([]matrixRowSB, 0, len(users))
	for i, u := range users {
		rankNow := i + 1
		cells := make([]matrixCellSB, len(challenges))
		for col, chID := range chIDs {
			if b, ok := blood[u.id+"|"+chID]; ok {
				medal := 0
				if b <= 3 {
					medal = b
				}
				cells[col] = matrixCellSB{S: 1, B: medal}
			}
		}
		delta := 0
		if rt, ok := rankThen[u.id]; ok {
			delta = rt - rankNow
		}
		rows = append(rows, matrixRowSB{
			Rank:     rankNow,
			UserID:   u.id,
			Username: u.username,
			Name:     u.name,
			Total:    u.total,
			Delta:    delta,
			Cells:    cells,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		h.respondQueryError(c, "failed to commit matrix snapshot", err)
		return
	}

	h.respondCacheableJSON(c, "matrix", 5*time.Second, gin.H{
		"challenges":  challenges,
		"rows":        rows,
		"total_users": totalUsers,
		"limit":       500,
		"truncated":   totalUsers > len(rows),
	})
}
