package handlers

import (
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
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
	// Uses solved_flags table (not solves) and status enum (not is_banned boolean)
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
		LEFT JOIN solved_flags s ON u.id = s.user_id
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
