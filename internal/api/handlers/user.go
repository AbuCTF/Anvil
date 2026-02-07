package handlers

import (
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// UserService handles user operations
type UserService struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

// NewUserService creates a new user service
func NewUserService(cfg *config.Config, db *database.DB, logger *zap.Logger) *UserService {
	return &UserService{config: cfg, db: db, logger: logger}
}

// UserProfileResponse represents the user profile
type UserProfileResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	Role            string  `json:"role"`
	TotalScore      int     `json:"total_score"`
	Rank            int     `json:"rank"`
	Bio             *string `json:"bio,omitempty"`
	JoinedAt        int64   `json:"joined_at"`
	TotalSolves     int     `json:"total_solves"`
	TotalChallenges int     `json:"total_challenges"`
}

// UserStatsResponse represents user statistics
type UserStatsResponse struct {
	TotalScore         int            `json:"total_score"`
	Rank               int            `json:"rank"`
	TotalSolves        int            `json:"total_solves"`
	TotalChallenges    int            `json:"total_challenges_solved"`
	TotalAttempts      int            `json:"total_attempts"`
	HintsUnlocked      int            `json:"hints_unlocked"`
	PointsSpentOnHints int            `json:"points_spent_on_hints"`
	SolvesByDifficulty map[string]int `json:"solves_by_difficulty"`
	SolvesByCategory   map[string]int `json:"solves_by_category"`
	RecentActivity     []ActivityItem `json:"recent_activity"`
}

// ActivityItem represents a recent activity
type ActivityItem struct {
	Type          string  `json:"type"` // solve, hint_unlock
	ChallengeID   string  `json:"challenge_id"`
	ChallengeName string  `json:"challenge_name"`
	FlagName      *string `json:"flag_name,omitempty"`
	Points        int     `json:"points"`
	Timestamp     int64   `json:"timestamp"`
}

// SolveResponse represents a solve in the history
type SolveResponse struct {
	ID            string `json:"id"`
	ChallengeID   string `json:"challenge_id"`
	ChallengeName string `json:"challenge_name"`
	ChallengeSlug string `json:"challenge_slug"`
	FlagID        string `json:"flag_id"`
	FlagName      string `json:"flag_name"`
	Points        int    `json:"points"`
	SolvedAt      int64  `json:"solved_at"`
}

// GetProfile returns the current user's profile
func (h *UserHandler) GetProfile(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// user_id is already a uuid.UUID from middleware
	uid, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user id"})
		return
	}

	// Get user data
	var profile UserProfileResponse
	var createdAt time.Time
	var bio *string

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, username, email, role, total_score, bio, created_at
		 FROM users WHERE id = $1`, uid).Scan(
		&profile.ID, &profile.Username, &profile.Email, &profile.Role,
		&profile.TotalScore, &bio, &createdAt,
	)
	if err != nil {
		h.logger.Error("failed to get user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch profile"})
		return
	}

	profile.Bio = bio
	profile.JoinedAt = createdAt.Unix()

	// Calculate rank
	var rank int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) + 1 FROM users WHERE total_score > $1 AND role != 'admin'`,
		profile.TotalScore).Scan(&rank)
	profile.Rank = rank

	// Get solve counts
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM solves WHERE user_id = $1`, uid).Scan(&profile.TotalSolves)

	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT f.challenge_id) FROM solves s
		 JOIN flags f ON s.flag_id = f.id
		 WHERE s.user_id = $1`, uid).Scan(&profile.TotalChallenges)

	c.JSON(http.StatusOK, profile)
}

// UpdateProfileRequest represents the profile update request
type UpdateProfileRequest struct {
	Bio *string `json:"bio"`
}

