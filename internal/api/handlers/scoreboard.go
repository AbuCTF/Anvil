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
	Rank        int     `json:"rank"`
	UserID      string  `json:"user_id"`
	Username    string  `json:"username"`
	TotalScore  int     `json:"total_score"`
	TotalSolves int     `json:"total_solves"`
	LastSolveAt *int64  `json:"last_solve_at,omitempty"`
	Country     *string `json:"country,omitempty"`
}

// Get returns the scoreboard
func (h *ScoreboardHandler) Get(c *gin.Context) {
	if !h.config.Platform.ScoreboardEnabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scoreboard is disabled"})
		return
	}

	// Get top users by score
	query := `
		SELECT 
			u.id, u.username, u.total_score, u.country,
			COUNT(DISTINCT s.id) as total_solves,
			MAX(s.solved_at) as last_solve
		FROM users u
		LEFT JOIN solves s ON u.id = s.user_id
		WHERE u.role != 'admin' AND u.is_banned = false
		GROUP BY u.id, u.username, u.total_score, u.country
		ORDER BY u.total_score DESC, last_solve ASC
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

		if err := rows.Scan(&entry.UserID, &entry.Username, &entry.TotalScore,
			&entry.Country, &entry.TotalSolves, &lastSolve); err != nil {
			continue
		}

		entry.Rank = rank
		if lastSolve != nil {
			ts := lastSolve.Unix()
			entry.LastSolveAt = &ts
		}

		entries = append(entries, entry)
		rank++
	}

	if entries == nil {
		entries = []ScoreboardEntry{}
	}

	// Get total user count
	var totalUsers int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE role != 'admin' AND is_banned = false`).Scan(&totalUsers)

	c.JSON(http.StatusOK, gin.H{
		"entries":     entries,
		"total_users": totalUsers,
	})
}
