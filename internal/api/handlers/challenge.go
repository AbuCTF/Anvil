package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// hashFlagForComparison creates a SHA256 hash of the flag for comparison
func hashFlagForComparison(flag string) string {
	hash := sha256.Sum256([]byte(flag))
	return hex.EncodeToString(hash[:])
}

// ChallengeService handles challenge-related operations
type ChallengeService struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

// NewChallengeService creates a new challenge service
func NewChallengeService(cfg *config.Config, db *database.DB, logger *zap.Logger) *ChallengeService {
	return &ChallengeService{config: cfg, db: db, logger: logger}
}

// ChallengeListResponse represents the challenge list response
type ChallengeListResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	Description  *string `json:"description,omitempty"`
	Difficulty   string  `json:"difficulty"`
	Category     *string `json:"category,omitempty"`
	CategoryID   *string `json:"category_id,omitempty"`
	BasePoints   int     `json:"base_points"`
	TotalSolves  int     `json:"total_solves"`
	TotalFlags   int     `json:"total_flags"`
	AuthorName   *string `json:"author_name,omitempty"`
	IsSolved     bool    `json:"is_solved"`
	UserSolves   int     `json:"user_solves"`   // Flags solved by this user
	ResourceType string  `json:"resource_type"` // docker or vm
}

// ChallengeDetailResponse includes more details for single challenge view
type ChallengeDetailResponse struct {
	ChallengeListResponse
	ExposedPorts    []models.ExposedPort `json:"exposed_ports"`
	Flags           []FlagResponse       `json:"flags"`
	Hints           []HintResponse       `json:"hints"`
	ReleaseDate     *time.Time           `json:"release_date,omitempty"`
	InstanceTimeout *int                 `json:"instance_timeout,omitempty"`
	MaxExtensions   *int                 `json:"max_extensions,omitempty"`
	Status          string               `json:"status"` // draft, published, archived
}

// FlagResponse represents a flag in the response
type FlagResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Points      int    `json:"points"`
	Order       int    `json:"order"`
	IsSolved    bool   `json:"is_solved"`
	SolvedAt    *int64 `json:"solved_at,omitempty"` // Unix timestamp
	TotalSolves int    `json:"total_solves"`
}

// HintResponse represents a hint in the response
type HintResponse struct {
	ID         string  `json:"id"`
	Content    *string `json:"content,omitempty"` // Only shown if unlocked
	Cost       int     `json:"cost"`
	Order      int     `json:"order"`
	IsUnlocked bool    `json:"is_unlocked"`
}

