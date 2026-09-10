package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// ScoreboardService handles scoreboard operations
type ScoreboardService struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

// NewScoreboardService creates a new scoreboard service
func NewScoreboardService(cfg *config.Config, db *database.DB, logger *zap.Logger) *ScoreboardService {
	return &ScoreboardService{config: cfg, db: db, logger: logger}
}

// ScoreboardEntry represents an entry in the scoreboard
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

// Get returns the scoreboard, paginated so the full field is served page by page.
func (h *ScoreboardHandler) Get(c *gin.Context) {
	if !h.config.Platform.ScoreboardEnabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scoreboard is disabled"})
		return
	}

	page := 1
	if v, err := strconv.Atoi(c.Query("page")); err == nil && v > 0 {
		page = v
	}
	limit := 100
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 500 {
		limit = 500
	}
	offset := (page - 1) * limit

	query := `
		SELECT u.id, u.username, u.display_name, u.total_score,
			COUNT(DISTINCT f.challenge_id) as challenges_solved,
			COUNT(DISTINCT s.flag_id) as flags_solved,
			MAX(s.solved_at) as last_solve
		FROM users u
		LEFT JOIN solves s ON u.id = s.user_id
		LEFT JOIN flags f ON s.flag_id = f.id
		WHERE u.role != 'admin' AND u.status = 'active'
		GROUP BY u.id, u.username, u.display_name, u.total_score
		ORDER BY u.total_score DESC, last_solve ASC NULLS LAST
		LIMIT $1 OFFSET $2
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query, limit, offset)
	if err != nil {
		h.logger.Error("failed to get scoreboard", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch scoreboard"})
		return
	}
	defer rows.Close()

	entries := []ScoreboardEntry{}
	rank := offset + 1
	for rows.Next() {
		var entry ScoreboardEntry
		var lastSolve *time.Time

		if err := rows.Scan(&entry.UserID, &entry.Username, &entry.DisplayName, &entry.TotalScore,
			&entry.ChallengesSolved, &entry.FlagsSolved, &lastSolve); err != nil {
			h.logger.Warn("failed to scan scoreboard row", zap.Error(err))
			continue
		}

		entry.Rank = rank
		if lastSolve != nil {
			formatted := lastSolve.Format(time.RFC3339)
			entry.LastSolveAt = &formatted
		}

		entries = append(entries, entry)
		rank++
	}

	var totalUsers int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE role != 'admin' AND status = 'active'`).Scan(&totalUsers)

	h.attachTrends(c.Request.Context(), entries)

	c.JSON(http.StatusOK, gin.H{
		"leaderboard": entries,
		"total_users": totalUsers,
		"page":        page,
		"limit":       limit,
	})
}

// attachTrends fills each entry's per-team sparkline (cumulative score over its
// solves) and rank delta (movement since the 20th-most-recent solve).
func (h *ScoreboardHandler) attachTrends(ctx context.Context, entries []ScoreboardEntry) {
	if len(entries) == 0 {
		return
	}
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.UserID
	}

	// Sparkline: cumulative points per user, in solve order.
	sparks := map[string][]int{}
	if rows, err := h.db.Pool.Query(ctx,
		`SELECT user_id, points_awarded FROM solves WHERE user_id::text = ANY($1) ORDER BY user_id, solved_at`, ids); err == nil {
		cum := map[string]int{}
		for rows.Next() {
			var uid uuid.UUID
			var p int
			if rows.Scan(&uid, &p) == nil {
				k := uid.String()
				cum[k] += p
				sparks[k] = append(sparks[k], cum[k])
			}
		}
		rows.Close()
	}

	// Rank delta: rank now (entry.Rank) vs rank as of the 20th-newest solve.
	rankThen := map[string]int{}
	var cutoff time.Time
	if h.db.Pool.QueryRow(ctx, `SELECT solved_at FROM solves ORDER BY solved_at DESC OFFSET 20 LIMIT 1`).Scan(&cutoff) == nil {
		scoreThen := map[string]int{}
		if rows, err := h.db.Pool.Query(ctx,
			`SELECT user_id, COALESCE(SUM(points_awarded), 0) FROM solves WHERE solved_at <= $1 GROUP BY user_id`, cutoff); err == nil {
			for rows.Next() {
				var uid uuid.UUID
				var s int
				if rows.Scan(&uid, &s) == nil {
					scoreThen[uid.String()] = s
				}
			}
			rows.Close()
		}
		type us struct {
			id   string
			then int
		}
		all := []us{}
		if rows, err := h.db.Pool.Query(ctx,
			`SELECT id FROM users WHERE role != 'admin' AND status = 'active'`); err == nil {
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) == nil {
					all = append(all, us{id.String(), scoreThen[id.String()]})
				}
			}
			rows.Close()
		}
		sort.SliceStable(all, func(a, b int) bool { return all[a].then > all[b].then })
		for i, u := range all {
			rankThen[u.id] = i + 1
		}
	}

	for i := range entries {
		entries[i].Spark = sparks[entries[i].UserID]
		if rt, ok := rankThen[entries[i].UserID]; ok {
			entries[i].Delta = rt - entries[i].Rank
		}
	}
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

