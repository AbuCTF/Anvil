package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func currentUserRank(ctx context.Context, db *database.DB, userID uuid.UUID) (int, error) {
	var rank int
	err := db.Pool.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT ranked.position FROM (
				SELECT candidate.id,
					ROW_NUMBER() OVER (
						ORDER BY COALESCE(candidate.total_score, 0) DESC,
							(SELECT MAX(solved_at) FROM solves WHERE user_id = candidate.id) ASC NULLS LAST,
							candidate.created_at ASC, candidate.id ASC
					) AS position
				FROM users candidate
				WHERE candidate.role != 'admin' AND candidate.status = 'active'
			) ranked WHERE ranked.id = $1
		), 0)
	`, userID).Scan(&rank)
	return rank, err
}

func userRankETag(rank int) string {
	return `"rank-` + strconv.Itoa(rank) + `"`
}

// etag lets a returning tab skip re-downloading an unchanged rank.
func (h *UserHandler) GetRank(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	rank, err := currentUserRank(c.Request.Context(), h.db, uid)
	if err != nil {
		h.logger.Error("failed to get current user rank", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch rank"})
		return
	}

	etag := userRankETag(rank)
	c.Header("Cache-Control", "private, no-cache")
	c.Header("ETag", etag)
	c.Header("Vary", "Authorization")
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rank": rank})
}

type UserService struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewUserService(cfg *config.Config, db *database.DB, logger *zap.Logger) *UserService {
	return &UserService{config: cfg, db: db, logger: logger}
}

type UserProfileResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	Email           *string `json:"email,omitempty"`
	DisplayName     *string `json:"display_name,omitempty"`
	Role            string  `json:"role"`
	TotalScore      int     `json:"total_score"`
	Rank            int     `json:"rank"`
	Bio             *string `json:"bio,omitempty"`
	JoinedAt        int64   `json:"joined_at"`
	TotalSolves     int     `json:"total_solves"`
	TotalChallenges int     `json:"total_challenges"`
}

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

type ActivityItem struct {
	Type          string  `json:"type"` // solve, hint_unlock
	ChallengeID   string  `json:"challenge_id"`
	ChallengeName string  `json:"challenge_name"`
	FlagName      *string `json:"flag_name,omitempty"`
	Points        int     `json:"points"`
	Timestamp     int64   `json:"timestamp"`
}

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

func (h *UserHandler) GetProfile(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var profile UserProfileResponse
	var createdAt time.Time

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT u.id,
			u.username,
			u.email,
			u.display_name,
			u.role,
			COALESCE(u.total_score, 0),
			u.bio,
			u.created_at,
			COALESCE((
				SELECT ranked.position FROM (
					SELECT candidate.id,
						ROW_NUMBER() OVER (
							ORDER BY COALESCE(candidate.total_score, 0) DESC,
								(SELECT MAX(solved_at) FROM solves WHERE user_id = candidate.id) ASC NULLS LAST,
								candidate.created_at ASC, candidate.id ASC
						) AS position
					FROM users candidate
					WHERE candidate.role != 'admin' AND candidate.status = 'active'
				) ranked WHERE ranked.id = u.id
			), 0),
			(SELECT COUNT(*) FROM solves s WHERE s.user_id = u.id),
			(SELECT COUNT(DISTINCT f.challenge_id)
			 FROM solves s
			 JOIN flags f ON s.flag_id = f.id
			 WHERE s.user_id = u.id)
		 FROM users u
		 WHERE u.id = $1`, uid).Scan(
		&profile.ID,
		&profile.Username,
		&profile.Email,
		&profile.DisplayName,
		&profile.Role,
		&profile.TotalScore,
		&profile.Bio,
		&createdAt,
		&profile.Rank,
		&profile.TotalSolves,
		&profile.TotalChallenges,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to get user profile", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch profile"})
		return
	}

	profile.JoinedAt = createdAt.Unix()

	c.JSON(http.StatusOK, profile)
}

type UpdateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Bio         *string `json:"bio"`
}

const (
	maxProfileRequestBytes = 16 << 10
	maxDisplayNameRunes    = 100
	maxBioRunes            = 2000
)

type normalizedProfileUpdate struct {
	displayName    string
	bio            string
	setDisplayName bool
	setBio         bool
}

func normalizeProfileUpdate(req UpdateProfileRequest) (normalizedProfileUpdate, error) {
	update := normalizedProfileUpdate{
		setDisplayName: req.DisplayName != nil,
		setBio:         req.Bio != nil,
	}
	if !update.setDisplayName && !update.setBio {
		return normalizedProfileUpdate{}, fmt.Errorf("at least one profile field is required")
	}
	if update.setDisplayName {
		update.displayName = strings.TrimSpace(*req.DisplayName)
		if utf8.RuneCountInString(update.displayName) > maxDisplayNameRunes {
			return normalizedProfileUpdate{}, fmt.Errorf("display_name must be at most %d characters", maxDisplayNameRunes)
		}
	}
	if update.setBio {
		update.bio = strings.TrimSpace(*req.Bio)
		if utf8.RuneCountInString(update.bio) > maxBioRunes {
			return normalizedProfileUpdate{}, fmt.Errorf("bio must be at most %d characters", maxBioRunes)
		}
	}
	return update, nil
}