// List returns all published challenges
func (h *ChallengeHandler) List(c *gin.Context) {
	// Get user ID if authenticated
	var userID *uuid.UUID
	if id, exists := c.Get("user_id"); exists {
		uid, _ := uuid.Parse(id.(string))
		userID = &uid
	}

	// Query published challenges
	query := `
		SELECT 
			c.id, c.name, c.slug, c.description, c.difficulty,
			c.base_points, c.total_solves, c.total_flags, c.author_name,
			c.resource_type, cat.id as category_id, cat.name as category_name
		FROM challenges c
		LEFT JOIN categories cat ON c.category_id = cat.id
		WHERE c.status = 'published'
		ORDER BY c.created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to list challenges", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenges"})
		return
	}
	defer rows.Close()

	var challenges []ChallengeListResponse
	for rows.Next() {
		var ch ChallengeListResponse
		var categoryID, categoryName *string

		if err := rows.Scan(
			&ch.ID, &ch.Name, &ch.Slug, &ch.Description, &ch.Difficulty,
			&ch.BasePoints, &ch.TotalSolves, &ch.TotalFlags, &ch.AuthorName,
			&ch.ResourceType, &categoryID, &categoryName,
		); err != nil {
			h.logger.Error("failed to scan challenge", zap.Error(err))
			continue
		}

		ch.CategoryID = categoryID
		ch.Category = categoryName

		// Check if user has solved any flags
		if userID != nil {
			var solveCount int
			solveQuery := `
				SELECT COUNT(*) FROM solves s
				JOIN flags f ON s.flag_id = f.id
				WHERE s.user_id = $1 AND f.challenge_id = $2
			`
			h.db.Pool.QueryRow(c.Request.Context(), solveQuery, userID, ch.ID).Scan(&solveCount)
			ch.UserSolves = solveCount
			ch.IsSolved = solveCount >= ch.TotalFlags && ch.TotalFlags > 0
		}

		challenges = append(challenges, ch)
	}

	if challenges == nil {
		challenges = []ChallengeListResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"challenges": challenges,
		"total":      len(challenges),
	})
}

// Get returns a single challenge by slug
func (h *ChallengeHandler) Get(c *gin.Context) {
	slug := c.Param("slug")

	// Get user ID if authenticated
	var userID *uuid.UUID
	var userRole string
	if id, exists := c.Get("user_id"); exists {
		if uid, ok := id.(uuid.UUID); ok {
			userID = &uid
		} else if uidStr, ok := id.(string); ok {
			uid, _ := uuid.Parse(uidStr)
			userID = &uid
		}
	}
	if role, exists := c.Get("role"); exists {
		userRole = role.(string)
	}

	// Query challenge - allow admins to see all challenges, others only published
	var statusCondition string
	if userRole == "admin" {
		statusCondition = "(c.status = 'published' OR c.status = 'draft')"
	} else {
		statusCondition = "c.status = 'published'"
	}

	query := `
		SELECT 
			c.id, c.name, c.slug, c.description, c.difficulty,
			c.base_points, c.total_solves, c.total_flags, c.author_name,
			c.exposed_ports, c.instance_timeout, c.max_extensions, c.release_date,
			c.resource_type, c.status,
			cat.id as category_id, cat.name as category_name
		FROM challenges c
		LEFT JOIN categories cat ON c.category_id = cat.id
		WHERE c.slug = $1 AND ` + statusCondition

	var ch ChallengeDetailResponse
	var categoryID, categoryName *string
	var exposedPortsJSON []byte

	err := h.db.Pool.QueryRow(c.Request.Context(), query, slug).Scan(
		&ch.ID, &ch.Name, &ch.Slug, &ch.Description, &ch.Difficulty,
		&ch.BasePoints, &ch.TotalSolves, &ch.TotalFlags, &ch.AuthorName,
		&exposedPortsJSON, &ch.InstanceTimeout, &ch.MaxExtensions, &ch.ReleaseDate,
		&ch.ResourceType, &ch.Status,
		&categoryID, &categoryName,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to get challenge", zap.Error(err), zap.String("slug", slug))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		return
	}

	ch.CategoryID = categoryID
	ch.Category = categoryName

	// Parse exposed ports
	if len(exposedPortsJSON) > 0 {
		ch.ExposedPorts = []models.ExposedPort{}
		// JSON unmarshal would be done here
	}

	// Get flags
	flagsQuery := `
		SELECT id, name, points, sort_order,
			(SELECT COUNT(*) FROM solves WHERE flag_id = flags.id) as total_solves
		FROM flags
		WHERE challenge_id = $1
		ORDER BY sort_order
	`
	flagRows, err := h.db.Pool.Query(c.Request.Context(), flagsQuery, ch.ID)
	if err == nil {
		defer flagRows.Close()
		for flagRows.Next() {
			var f FlagResponse
			if err := flagRows.Scan(&f.ID, &f.Name, &f.Points, &f.Order, &f.TotalSolves); err != nil {
				continue
			}

			// Check if user solved this flag
			if userID != nil {
				var solvedAt *time.Time
				solveQuery := `SELECT solved_at FROM solves WHERE user_id = $1 AND flag_id = $2`
				if err := h.db.Pool.QueryRow(c.Request.Context(), solveQuery, userID, f.ID).Scan(&solvedAt); err == nil && solvedAt != nil {
					f.IsSolved = true
					ts := solvedAt.Unix()
					f.SolvedAt = &ts
				}
			}
			ch.Flags = append(ch.Flags, f)
		}
	}

	// Get hints
	hintsQuery := `
		SELECT id, content, cost, sort_order
		FROM hints
		WHERE challenge_id = $1
		ORDER BY sort_order
	`
	hintRows, err := h.db.Pool.Query(c.Request.Context(), hintsQuery, ch.ID)
	if err == nil {
		defer hintRows.Close()
		for hintRows.Next() {
			var hint HintResponse
			var content string
			if err := hintRows.Scan(&hint.ID, &content, &hint.Cost, &hint.Order); err != nil {
				continue
			}

			// Check if user unlocked this hint
			if userID != nil {
				var unlocked bool
				unlockQuery := `SELECT EXISTS(SELECT 1 FROM hint_unlocks WHERE user_id = $1 AND hint_id = $2)`
				h.db.Pool.QueryRow(c.Request.Context(), unlockQuery, userID, hint.ID).Scan(&unlocked)
				hint.IsUnlocked = unlocked
				if unlocked {
					hint.Content = &content
				}
			}
			ch.Hints = append(ch.Hints, hint)
		}
	}

	// Check overall solve status
	if userID != nil {
		var solveCount int
		h.db.Pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM solves s JOIN flags f ON s.flag_id = f.id WHERE s.user_id = $1 AND f.challenge_id = $2`,
			userID, ch.ID).Scan(&solveCount)
		ch.UserSolves = solveCount
		ch.IsSolved = solveCount >= ch.TotalFlags && ch.TotalFlags > 0
	}

	c.JSON(http.StatusOK, ch)
}

