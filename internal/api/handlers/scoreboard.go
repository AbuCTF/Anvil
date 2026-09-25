package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// teamRankedCTE is the team board's ranking (jeopardy total + capped KotH hold-time,
// test teams excluded); /user/me ranks against it too so the badge matches the board.
// a team only lists (and ranks) once it has scored.
var teamRankedCTE = `last_solves AS (
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
		WHERE ` + publicTeamSQL("t") + `
		  AND (ls.last_solve IS NOT NULL OR t.total_score + t.koth_score > 0)
	)`

var teamCountQuery = `WITH ` + teamRankedCTE + `
	SELECT COUNT(*), COUNT(*) FILTER (WHERE $1 = '' OR name ILIKE '%' || $1 || '%' ESCAPE '\')
	FROM ranked`

// teamScoreboardQuery ranks teams (teams mode) with the same column shape, params ($1 limit, $2 offset, $3 search, $4 sort), and scan order as the user query: id, name (as username), display_name (null), total_score, challenges_solved (fully-completed), flags_solved (distinct flags), last_solve, rank
var teamScoreboardQuery = `
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
	), ` + teamRankedCTE + `, page_teams AS (
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

// teamEconomyRankedCTE is the economy board's ranking: economy points plus the
// capped KotH hold-time (koth_score is points-only, never convertible).
var teamEconomyRankedCTE = `scores AS (
		SELECT t.id, t.name,
			COALESCE(ets.points, 0) + t.koth_score AS points,
			(SELECT MAX(s.solved_at) FROM solves s JOIN users u ON u.id = s.user_id WHERE u.team_id = t.id) AS last_solve,
			(SELECT COUNT(*)::int FROM economy_challenge_state e WHERE e.team_id = t.id AND e.holds_solve) AS solved
		FROM teams t
		LEFT JOIN economy_team_score ets ON ets.team_id = t.id
		WHERE ` + publicTeamSQL("t") + `
	), ranked AS (
		SELECT id, name, points, last_solve, solved,
			ROW_NUMBER() OVER (
				ORDER BY points DESC, last_solve ASC NULLS LAST, name ASC
			) AS rank
		FROM scores
		WHERE points > 0 OR solved > 0 OR last_solve IS NOT NULL
	)`

var teamEconomyCountQuery = `WITH ` + teamEconomyRankedCTE + `
	SELECT COUNT(*), COUNT(*) FILTER (WHERE $1 = '' OR name ILIKE '%' || $1 || '%' ESCAPE '\')
	FROM ranked`

// teamEconomyScoreboardQuery ranks teams by their economy point total (economy mode); same column/scan shape as the user + team queries; challenges/flags "solved" = challenges the team holds under the economy
var teamEconomyScoreboardQuery = `
	WITH ` + teamEconomyRankedCTE + `, page_teams AS (
		SELECT * FROM ranked
		WHERE $3 = '' OR name ILIKE '%' || $3 || '%' ESCAPE '\'
		ORDER BY CASE WHEN $4 = 'name' THEN LOWER(name) END, rank
		LIMIT $1 OFFSET $2
	)
	SELECT pt.id, pt.name, NULL::text, ROUND(pt.points)::int, pt.solved, pt.solved, pt.last_solve, pt.rank
	FROM page_teams pt
	ORDER BY CASE WHEN $4 = 'name' THEN LOWER(pt.name) END, pt.rank
`

var userCountQuery = `SELECT COUNT(*), COUNT(*) FILTER (
		 WHERE $1 = '' OR username ILIKE '%' || $1 || '%' ESCAPE '\'
		    OR COALESCE(display_name, '') ILIKE '%' || $1 || '%' ESCAPE '\'
	 )
	 FROM users WHERE role NOT IN ('admin', 'author') AND status = 'active'`

var userScoreboardQuery = `
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
		WHERE u.role NOT IN ('admin', 'author') AND u.status = 'active'
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

// boardParams reads page/limit/q/sort, writing the 400 itself when invalid.
func boardParams(c *gin.Context, defaultLimit, maxLimit int) (page, limit int, queryText, sortBy string, ok bool) {
	page = 1
	if raw := c.Query("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 1_000_000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page must be between 1 and 1000000"})
			return
		}
		page = v
	}
	limit = defaultLimit
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		limit = v
	}
	limit = min(limit, maxLimit)
	queryText = strings.TrimSpace(c.Query("q"))
	if len(queryText) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q must be at most 50 characters"})
		return
	}
	sortBy = c.DefaultQuery("sort", "rank")
	if sortBy != "rank" && sortBy != "name" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort must be rank or name"})
		return
	}
	return page, limit, queryText, sortBy, true
}

