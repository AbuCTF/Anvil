package handlers

import (
	"errors"
	"net/http"
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
}

// Get returns the scoreboard
func (h *ScoreboardHandler) Get(c *gin.Context) {
	if !h.config.Platform.ScoreboardEnabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scoreboard is disabled"})
		return
	}

	// Get top users by score with challenge and flag counts
	query := `
		SELECT 
			u.id, 
			u.username, 
			u.display_name,
			u.total_score, 
			COUNT(DISTINCT f.challenge_id) as challenges_solved,
			COUNT(DISTINCT s.flag_id) as flags_solved,
			MAX(s.solved_at) as last_solve
		FROM users u
		LEFT JOIN solves s ON u.id = s.user_id
		LEFT JOIN flags f ON s.flag_id = f.id
		WHERE u.role != 'admin' AND u.status = 'active'
		GROUP BY u.id, u.username, u.display_name, u.total_score
		HAVING u.total_score > 0 OR COUNT(s.id) > 0
		ORDER BY u.total_score DESC, last_solve ASC NULLS LAST
		LIMIT 100
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to get scoreboard", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch scoreboard"})
		return
	}
	defer rows.Close()

	var entries []ScoreboardEntry
	rank := 1
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

	if entries == nil {
		entries = []ScoreboardEntry{}
	}

	// Get total user count (only users with scores or activity)
	var totalUsers int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE role != 'admin' AND status = 'active' AND total_score > 0`).Scan(&totalUsers)

	c.JSON(http.StatusOK, gin.H{
		"leaderboard": entries,
		"total_users": totalUsers,
	})
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