// GetFlags returns flag information for a challenge
func (h *ChallengeHandler) GetFlags(c *gin.Context) {
	slug := c.Param("slug")

	// Get user ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	// Get challenge ID
	var challengeID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges WHERE slug = $1 AND status = 'published'`, slug).Scan(&challengeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// Get flags
	query := `
		SELECT f.id, f.name, f.points, f.sort_order,
			(SELECT COUNT(*) FROM solves WHERE flag_id = f.id) as total_solves,
			s.solved_at
		FROM flags f
		LEFT JOIN solves s ON f.id = s.flag_id AND s.user_id = $1
		WHERE f.challenge_id = $2
		ORDER BY f.sort_order
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query, uid, challengeID)
	if err != nil {
		h.logger.Error("failed to get flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
		return
	}
	defer rows.Close()

	var flags []FlagResponse
	for rows.Next() {
		var f FlagResponse
		var solvedAt *time.Time
		if err := rows.Scan(&f.ID, &f.Name, &f.Points, &f.Order, &f.TotalSolves, &solvedAt); err != nil {
			continue
		}
		if solvedAt != nil {
			f.IsSolved = true
			ts := solvedAt.Unix()
			f.SolvedAt = &ts
		}
		flags = append(flags, f)
	}

	if flags == nil {
		flags = []FlagResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"flags": flags})
}

// SubmitFlagRequest represents the flag submission request
type SubmitFlagRequest struct {
	Flag string `json:"flag" binding:"required"`
}