// boardMode reads the settings that decide what the board ranks; read before the
// snapshot tx so a fill never holds two pooled connections.
func (h *ScoreboardHandler) boardMode(ctx context.Context) (teamRanked, economy, frozen bool, err error) {
	teamsMode, err := isTeamsMode(ctx, h.db)
	if err != nil {
		return false, false, false, fmt.Errorf("read teams_mode: %w", err)
	}
	economy, err = isEconomyMode(ctx, h.db)
	if err != nil {
		return false, false, false, fmt.Errorf("read economy_mode: %w", err)
	}
	frozen, _ = boolSettingOrDefault(ctx, h.db, "scoreboard_frozen", false)
	return teamsMode || economy, economy, frozen, nil
}

// standingsPage is one page of the public board plus its (total, matching) counts;
// team boards hold only teams that have scored.
func standingsPage(ctx context.Context, q scoreboardQuerier, teamRanked, economy bool,
	limit, offset int, pattern, sortBy string) ([]ScoreboardEntry, int, int, error) {
	countQuery, query := userCountQuery, userScoreboardQuery
	if economy {
		countQuery, query = teamEconomyCountQuery, teamEconomyScoreboardQuery
	} else if teamRanked {
		countQuery, query = teamCountQuery, teamScoreboardQuery
	}
	var total, matching int
	if err := q.QueryRow(ctx, countQuery, pattern).Scan(&total, &matching); err != nil {
		return nil, 0, 0, fmt.Errorf("count scoreboard rows: %w", err)
	}
	entries := []ScoreboardEntry{}
	if offset >= matching {
		return entries, total, matching, nil
	}
	rows, err := q.Query(ctx, query, limit, offset, pattern, sortBy)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("scoreboard page: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry ScoreboardEntry
		var lastSolve *time.Time
		if err := rows.Scan(&entry.UserID, &entry.Username, &entry.DisplayName, &entry.TotalScore,
			&entry.ChallengesSolved, &entry.FlagsSolved, &lastSolve, &entry.Rank); err != nil {
			return nil, 0, 0, fmt.Errorf("scan scoreboard row: %w", err)
		}
		if lastSolve != nil {
			formatted := lastSolve.UTC().Format(time.RFC3339)
			entry.LastSolveAt = &formatted
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("read scoreboard rows: %w", err)
	}
	return entries, total, matching, nil
}

func (h *ScoreboardHandler) Get(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}
	page, limit, queryText, sortBy, ok := boardParams(c, 100, 500)
	if !ok {
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
	// teams and economy mode rank teams instead of users (trends skipped)
	teamRanked, economyMode, frozen, err := h.boardMode(c.Request.Context())
	if err != nil {
		h.respondQueryError(c, "failed to read scoreboard mode", err)
		return
	}

	tx, ok := h.beginReadSnapshot(c)
	if !ok {
		return
	}
	defer tx.Rollback(c.Request.Context())

	entries, totalUsers, matchingUsers, err := standingsPage(c.Request.Context(), tx,
		teamRanked, economyMode, limit, offset, queryPattern, sortBy)
	if err != nil {
		h.respondQueryError(c, "failed to get scoreboard", err)
		return
	}

	// trends (spark + rank delta) key on user_id; team boards skip them (team trends are a follow-up)
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

	h.respondCacheableJSON(c, cacheKey, 2*time.Second, gin.H{
		"leaderboard":    entries,
		"total_users":    totalUsers,
		"matching_users": matchingUsers,
		"page":           page,
		"limit":          limit,
		"frozen":         frozen,
		"economy":        economyMode,
		"teams":          teamRanked,
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
			WHERE u.role NOT IN ('admin', 'author') AND u.status = 'active'
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
	ctx := c.Request.Context()
	teamRanked, economyMode, _, err := h.boardMode(ctx)
	if err != nil {
		h.respondQueryError(c, "failed to read scoreboard mode", err)
		return
	}

	var series []*sbSeries
	switch {
	case economyMode:
		series, err = h.economyHistory(ctx)
	case teamRanked:
		series, err = h.cumulativeHistory(ctx, teamHistoryQuery)
	default:
		series, err = h.cumulativeHistory(ctx, userHistoryQuery)
	}
	if err != nil {
		h.respondQueryError(c, "failed to fetch history", err)
		return
	}
	h.respondCacheableJSON(c, "history", 5*time.Second, gin.H{"series": series, "teams": teamRanked})
}

var userHistoryQuery = `
	WITH leaders AS MATERIALIZED (
		SELECT u.id, u.username, u.display_name
		FROM users u
		LEFT JOIN LATERAL (
			SELECT solved_at AS last_solve
			FROM solves WHERE user_id = u.id
			ORDER BY solved_at DESC LIMIT 1
		) ls ON true
		WHERE u.role NOT IN ('admin', 'author') AND u.status = 'active' AND u.total_score > 0
		ORDER BY u.total_score DESC, ls.last_solve ASC NULLS LAST, u.created_at ASC, u.id ASC
		LIMIT 10
	), events AS (
		SELECT s.user_id, s.solved_at AS event_at, s.points_awarded AS points, s.id
		FROM solves s JOIN leaders l ON l.id = s.user_id
		UNION ALL
		SELECT hu.user_id, hu.unlocked_at AS event_at, -hu.points_deducted AS points, hu.id
		FROM hint_unlocks hu JOIN leaders l ON l.id = hu.user_id
	)
	SELECT l.id, COALESCE(NULLIF(l.display_name, ''), l.username), e.event_at, e.points
	FROM leaders l
	JOIN events e ON e.user_id = l.id
	ORDER BY l.id, e.event_at, e.id
`

// top 10 teams; a flag scores once per team, on its first capture by any member
var teamHistoryQuery = `
	WITH ` + teamRankedCTE + `, leaders AS MATERIALIZED (
		SELECT id, name, rank FROM ranked ORDER BY rank LIMIT 10
	), firsts AS (
		SELECT DISTINCT ON (u.team_id, s.flag_id) u.team_id, s.solved_at, s.points_awarded, s.id
		FROM solves s
		JOIN users u ON u.id = s.user_id
		JOIN leaders l ON l.id = u.team_id
		ORDER BY u.team_id, s.flag_id, s.solved_at, s.id
	)
	SELECT l.id, l.name, f.solved_at, f.points_awarded
	FROM leaders l
	JOIN firsts f ON f.team_id = l.id
	ORDER BY l.rank, f.solved_at, f.id
`

// cumulativeHistory runs a (id, label, at, points) event query into running totals, one series per id in first-seen order.
func (h *ScoreboardHandler) cumulativeHistory(ctx context.Context, query string) ([]*sbSeries, error) {
	rows, err := h.db.Pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("history query: %w", err)
	}
	defer rows.Close()

	order := []string{}
	byID := map[string]*sbSeries{}
	cum := map[string]float64{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var at time.Time
		var pts int
		if err := rows.Scan(&id, &name, &at, &pts); err != nil {
			return nil, fmt.Errorf("history scan: %w", err)
		}
		key := id.String()
		s := byID[key]
		if s == nil {
			s = &sbSeries{ID: key, Label: name}
			byID[key] = s
			order = append(order, key)
		}
		cum[key] += float64(pts)
		s.Points = append(s.Points, sbPoint{X: at.Unix(), Y: cum[key]})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history rows: %w", err)
	}

	series := make([]*sbSeries, 0, len(order))
	for _, k := range order {
		series = append(series, byID[k])
	}
	return series, nil
}

// economyHistory replays the point ledger of the top 10 economy teams. challenge
// events carry the team's new value for that challenge (applied as a delta floored
// at 0, like the live score); convert/freeze events carry the team total. only
// captures and conversions are plotted, decay between them folds into the next point.
func (h *ScoreboardHandler) economyHistory(ctx context.Context) ([]*sbSeries, error) {
	rows, err := h.db.Pool.Query(ctx, `
		WITH `+teamEconomyRankedCTE+`, leaders AS MATERIALIZED (
			SELECT id, name, rank FROM ranked ORDER BY rank LIMIT 10
		)
		SELECT l.id, l.name, e.created_at, e.challenge_id, e.value_after::float8
		FROM leaders l
		JOIN economy_point_events e ON e.team_id = l.id
		ORDER BY l.rank, e.id`)
	if err != nil {
		return nil, fmt.Errorf("economy history query: %w", err)
	}
	defer rows.Close()

	series := []*sbSeries{}
	var cur *sbSeries
	var held map[uuid.UUID]float64
	var pts float64
	var lastAt time.Time
	pending := false
	flush := func() {
		if cur != nil && pending {
			cur.Points = append(cur.Points, sbPoint{X: lastAt.Unix(), Y: math.Round(pts)})
		}
	}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var at time.Time
		var challengeID *uuid.UUID
		var value float64
		if err := rows.Scan(&id, &name, &at, &challengeID, &value); err != nil {
			return nil, fmt.Errorf("economy history scan: %w", err)
		}
		if cur == nil || cur.ID != id.String() {
			flush()
			cur = &sbSeries{ID: id.String(), Label: name}
			series = append(series, cur)
			held, pts, pending = map[uuid.UUID]float64{}, 0, false
		}
		lastAt = at
		plot := true
		if challengeID != nil {
			prev, ok := held[*challengeID]
			held[*challengeID] = value
			pts = math.Max(pts+value-prev, 0)
			plot = !ok || value > prev
		} else {
			pts = value
		}
		if plot {
			cur.Points = append(cur.Points, sbPoint{X: at.Unix(), Y: math.Round(pts)})
		}
		pending = !plot
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("economy history rows: %w", err)
	}
	flush()
	return series, nil
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
	var displayName *string
	err := tx.QueryRow(c.Request.Context(),
		`SELECT id, display_name, total_score, global_rank
		 FROM (
			SELECT u.id, u.username, u.display_name, u.total_score,
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
			WHERE u.status = 'active' AND u.role NOT IN ('admin', 'author')
		 ) ranked
		 WHERE username = $1`, username).
		Scan(&userID, &displayName, &totalScore, &globalRank)
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
			WHERE u.role NOT IN ('admin', 'author') AND u.status = 'active'
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
			"display_name":      displayName,
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
	Difficulty    string `json:"difficulty"`
	Points        int    `json:"points"`
}

type matrixCellSB struct {
	S int `json:"s"`           // solved: 0 or 1
	B int `json:"b"`           // blood rank: 0 none, 1/2/3 first/second/third
	P int `json:"p,omitempty"` // economy: holds a share without the full solve (s stays 0)
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

// completions are ranked globally so blood placement stays correct, but only the page's rows come back
var userBloodQuery = `
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
		WHERE u.role NOT IN ('admin', 'author') AND u.status = 'active'
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
`

// a team completes a challenge when its members together hold every flag; the
// completion time is when its last missing flag was first captured
var teamBloodQuery = `
	WITH public_teams AS (
		SELECT t.id FROM teams t WHERE ` + publicTeamSQL("t") + `
	), flag_totals AS (
		SELECT f.challenge_id, COUNT(*)::int AS total_flags
		FROM flags f
		JOIN challenges c ON c.id = f.challenge_id
		WHERE c.status = 'published'
		  AND (c.release_date IS NULL OR c.release_date <= NOW())
		GROUP BY f.challenge_id
	), team_flags AS (
		SELECT u.team_id, f.challenge_id, s.flag_id, MIN(s.solved_at) AS captured_at
		FROM solves s
		JOIN users u ON u.id = s.user_id
		JOIN public_teams pt ON pt.id = u.team_id
		JOIN flags f ON f.id = s.flag_id
		JOIN flag_totals ft ON ft.challenge_id = f.challenge_id
		GROUP BY u.team_id, f.challenge_id, s.flag_id
	), completions AS (
		SELECT tf.team_id, tf.challenge_id, MAX(tf.captured_at) AS completed_at
		FROM team_flags tf
		JOIN flag_totals ft ON ft.challenge_id = tf.challenge_id
		GROUP BY tf.team_id, tf.challenge_id, ft.total_flags
		HAVING COUNT(*) >= ft.total_flags
	), ranked AS MATERIALIZED (
		SELECT team_id, challenge_id,
		       ROW_NUMBER() OVER (
		           PARTITION BY challenge_id ORDER BY completed_at, team_id
		       ) AS blood
		FROM completions
	)
	SELECT team_id, challenge_id, blood
	FROM ranked
	WHERE team_id = ANY($1)
`

// returns one page of the board as a rows x challenges grid: solve state, blood
// medals and recent rank movement. rows are the standings page (same filter, search,
// sort and rank); columns are released, solvable challenges grouped by category.
func (h *ScoreboardHandler) Matrix(c *gin.Context) {
	cancel := limitScoreboardRequest(c)
	defer cancel()
	if !h.scoreboardAvailable(c) {
		return
	}
	// no limit = the old top-500 grid, which herald scans for first bloods; the page asks for 50
	page, limit, queryText, sortBy, ok := boardParams(c, 500, 500)
	if !ok {
		return
	}
	cacheKey := "matrix:" + strconv.Itoa(page) + ":" + strconv.Itoa(limit) + ":" + sortBy + ":" + queryText
	staff := isStaff(c)
	if staff {
		// staff preview the slate before the start, so their copy never goes public
		cacheKey = "staff:" + cacheKey
		c.Set(scoreboardPrivateCacheKey, true)
	}
	if h.serveCachedJSON(c, cacheKey) {
		return
	}
	if !h.beginCacheFill(c, cacheKey) {
		return
	}
	defer h.finishCacheFill(cacheKey)
	ctx := c.Request.Context()
	teamRanked, economyMode, frozen, err := h.boardMode(ctx)
	if err != nil {
		h.respondQueryError(c, "failed to read scoreboard mode", err)
		return
	}
	// nothing before the start, same as the challenge list
	phase, _ := eventPlayState(c, h.db)
	hidden := phase == "scheduled" && !staff
	tx, ok := h.beginReadSnapshot(c)
	if !ok {
		return
	}
	defer tx.Rollback(ctx)

	challenges := []matrixChallenge{}
	chIDs := []string{}
	if !hidden {
		chRows, err := tx.Query(ctx, `
			SELECT c.id, c.slug, c.name, COALESCE(cat.name, 'Uncategorized'),
			       COALESCE(cat.color, '#94a3b8'), c.difficulty::text, c.base_points
			FROM challenges c
			LEFT JOIN categories cat ON cat.id = c.category_id
			WHERE c.status = 'published'
			  AND (c.release_date IS NULL OR c.release_date <= NOW())
			  AND EXISTS (SELECT 1 FROM flags f WHERE f.challenge_id = c.id)
			ORDER BY COALESCE(cat.sort_order, 999), cat.name NULLS LAST, c.difficulty, c.base_points, c.name`)
		if err != nil {
			h.respondQueryError(c, "matrix challenges", err)
			return
		}
		for chRows.Next() {
			var id uuid.UUID
			var mc matrixChallenge
			if err := chRows.Scan(&id, &mc.Slug, &mc.Name, &mc.Category, &mc.CategoryColor, &mc.Difficulty, &mc.Points); err != nil {
				chRows.Close()
				h.respondQueryError(c, "matrix challenge scan", err)
				return
			}
			chIDs = append(chIDs, id.String())
			challenges = append(challenges, mc)
		}
		chRows.Close()
		if err := chRows.Err(); err != nil {
			h.respondQueryError(c, "matrix challenge rows", err)
			return
		}
	}

	entries, total, matching := []ScoreboardEntry{}, 0, 0
	if !hidden {
		entries, total, matching, err = standingsPage(ctx, tx, teamRanked, economyMode,
			limit, (page-1)*limit, escapeScoreboardSearch(queryText), sortBy)
		if err != nil {
			h.respondQueryError(c, "matrix rows", err)
			return
		}
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		if id, err := uuid.Parse(entry.UserID); err == nil {
			ids = append(ids, id)
		}
	}

	solved := map[string]matrixCellSB{}
	if len(ids) > 0 && len(chIDs) > 0 {
		bloodQuery := userBloodQuery
		if teamRanked {
			bloodQuery = teamBloodQuery
		}
		bloodRows, err := tx.Query(ctx, bloodQuery, ids)
		if err != nil {
			h.respondQueryError(c, "matrix blood", err)
			return
		}
		for bloodRows.Next() {
			var owner, ch uuid.UUID
			var rank int
			if err := bloodRows.Scan(&owner, &ch, &rank); err != nil {
				bloodRows.Close()
				h.respondQueryError(c, "matrix blood scan", err)
				return
			}
			medal := 0
			if rank <= 3 {
				medal = rank
			}
			solved[owner.String()+"|"+ch.String()] = matrixCellSB{S: 1, B: medal}
		}
		bloodRows.Close()
		if err := bloodRows.Err(); err != nil {
			h.respondQueryError(c, "matrix blood rows", err)
			return
		}
	}
	// economy scores any held share, so the grid marks it too (the board's solve count includes it)
	if economyMode && len(ids) > 0 && len(chIDs) > 0 {
		heldRows, err := tx.Query(ctx, `SELECT team_id, challenge_id FROM economy_challenge_state
			WHERE holds_solve AND team_id = ANY($1)`, ids)
		if err != nil {
			h.respondQueryError(c, "matrix holdings", err)
			return
		}
		for heldRows.Next() {
			var team, ch uuid.UUID
			if err := heldRows.Scan(&team, &ch); err != nil {
				heldRows.Close()
				h.respondQueryError(c, "matrix holdings scan", err)
				return
			}
			if key := team.String() + "|" + ch.String(); solved[key].S == 0 {
				solved[key] = matrixCellSB{P: 1}
			}
		}
		heldRows.Close()
		if err := heldRows.Err(); err != nil {
			h.respondQueryError(c, "matrix holdings rows", err)
			return
		}
	}

	// rank movement keys on users; team boards don't track it yet
	rankThen := map[string]int{}
	if !teamRanked {
		if rankThen, err = h.historicRanks(ctx, tx, ids); err != nil {
			h.respondQueryError(c, "matrix historic ranks", err)
			return
		}
	}

	rows := make([]matrixRowSB, 0, len(entries))
	for _, entry := range entries {
		cells := make([]matrixCellSB, len(challenges))
		for col, chID := range chIDs {
			cells[col] = solved[entry.UserID+"|"+chID]
		}
		name := entry.Username
		if entry.DisplayName != nil && *entry.DisplayName != "" {
			name = *entry.DisplayName
		}
		delta := 0
		if rt, ok := rankThen[entry.UserID]; ok {
			delta = rt - entry.Rank
		}
		rows = append(rows, matrixRowSB{
			Rank:     entry.Rank,
			UserID:   entry.UserID,
			Username: entry.Username,
			Name:     name,
			Total:    entry.TotalScore,
			Delta:    delta,
			Cells:    cells,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		h.respondQueryError(c, "failed to commit matrix snapshot", err)
		return
	}

	h.respondCacheableJSON(c, cacheKey, 5*time.Second, gin.H{
		"challenges":     challenges,
		"rows":           rows,
		"total_users":    total,
		"matching_users": matching,
		"page":           page,
		"limit":          limit,
		"frozen":         frozen,
		"economy":        economyMode,
		"teams":          teamRanked,
	})
}