// History returns the top players' cumulative score over time for the race chart.
func (h *ScoreboardHandler) History(c *gin.Context) {
	if !h.config.Platform.ScoreboardEnabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scoreboard is disabled"})
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT u.id, u.username, s.solved_at, s.points_awarded
		FROM users u
		JOIN solves s ON s.user_id = u.id
		WHERE u.id IN (
			SELECT id FROM users
			WHERE role != 'admin' AND status = 'active' AND total_score > 0
			ORDER BY total_score DESC LIMIT 10
		)
		ORDER BY u.id, s.solved_at
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
			continue
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

	series := make([]*sbSeries, 0, len(order))
	for _, k := range order {
		series = append(series, byUser[k])
	}
	c.JSON(http.StatusOK, gin.H{"series": series})
}

type profileSolve struct {
	Name          string  `json:"name"`
	Slug          string  `json:"slug"`
	Category      *string `json:"category,omitempty"`
	CategoryColor *string `json:"category_color,omitempty"`
	Points        int     `json:"points"`
	SolvedAt      int64   `json:"solved_at"`
}

// Profile returns a player's solved challenges with times and categories.
func (h *ScoreboardHandler) Profile(c *gin.Context) {
	username := c.Param("username")

	var userID uuid.UUID
	var totalScore int
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, total_score FROM users WHERE username = $1 AND status = 'active'`, username).
		Scan(&userID, &totalScore)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "player not found"})
		return
	}
	if err != nil {
		h.logger.Error("profile user query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT c.name, c.slug, cat.name, cat.color, f.points, s.solved_at
		FROM solves s
		JOIN flags f ON f.id = s.flag_id
		JOIN challenges c ON c.id = f.challenge_id
		LEFT JOIN categories cat ON cat.id = c.category_id
		WHERE s.user_id = $1
		ORDER BY s.solved_at
	`, userID)
	if err != nil {
		h.logger.Error("profile solves query", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	solves := []profileSolve{}
	for rows.Next() {
		var ps profileSolve
		var solvedAt time.Time
		if err := rows.Scan(&ps.Name, &ps.Slug, &ps.Category, &ps.CategoryColor, &ps.Points, &solvedAt); err != nil {
			continue
		}
		ps.SolvedAt = solvedAt.Unix()
		solves = append(solves, ps)
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"username":          username,
			"total_score":       totalScore,
			"challenges_solved": len(solves),
		},
		"solves": solves,
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

// Matrix returns the teams x challenges grid: solve state, blood medals, and
// recent rank movement. Columns are challenges grouped by category.
func (h *ScoreboardHandler) Matrix(c *gin.Context) {
	if !h.config.Platform.ScoreboardEnabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scoreboard is disabled"})
		return
	}
	ctx := c.Request.Context()

	// Columns: published challenges, grouped by category.
	chRows, err := h.db.Pool.Query(ctx, `
		SELECT c.id, c.slug, c.name, COALESCE(cat.name, 'Uncategorized'),
		       COALESCE(cat.color, '#94a3b8'), c.base_points
		FROM challenges c
		LEFT JOIN categories cat ON cat.id = c.category_id
		WHERE c.status = 'published'
		ORDER BY COALESCE(cat.sort_order, 999), cat.name NULLS LAST, c.base_points DESC, c.name`)
	if err != nil {
		h.logger.Error("matrix challenges", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	challenges := []matrixChallenge{}
	chIDs := []string{}
	for chRows.Next() {
		var id uuid.UUID
		var mc matrixChallenge
		if err := chRows.Scan(&id, &mc.Slug, &mc.Name, &mc.Category, &mc.CategoryColor, &mc.Points); err != nil {
			continue
		}
		chIDs = append(chIDs, id.String())
		challenges = append(challenges, mc)
	}
	chRows.Close()

	// Blood rank per (user, challenge): order distinct solvers by first solve.
	bloodRows, err := h.db.Pool.Query(ctx, `
		SELECT user_id, challenge_id,
		       ROW_NUMBER() OVER (PARTITION BY challenge_id ORDER BY first_at) AS blood
		FROM (
			SELECT user_id, challenge_id, MIN(solved_at) AS first_at
			FROM solves GROUP BY user_id, challenge_id
		) fs`)
	if err != nil {
		h.logger.Error("matrix blood", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	blood := map[string]int{}
	for bloodRows.Next() {
		var user, ch uuid.UUID
		var rank int
		if err := bloodRows.Scan(&user, &ch, &rank); err != nil {
			continue
		}
		blood[user.String()+"|"+ch.String()] = rank
	}
	bloodRows.Close()

	// Rows: the leaderboard, same ordering as the list view.
	userRows, err := h.db.Pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name, u.total_score
		FROM users u
		WHERE u.role != 'admin' AND u.status = 'active' AND u.total_score > 0
		ORDER BY u.total_score DESC, (SELECT MAX(solved_at) FROM solves WHERE user_id = u.id) ASC NULLS LAST
		LIMIT 100`)
	if err != nil {
		h.logger.Error("matrix users", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	type urow struct {
		id, username, name string
		total              int
	}
	users := []urow{}
	for userRows.Next() {
		var u urow
		var id uuid.UUID
		var display *string
		if err := userRows.Scan(&id, &u.username, &display, &u.total); err != nil {
			continue
		}
		u.id = id.String()
		if display != nil {
			u.name = *display
		} else {
			u.name = u.username
		}
		users = append(users, u)
	}
	userRows.Close()

	// Rank movement: compare current rank to rank as of the 20th-newest solve.
	rankThen := map[string]int{}
	var cutoff time.Time
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT solved_at FROM solves ORDER BY solved_at DESC OFFSET 20 LIMIT 1`).Scan(&cutoff); err == nil {
		thenRows, err := h.db.Pool.Query(ctx,
			`SELECT user_id, COALESCE(SUM(points_awarded), 0) FROM solves WHERE solved_at <= $1 GROUP BY user_id`, cutoff)
		if err == nil {
			scoreThen := map[string]int{}
			for thenRows.Next() {
				var id uuid.UUID
				var s int
				if err := thenRows.Scan(&id, &s); err == nil {
					scoreThen[id.String()] = s
				}
			}
			thenRows.Close()

			ordered := make([]urow, len(users))
			copy(ordered, users)
			sort.SliceStable(ordered, func(a, b int) bool {
				return scoreThen[ordered[a].id] > scoreThen[ordered[b].id]
			})
			for i, u := range ordered {
				rankThen[u.id] = i + 1
			}
		}
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

	c.JSON(http.StatusOK, gin.H{"challenges": challenges, "rows": rows})
}