// SubmitFlag handles flag submission
func (h *ChallengeHandler) SubmitFlag(c *gin.Context) {
	slug := c.Param("slug")

	// Get user ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	var req SubmitFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required"})
		return
	}

	// Get challenge
	var challengeID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges WHERE slug = $1 AND status = 'published'`, slug).Scan(&challengeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// Normalize flag (trim whitespace)
	submittedFlag := strings.TrimSpace(req.Flag)

	// Find matching flag
	query := `
		SELECT f.id, f.flag_hash, f.name, f.points, f.case_sensitive
		FROM flags f
		WHERE f.challenge_id = $1
	`
	rows, err := h.db.Pool.Query(c.Request.Context(), query, challengeID)
	if err != nil {
		h.logger.Error("failed to query flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}
	defer rows.Close()

	var matchedFlag struct {
		ID            string
		FlagHash      string
		Name          string
		Points        int
		CaseSensitive bool
	}
	found := false

	for rows.Next() {
		var f struct {
			ID            string
			FlagHash      string
			Name          string
			Points        int
			CaseSensitive bool
		}
		if err := rows.Scan(&f.ID, &f.FlagHash, &f.Name, &f.Points, &f.CaseSensitive); err != nil {
			continue
		}

		// Compare flags by hashing submitted flag and comparing hashes
		var submittedHash string
		if f.CaseSensitive {
			submittedHash = hashFlagForComparison(submittedFlag)
		} else {
			submittedHash = hashFlagForComparison(strings.ToLower(submittedFlag))
		}

		match := subtle.ConstantTimeCompare([]byte(submittedHash), []byte(f.FlagHash)) == 1

		if match {
			matchedFlag = f
			found = true
			break
		}
	}

	// Record attempt
	attemptID := uuid.New()
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO flag_attempts (id, user_id, challenge_id, submitted_flag, is_correct, created_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		attemptID, uid, challengeID, submittedFlag, found)
	if err != nil {
		h.logger.Warn("failed to record attempt", zap.Error(err))
	}

	// Update attempt count
	h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET total_attempts = total_attempts + 1 WHERE id = $1`, challengeID)

	if !found {
		c.JSON(http.StatusOK, gin.H{
			"correct": false,
			"message": "Incorrect flag. Try again!",
		})
		return
	}

	// Check if already solved
	var alreadySolved bool
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM solves WHERE user_id = $1 AND flag_id = $2)`,
		uid, matchedFlag.ID).Scan(&alreadySolved)

	if err != nil {
		h.logger.Warn("failed to check solve status", zap.Error(err))
		// Continue anyway, ON CONFLICT will handle it
		alreadySolved = false
	}

	if alreadySolved {
		c.JSON(http.StatusOK, gin.H{
			"correct":        true,
			"already_solved": true,
			"message":        "Correct! But you've already solved this flag.",
			"flag_name":      matchedFlag.Name,
			"points":         0,
		})
		return
	}

	// Record solve (with ON CONFLICT to handle race conditions)
	solveID := uuid.New()
	result, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO solves (id, user_id, flag_id, points_awarded, solved_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, flag_id) DO NOTHING`,
		solveID, uid, matchedFlag.ID, matchedFlag.Points)

	if err != nil {
		h.logger.Error("failed to record solve", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	// Check if row was actually inserted (RowsAffected=0 means conflict/already existed)
	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusOK, gin.H{
			"correct":        true,
			"already_solved": true,
			"message":        "Correct! But you've already solved this flag.",
			"flag_name":      matchedFlag.Name,
			"points":         0,
		})
		return
	}

	// Update user's total score (only if actually inserted)
	h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET total_score = total_score + $1, updated_at = NOW() WHERE id = $2`,
		matchedFlag.Points, uid)

	// Update challenge solve count
	h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET total_solves = (
			SELECT COUNT(DISTINCT user_id) FROM solves s
			JOIN flags f ON s.flag_id = f.id
			WHERE f.challenge_id = $1
		) WHERE id = $1`, challengeID)

	// Check if all flags solved (first blood check)
	var totalFlags, solvedFlags int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT total_flags FROM challenges WHERE id = $1`, challengeID).Scan(&totalFlags)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM solves s JOIN flags f ON s.flag_id = f.id
		 WHERE s.user_id = $1 AND f.challenge_id = $2`, uid, challengeID).Scan(&solvedFlags)

	response := gin.H{
		"correct":      true,
		"message":      "Correct! Flag captured!",
		"flag_name":    matchedFlag.Name,
		"points":       matchedFlag.Points,
		"fully_solved": solvedFlags >= totalFlags,
		"solved_flags": solvedFlags,
		"total_flags":  totalFlags,
	}

	c.JSON(http.StatusOK, response)
}