func (h *UserHandler) UpdateProfile(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileRequestBytes)
	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	update, err := normalizeProfileUpdate(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE users
		SET display_name = CASE WHEN $1 THEN NULLIF($2::text, '') ELSE display_name END,
			bio = CASE WHEN $3 THEN NULLIF($4::text, '') ELSE bio END,
			updated_at = NOW()
		WHERE id = $5
	`, update.setDisplayName, update.displayName, update.setBio, update.bio, uid)
	if err != nil {
		h.logger.Error("failed to update profile", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("profile update affected an unexpected number of rows",
			zap.String("user_id", uid.String()),
			zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "profile updated"})
}

func (h *UserHandler) GetStats(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var stats UserStatsResponse
	stats.SolvesByDifficulty = make(map[string]int)
	stats.SolvesByCategory = make(map[string]int)

	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COALESCE(u.total_score, 0),
			COALESCE((
				SELECT ranked.position FROM (
					SELECT candidate.id,
						ROW_NUMBER() OVER (
							ORDER BY COALESCE(candidate.total_score, 0) DESC,
								(SELECT MAX(solved_at) FROM solves WHERE user_id = candidate.id) ASC NULLS LAST,
								candidate.created_at ASC, candidate.id ASC
						) AS position
					FROM users candidate
					WHERE candidate.role != 'admin' AND candidate.status = 'active'
				) ranked WHERE ranked.id = u.id
			), 0),
			(SELECT COUNT(*) FROM solves s WHERE s.user_id = u.id),
			(SELECT COUNT(DISTINCT f.challenge_id)
			 FROM solves s
			 JOIN flags f ON s.flag_id = f.id
			 WHERE s.user_id = u.id),
			(SELECT COUNT(*) FROM flag_attempts a WHERE a.user_id = u.id),
			(SELECT COUNT(*) FROM hint_unlocks hu WHERE hu.user_id = u.id),
			(SELECT COALESCE(SUM(hu.points_deducted), 0) FROM hint_unlocks hu WHERE hu.user_id = u.id)
		FROM users u
		WHERE u.id = $1
	`, uid).Scan(
		&stats.TotalScore,
		&stats.Rank,
		&stats.TotalSolves,
		&stats.TotalChallenges,
		&stats.TotalAttempts,
		&stats.HintsUnlocked,
		&stats.PointsSpentOnHints,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load user stats", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT c.difficulty, COUNT(DISTINCT c.id)
		 FROM solves s
		 JOIN flags f ON s.flag_id = f.id
		 JOIN challenges c ON f.challenge_id = c.id
		 WHERE s.user_id = $1
		 GROUP BY c.difficulty`, uid)
	if err != nil {
		h.logger.Error("failed to load solves by difficulty", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}
	for rows.Next() {
		var difficulty string
		var count int
		if err := rows.Scan(&difficulty, &count); err != nil {
			rows.Close()
			h.logger.Error("failed to scan solves by difficulty", zap.String("user_id", uid.String()), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
			return
		}
		stats.SolvesByDifficulty[difficulty] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		h.logger.Error("failed while reading solves by difficulty", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}
	rows.Close()

	rows, err = h.db.Pool.Query(c.Request.Context(),
		`SELECT COALESCE(cat.name, 'Uncategorized'), COUNT(DISTINCT c.id)
		 FROM solves s
		 JOIN flags f ON s.flag_id = f.id
		 JOIN challenges c ON f.challenge_id = c.id
		 LEFT JOIN categories cat ON c.category_id = cat.id
		 WHERE s.user_id = $1
		 GROUP BY cat.name`, uid)
	if err != nil {
		h.logger.Error("failed to load solves by category", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			rows.Close()
			h.logger.Error("failed to scan solves by category", zap.String("user_id", uid.String()), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
			return
		}
		stats.SolvesByCategory[category] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		h.logger.Error("failed while reading solves by category", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}
	rows.Close()

	activityQuery := `
		SELECT 'solve' as type, c.id, c.name, f.name, s.points_awarded, s.solved_at
		FROM solves s
		JOIN flags f ON s.flag_id = f.id
		JOIN challenges c ON f.challenge_id = c.id
		WHERE s.user_id = $1
		ORDER BY s.solved_at DESC
		LIMIT 10
	`
	rows, err = h.db.Pool.Query(c.Request.Context(), activityQuery, uid)
	if err != nil {
		h.logger.Error("failed to load recent user activity", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}
	for rows.Next() {
		var activity ActivityItem
		var flagName string
		var timestamp time.Time
		if err := rows.Scan(&activity.Type, &activity.ChallengeID, &activity.ChallengeName,
			&flagName, &activity.Points, &timestamp); err != nil {
			rows.Close()
			h.logger.Error("failed to scan recent user activity", zap.String("user_id", uid.String()), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
			return
		}
		activity.FlagName = &flagName
		activity.Timestamp = timestamp.Unix()
		stats.RecentActivity = append(stats.RecentActivity, activity)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		h.logger.Error("failed while reading recent user activity", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user stats"})
		return
	}
	rows.Close()

	if stats.RecentActivity == nil {
		stats.RecentActivity = []ActivityItem{}
	}

	c.JSON(http.StatusOK, stats)
}

func (h *UserHandler) GetSolves(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var userExists bool
	if err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, uid).Scan(&userExists); err != nil {
		h.logger.Error("failed to check user before loading solves", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch solves"})
		return
	}
	if !userExists {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

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
			h.logger.Error("failed to scan user solve", zap.String("user_id", uid.String()), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch solves"})
			return
		}
		s.SolvedAt = solvedAt.Unix()
		solves = append(solves, s)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while reading user solves", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch solves"})
		return
	}

	if solves == nil {
		solves = []SolveResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"solves": solves,
		"total":  len(solves),
	})
}