// UpdateProfile updates the current user's profile
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update profile
	_, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET bio = COALESCE($1, bio), updated_at = NOW() WHERE id = $2`,
		req.Bio, uid)
	if err != nil {
		h.logger.Error("failed to update profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile updated"})
}

// GetStats returns user statistics
func (h *UserHandler) GetStats(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	var stats UserStatsResponse
	stats.SolvesByDifficulty = make(map[string]int)
	stats.SolvesByCategory = make(map[string]int)

	// Basic stats
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT total_score FROM users WHERE id = $1`, uid).Scan(&stats.TotalScore)

	// Rank
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) + 1 FROM users WHERE total_score > $1 AND role != 'admin'`,
		stats.TotalScore).Scan(&stats.Rank)

	// Total solves
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM solves WHERE user_id = $1`, uid).Scan(&stats.TotalSolves)

	// Total challenges solved
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT f.challenge_id) FROM solves s
		 JOIN flags f ON s.flag_id = f.id WHERE s.user_id = $1`, uid).Scan(&stats.TotalChallenges)

	// Total attempts
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM flag_attempts WHERE user_id = $1`, uid).Scan(&stats.TotalAttempts)

	// Hints unlocked
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(points_spent), 0) FROM hint_unlocks WHERE user_id = $1`,
		uid).Scan(&stats.HintsUnlocked, &stats.PointsSpentOnHints)

	// Solves by difficulty
	rows, _ := h.db.Pool.Query(c.Request.Context(),
		`SELECT c.difficulty, COUNT(DISTINCT c.id)
		 FROM solves s
		 JOIN flags f ON s.flag_id = f.id
		 JOIN challenges c ON f.challenge_id = c.id
		 WHERE s.user_id = $1
		 GROUP BY c.difficulty`, uid)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var diff string
			var count int
			rows.Scan(&diff, &count)
			stats.SolvesByDifficulty[diff] = count
		}
	}

	// Solves by category
	rows, _ = h.db.Pool.Query(c.Request.Context(),
		`SELECT COALESCE(cat.name, 'Uncategorized'), COUNT(DISTINCT c.id)
		 FROM solves s
		 JOIN flags f ON s.flag_id = f.id
		 JOIN challenges c ON f.challenge_id = c.id
		 LEFT JOIN categories cat ON c.category_id = cat.id
		 WHERE s.user_id = $1
		 GROUP BY cat.name`, uid)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var cat string
			var count int
			rows.Scan(&cat, &count)
			stats.SolvesByCategory[cat] = count
		}
	}

	// Recent activity
	activityQuery := `
		SELECT 'solve' as type, c.id, c.name, f.name, s.points_awarded, s.solved_at
		FROM solves s
		JOIN flags f ON s.flag_id = f.id
		JOIN challenges c ON f.challenge_id = c.id
		WHERE s.user_id = $1
		ORDER BY s.solved_at DESC
		LIMIT 10
	`
	rows, _ = h.db.Pool.Query(c.Request.Context(), activityQuery, uid)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var activity ActivityItem
			var flagName string
			var timestamp time.Time
			rows.Scan(&activity.Type, &activity.ChallengeID, &activity.ChallengeName,
				&flagName, &activity.Points, &timestamp)
			activity.FlagName = &flagName
			activity.Timestamp = timestamp.Unix()
			stats.RecentActivity = append(stats.RecentActivity, activity)
		}
	}

	if stats.RecentActivity == nil {
		stats.RecentActivity = []ActivityItem{}
	}

	c.JSON(http.StatusOK, stats)
}

// GetSolves returns the user's solve history
func (h *UserHandler) GetSolves(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	query := `
		SELECT s.id, c.id, c.name, c.slug, f.id, f.name, s.points_awarded, s.solved_at
		FROM solves s
		JOIN flags f ON s.flag_id = f.id
		JOIN challenges c ON f.challenge_id = c.id
		WHERE s.user_id = $1
		ORDER BY s.solved_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query, uid)
	if err != nil {
		h.logger.Error("failed to get solves", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch solves"})
		return
	}
	defer rows.Close()

	var solves []SolveResponse
	for rows.Next() {
		var s SolveResponse
		var solvedAt time.Time
		if err := rows.Scan(&s.ID, &s.ChallengeID, &s.ChallengeName, &s.ChallengeSlug,
			&s.FlagID, &s.FlagName, &s.Points, &solvedAt); err != nil {
			continue
		}
		s.SolvedAt = solvedAt.Unix()
		solves = append(solves, s)
	}

	if solves == nil {
		solves = []SolveResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"solves": solves,
		"total":  len(solves),
	})
}