// GetHints returns hints for a challenge
func (h *ChallengeHandler) GetHints(c *gin.Context) {
	slug := c.Param("slug")

	// Get user ID if authenticated
	var userID *uuid.UUID
	if id, exists := c.Get("user_id"); exists {
		uid, _ := uuid.Parse(id.(string))
		userID = &uid
	}

	// Get challenge ID
	var challengeID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges WHERE slug = $1 AND status = 'published'`, slug).Scan(&challengeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// Get hints
	query := `
		SELECT id, content, cost, sort_order
		FROM hints
		WHERE challenge_id = $1
		ORDER BY sort_order
	`
	rows, err := h.db.Pool.Query(c.Request.Context(), query, challengeID)
	if err != nil {
		h.logger.Error("failed to get hints", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
		return
	}
	defer rows.Close()

	var hints []HintResponse
	for rows.Next() {
		var hint HintResponse
		var content string
		if err := rows.Scan(&hint.ID, &content, &hint.Cost, &hint.Order); err != nil {
			continue
		}

		// Check if unlocked
		if userID != nil {
			var unlocked bool
			h.db.Pool.QueryRow(c.Request.Context(),
				`SELECT EXISTS(SELECT 1 FROM hint_unlocks WHERE user_id = $1 AND hint_id = $2)`,
				userID, hint.ID).Scan(&unlocked)
			hint.IsUnlocked = unlocked
			if unlocked {
				hint.Content = &content
			}
		}
		hints = append(hints, hint)
	}

	if hints == nil {
		hints = []HintResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"hints": hints})
}

// UnlockHint unlocks a hint for the user
func (h *ChallengeHandler) UnlockHint(c *gin.Context) {
	slug := c.Param("slug")
	hintID := c.Param("hint_id")

	// Get user ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)
	hid, err := uuid.Parse(hintID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hint ID"})
		return
	}

	// Verify challenge and hint exist
	var challengeID string
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges WHERE slug = $1 AND status = 'published'`, slug).Scan(&challengeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// Get hint info
	var hintCost int
	var hintContent string
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT cost, content FROM hints WHERE id = $1 AND challenge_id = $2`,
		hid, challengeID).Scan(&hintCost, &hintContent)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "hint not found"})
		return
	}

	// Check if already unlocked
	var alreadyUnlocked bool
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM hint_unlocks WHERE user_id = $1 AND hint_id = $2)`,
		uid, hid).Scan(&alreadyUnlocked)

	if alreadyUnlocked {
		c.JSON(http.StatusOK, gin.H{
			"content":          hintContent,
			"already_unlocked": true,
		})
		return
	}

	// Get user's score
	var userScore int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT total_score FROM users WHERE id = $1`, uid).Scan(&userScore)

	if userScore < hintCost {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":          "insufficient points",
			"required":       hintCost,
			"current_points": userScore,
		})
		return
	}

	// Deduct points and record unlock
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "transaction failed"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	// Deduct points
	_, err = tx.Exec(c.Request.Context(),
		`UPDATE users SET total_score = total_score - $1, updated_at = NOW() WHERE id = $2`,
		hintCost, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deduct points"})
		return
	}

	// Record unlock
	unlockID := uuid.New()
	_, err = tx.Exec(c.Request.Context(),
		`INSERT INTO hint_unlocks (id, user_id, hint_id, points_spent, unlocked_at)
		 VALUES ($1, $2, $3, $4, NOW())`,
		unlockID, uid, hid, hintCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"content":          hintContent,
		"points_spent":     hintCost,
		"remaining_points": userScore - hintCost,
	})
}
