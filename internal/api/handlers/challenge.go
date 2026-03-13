package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
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
	Attachments     []AttachmentResponse `json:"attachments"`
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
		if uid, ok := id.(uuid.UUID); ok {
			userID = &uid
		} else if uidStr, ok := id.(string); ok {
			if uid, err := uuid.Parse(uidStr); err == nil {
				userID = &uid
			}
		}
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
			if uid, err := uuid.Parse(uidStr); err == nil {
				userID = &uid
			}
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

	// Attach file attachments
	if h.attachmentHdlr != nil {
		if attachments, err := h.attachmentHdlr.ListPublic(c, ch.ID); err == nil {
			ch.Attachments = attachments
		}
	}
	if ch.Attachments == nil {
		ch.Attachments = []AttachmentResponse{}
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

	// ── Brute-force lockout check ─────────────────────────────────────────────
	// After flagLockoutThreshold consecutive wrong attempts in flagLockoutWindow,
	// the user is locked out for flagLockoutDuration.
	const (
		flagLockoutThreshold = 10
		flagLockoutWindow    = 5 * time.Minute
		flagLockoutDuration  = 10 * time.Minute
	)
	var lockedUntil *time.Time
	var wrongAttempts int
	var firstAttemptAt time.Time
	lockRow := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT wrong_attempts, first_attempt_at, locked_until
		 FROM flag_attempt_lockouts
		 WHERE user_id = $1 AND challenge_id = $2`,
		uid, challengeID,
	)
	lockErr := lockRow.Scan(&wrongAttempts, &firstAttemptAt, &lockedUntil)
	if lockErr == nil && lockedUntil != nil && time.Now().Before(*lockedUntil) {
		retryAfter := int(time.Until(*lockedUntil).Seconds()) + 1
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       "Too many incorrect attempts. Please wait before trying again.",
			"locked_until": lockedUntil.Unix(),
			"retry_after":  retryAfter,
		})
		return
	}
	// Reset window if the last tracking window has expired
	if lockErr == nil && time.Since(firstAttemptAt) > flagLockoutWindow {
		if _, delErr := h.db.Pool.Exec(c.Request.Context(),
			`DELETE FROM flag_attempt_lockouts WHERE user_id = $1 AND challenge_id = $2`,
			uid, challengeID,
		); delErr != nil {
			h.logger.Warn("failed to reset lockout tracking", zap.Error(delErr))
		}
		wrongAttempts = 0
	}

	// Normalize flag (trim whitespace)
	submittedFlag := strings.TrimSpace(req.Flag)

	// ── Static flag check ────────────────────────────────────────────────────
	// Query static flags and compare by hashing the submitted value.
	query := `
		SELECT f.id, f.flag_hash, f.name, f.points, f.case_sensitive
		FROM flags f
		WHERE f.challenge_id = $1 AND f.flag_type = 'static'
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

		var submittedHash string
		if f.CaseSensitive {
			submittedHash = hashFlagForComparison(submittedFlag)
		} else {
			submittedHash = hashFlagForComparison(strings.ToLower(submittedFlag))
		}

		if subtle.ConstantTimeCompare([]byte(submittedHash), []byte(f.FlagHash)) == 1 {
			matchedFlag = f
			found = true
			break
		}
	}
	rows.Close()

	// ── Regex flag check ─────────────────────────────────────────────────────
	// Container generates its own dynamic flag; admin defines a regex pattern.
	// Any submission matching the regex is correct. Duplicate values across
	// users trigger a silent flag-share event.
	if !found {
		regexRows, regexErr := h.db.Pool.Query(c.Request.Context(),
			`SELECT f.id, f.flag_hash, f.name, f.points
			   FROM flags f
			  WHERE f.challenge_id = $1 AND f.flag_type = 'regex'`,
			challengeID)
		if regexErr == nil {
			for regexRows.Next() {
				var fID, pattern, fName string
				var fPoints int
				if err := regexRows.Scan(&fID, &pattern, &fName, &fPoints); err != nil {
					continue
				}
				re, err := regexp.Compile(pattern)
				if err != nil {
					h.logger.Warn("invalid regex flag pattern", zap.String("flag_id", fID), zap.Error(err))
					continue
				}
				if re.MatchString(submittedFlag) {
					matchedFlag.ID = fID
					matchedFlag.Name = fName
					matchedFlag.Points = fPoints
					found = true

					// Flag share detection: same exact value previously submitted by another user
					var priorUserID string
					shareErr := h.db.Pool.QueryRow(c.Request.Context(),
						`SELECT user_id FROM flag_attempts
						  WHERE submitted_flag = $1 AND challenge_id = $2
						    AND is_correct = true AND user_id != $3
						  LIMIT 1`,
						submittedFlag, challengeID, uid,
					).Scan(&priorUserID)
					if shareErr == nil {
						_, logErr := h.db.Pool.Exec(c.Request.Context(),
							`INSERT INTO flag_share_events
								(id, challenge_id, flag_id, owner_user_id, owner_instance_id,
								 submitter_user_id, flag_value, submitter_ip, created_at)
							 VALUES
								(uuid_generate_v4(), $1, $2, $3, NULL, $4, $5, $6, NOW())`,
							challengeID, fID, priorUserID, uid, submittedFlag, c.ClientIP())
						if logErr != nil {
							h.logger.Warn("failed to log regex flag share event", zap.Error(logErr))
						} else {
							h.logger.Warn("FLAG SHARE DETECTED (regex)",
								zap.String("submitter", uid.String()),
								zap.String("prior_user", priorUserID),
								zap.String("challenge_id", challengeID),
								zap.String("flag_value", submittedFlag),
							)
						}
					}
					break
				}
			}
			regexRows.Close()
		}
	}

	// ── Dynamic flag check ───────────────────────────────────────────────────
	// 1. Look for a flag generated for THIS user → clean solve.
	// 2. If not found, check if the value exists for ANY other user's instance
	//    → flag share detected: still grant the solve (silent detection) and
	//      log a flag_share_events record for admin review.
	if !found {
		var dynFlagID, dynFlagName string
		var dynPoints int
		err := h.db.Pool.QueryRow(c.Request.Context(),
			`SELECT f.id, f.name, f.points
			   FROM instance_flags inf
			   JOIN flags f ON f.id = inf.flag_id
			  WHERE inf.user_id      = $1
			    AND inf.challenge_id = $2
			    AND inf.flag_value   = $3`,
			uid, challengeID, submittedFlag,
		).Scan(&dynFlagID, &dynFlagName, &dynPoints)
		if err == nil {
			// Legit: flag belongs to this user
			matchedFlag.ID = dynFlagID
			matchedFlag.Name = dynFlagName
			matchedFlag.Points = dynPoints
			found = true
		} else {
			// Check whether this exact flag value was generated for someone ELSE
			var ownerUserID, ownerInstanceID, sharedFlagID, sharedFlagName string
			var sharedPoints int
			shareErr := h.db.Pool.QueryRow(c.Request.Context(),
				`SELECT inf.user_id, inf.instance_id, f.id, f.name, f.points
				   FROM instance_flags inf
				   JOIN flags f ON f.id = inf.flag_id
				  WHERE inf.flag_value   = $1
				    AND inf.challenge_id = $2
				    AND inf.user_id     != $3
				  LIMIT 1`,
				submittedFlag, challengeID, uid,
			).Scan(&ownerUserID, &ownerInstanceID, &sharedFlagID, &sharedFlagName, &sharedPoints)
			if shareErr == nil {
				// Flag share detected — accept transparently, log silently
				matchedFlag.ID = sharedFlagID
				matchedFlag.Name = sharedFlagName
				matchedFlag.Points = sharedPoints
				found = true

				_, logErr := h.db.Pool.Exec(c.Request.Context(),
					`INSERT INTO flag_share_events
						(id, challenge_id, flag_id, owner_user_id, owner_instance_id,
						 submitter_user_id, flag_value, submitter_ip, created_at)
					VALUES
						(uuid_generate_v4(), $1, $2, $3, $4, $5, $6, $7, NOW())`,
					challengeID, sharedFlagID, ownerUserID, ownerInstanceID,
					uid, submittedFlag, c.ClientIP())
				if logErr != nil {
					h.logger.Warn("failed to log flag share event", zap.Error(logErr))
				} else {
					h.logger.Warn("FLAG SHARE DETECTED",
						zap.String("submitter", uid.String()),
						zap.String("owner", ownerUserID),
						zap.String("challenge_id", challengeID),
						zap.String("flag_value", submittedFlag),
					)
				}
			}
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
		// ── Update brute-force lockout tracking ───────────────────────────────
		newCount := wrongAttempts + 1
		var newLockedUntil interface{}
		if newCount >= flagLockoutThreshold {
			t := time.Now().Add(flagLockoutDuration)
			newLockedUntil = t
		}
		if _, upsertErr := h.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO flag_attempt_lockouts (user_id, challenge_id, wrong_attempts, first_attempt_at, locked_until, updated_at)
			 VALUES ($1, $2, $3, NOW(), $4, NOW())
			 ON CONFLICT (user_id, challenge_id) DO UPDATE
			 SET wrong_attempts = EXCLUDED.wrong_attempts,
			     locked_until   = COALESCE($4, flag_attempt_lockouts.locked_until),
			     updated_at     = NOW()`,
			uid, challengeID, newCount, newLockedUntil,
		); upsertErr != nil {
			h.logger.Warn("failed to update lockout tracking", zap.Error(upsertErr))
		}

		c.JSON(http.StatusOK, gin.H{
			"correct": false,
			"message": "Incorrect flag. Try again!",
		})
		return
	}

	// Correct submission: clear any lockout entry
	if _, delErr := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM flag_attempt_lockouts WHERE user_id = $1 AND challenge_id = $2`,
		uid, challengeID,
	); delErr != nil {
		h.logger.Warn("failed to clear lockout on correct submission", zap.Error(delErr))
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
		`INSERT INTO solves (id, user_id, challenge_id, flag_id, points_awarded, solved_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (user_id, flag_id) DO NOTHING`,
		solveID, uid, challengeID, matchedFlag.ID, matchedFlag.Points)

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
		if uid, ok := id.(uuid.UUID); ok {
			userID = &uid
		} else if uidStr, ok := id.(string); ok {
			if uid, err := uuid.Parse(uidStr); err == nil {
				userID = &uid
			}
		}
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
