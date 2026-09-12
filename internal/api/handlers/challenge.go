package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/jackc/pgx/v5"
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
	if uid, ok := contextUserID(c); ok {
		userID = &uid
	}

	// Query published challenges
	query := `
		SELECT 
			c.id, c.name, c.slug, c.description, c.difficulty,
			c.base_points, c.total_solves, c.total_flags, c.author_name,
			c.resource_type, cat.id as category_id, cat.name as category_name,
			COALESCE((
				SELECT COUNT(*) FROM solves s
				JOIN flags f ON s.flag_id = f.id
				WHERE s.user_id = $1 AND f.challenge_id = c.id
			), 0) AS user_solves
		FROM challenges c
		LEFT JOIN categories cat ON c.category_id = cat.id
		WHERE c.status = 'published'
		  AND (c.release_date IS NULL OR c.release_date <= NOW())
		ORDER BY c.created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query, userID)
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
			&ch.ResourceType, &categoryID, &categoryName, &ch.UserSolves,
		); err != nil {
			h.logger.Error("failed to scan challenge", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenges"})
			return
		}

		ch.CategoryID = categoryID
		ch.Category = categoryName

		ch.IsSolved = ch.UserSolves >= ch.TotalFlags && ch.TotalFlags > 0

		challenges = append(challenges, ch)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while reading challenges", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenges"})
		return
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
	if uid, ok := contextUserID(c); ok {
		userID = &uid
	}
	if role, exists := c.Get("role"); exists {
		if typedRole, ok := role.(string); ok {
			userRole = typedRole
		}
	}

	// Query challenge - allow admins to see all challenges, others only published
	var statusCondition string
	if userID != nil && userRole == "admin" {
		statusCondition = "(c.status = 'published' OR c.status = 'draft')"
	} else {
		statusCondition = "c.status = 'published' AND (c.release_date IS NULL OR c.release_date <= NOW())"
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

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to get challenge", zap.Error(err), zap.String("slug", slug))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		return
	}

	ch.CategoryID = categoryID
	ch.Category = categoryName

	ch.ExposedPorts = []models.ExposedPort{}
	if len(exposedPortsJSON) > 0 {
		if err := json.Unmarshal(exposedPortsJSON, &ch.ExposedPorts); err != nil {
			h.logger.Error("failed to decode challenge exposed ports", zap.String("challenge_id", ch.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
			return
		}
		if ch.ExposedPorts == nil {
			ch.ExposedPorts = []models.ExposedPort{}
		}
	}

	// Get flags
	flagsQuery := `
		SELECT f.id, f.name, f.points, f.sort_order,
			(SELECT COUNT(*) FROM solves WHERE flag_id = f.id) AS total_solves,
			s.solved_at
		FROM flags f
		LEFT JOIN solves s ON s.flag_id = f.id AND s.user_id = $2
		WHERE f.challenge_id = $1
		ORDER BY f.sort_order
	`
	flagRows, err := h.db.Pool.Query(c.Request.Context(), flagsQuery, ch.ID, userID)
	if err != nil {
		h.logger.Error("failed to query challenge flags", zap.String("challenge_id", ch.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		return
	}
	defer flagRows.Close()
	for flagRows.Next() {
		var f FlagResponse
		var solvedAt *time.Time
		if err := flagRows.Scan(&f.ID, &f.Name, &f.Points, &f.Order, &f.TotalSolves, &solvedAt); err != nil {
			h.logger.Error("failed to scan challenge flag", zap.String("challenge_id", ch.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
			return
		}

		if solvedAt != nil {
			f.IsSolved = true
			ts := solvedAt.Unix()
			f.SolvedAt = &ts
			ch.UserSolves++
		}
		ch.Flags = append(ch.Flags, f)
	}
	if err := flagRows.Err(); err != nil {
		h.logger.Error("failed while reading challenge flags", zap.String("challenge_id", ch.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		return
	}

	// Get hints
	hintsQuery := `
		SELECT h.id, h.content, h.cost, h.sort_order, hu.id IS NOT NULL
		FROM hints h
		LEFT JOIN hint_unlocks hu ON hu.hint_id = h.id AND hu.user_id = $2
		WHERE h.challenge_id = $1
		ORDER BY h.sort_order
	`
	hintRows, err := h.db.Pool.Query(c.Request.Context(), hintsQuery, ch.ID, userID)
	if err != nil {
		h.logger.Error("failed to query challenge hints", zap.String("challenge_id", ch.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		return
	}
	defer hintRows.Close()
	for hintRows.Next() {
		var hint HintResponse
		var content string
		if err := hintRows.Scan(&hint.ID, &content, &hint.Cost, &hint.Order, &hint.IsUnlocked); err != nil {
			h.logger.Error("failed to scan challenge hint", zap.String("challenge_id", ch.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
			return
		}

		if hint.IsUnlocked {
			hint.Content = &content
		}
		ch.Hints = append(ch.Hints, hint)
	}
	if err := hintRows.Err(); err != nil {
		h.logger.Error("failed while reading challenge hints", zap.String("challenge_id", ch.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		return
	}

	ch.IsSolved = ch.UserSolves >= ch.TotalFlags && ch.TotalFlags > 0

	// Attach file attachments
	if h.attachmentHdlr != nil {
		attachments, err := h.attachmentHdlr.ListPublic(c, ch.ID)
		if err != nil {
			h.logger.Error("failed to query challenge attachments", zap.String("challenge_id", ch.ID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
			return
		}
		ch.Attachments = attachments
	}
	if ch.Flags == nil {
		ch.Flags = []FlagResponse{}
	}
	if ch.Hints == nil {
		ch.Hints = []HintResponse{}
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
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Get challenge ID
	var challengeID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges
		 WHERE slug = $1 AND status = 'published'
		   AND (release_date IS NULL OR release_date <= NOW())`, slug).Scan(&challengeID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to query challenge flags", zap.String("slug", slug), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
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
			h.logger.Error("failed to scan challenge flag", zap.String("challenge_id", challengeID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
			return
		}
		if solvedAt != nil {
			f.IsSolved = true
			ts := solvedAt.Unix()
			f.SolvedAt = &ts
		}
		flags = append(flags, f)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while reading challenge flags", zap.String("challenge_id", challengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
		return
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

const maxSubmittedFlagLength = 4096

// SubmitFlag handles flag submission
func (h *ChallengeHandler) SubmitFlag(c *gin.Context) {
	slug := c.Param("slug")

	// Get user ID
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req SubmitFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required"})
		return
	}
	submittedFlag := strings.TrimSpace(req.Flag)
	if submittedFlag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required"})
		return
	}
	if len(submittedFlag) > maxSubmittedFlagLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is too long"})
		return
	}

	// Get challenge
	var challengeID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges
		 WHERE slug = $1 AND status = 'published'
		   AND (release_date IS NULL OR release_date <= NOW())`, slug).Scan(&challengeID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to query challenge for flag submission", zap.String("slug", slug), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
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
	lockRow := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT locked_until
		 FROM flag_attempt_lockouts
		 WHERE user_id = $1 AND challenge_id = $2`,
		uid, challengeID,
	)
	lockErr := lockRow.Scan(&lockedUntil)
	if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
		h.logger.Error("failed to check flag submission lockout", zap.String("challenge_id", challengeID), zap.Error(lockErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}
	if lockErr == nil && lockedUntil != nil && time.Now().Before(*lockedUntil) {
		retryAfter := int(time.Until(*lockedUntil).Seconds()) + 1
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":        "Too many incorrect attempts. Please wait before trying again.",
			"locked_until": lockedUntil.Unix(),
			"retry_after":  retryAfter,
		})
		return
	}
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
			rows.Close()
			h.logger.Error("failed to scan static flag", zap.String("challenge_id", challengeID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
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
	if err := rows.Err(); err != nil {
		rows.Close()
		h.logger.Error("failed while reading static flags", zap.String("challenge_id", challengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}
	rows.Close()

	// ── Regex flag check ─────────────────────────────────────────────────────
	// Container generates its own dynamic flag; admin defines a regex pattern.
	// Any submission matching the regex is correct. Duplicate values across
	// users trigger a silent flag-share event.
	if !found {
		regexRows, regexErr := h.db.Pool.Query(c.Request.Context(),
			`SELECT f.id, f.flag_hash, f.name, f.points, f.case_sensitive
			   FROM flags f
			  WHERE f.challenge_id = $1 AND f.flag_type = 'regex'`,
			challengeID)
		if regexErr != nil {
			h.logger.Error("failed to query regex flags", zap.String("challenge_id", challengeID), zap.Error(regexErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
		}
		for regexRows.Next() {
			var fID, pattern, fName string
			var fPoints int
			var caseSensitive bool
			if err := regexRows.Scan(&fID, &pattern, &fName, &fPoints, &caseSensitive); err != nil {
				regexRows.Close()
				h.logger.Error("failed to scan regex flag", zap.String("challenge_id", challengeID), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
				return
			}
			if !caseSensitive {
				pattern = "(?i:" + pattern + ")"
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				regexRows.Close()
				h.logger.Error("invalid regex flag pattern", zap.String("flag_id", fID), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
				return
			}
			if re.MatchString(submittedFlag) {
				matchedFlag.ID = fID
				matchedFlag.Name = fName
				matchedFlag.Points = fPoints
				found = true
				break
			}
		}
		if err := regexRows.Err(); err != nil {
			regexRows.Close()
			h.logger.Error("failed while reading regex flags", zap.String("challenge_id", challengeID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
		}
		regexRows.Close()

		if found {
			// Flag share detection: same exact value previously submitted by another user.
			var priorUserID string
			shareErr := h.db.Pool.QueryRow(c.Request.Context(),
				`SELECT user_id FROM flag_attempts
				 WHERE submitted_flag = $1 AND challenge_id = $2 AND flag_id = $4
				   AND is_correct = true AND user_id != $3
				 LIMIT 1`,
				submittedFlag, challengeID, uid, matchedFlag.ID,
			).Scan(&priorUserID)
			switch {
			case shareErr == nil:
				if _, err := h.db.Pool.Exec(c.Request.Context(),
					`INSERT INTO flag_share_events
						(id, challenge_id, flag_id, owner_user_id, owner_instance_id,
						 submitter_user_id, flag_value, submitter_ip, created_at)
					 VALUES
						(uuid_generate_v4(), $1, $2, $3, NULL, $4, $5, $6, NOW())`,
					challengeID, matchedFlag.ID, priorUserID, uid, submittedFlag, c.ClientIP(),
				); err != nil {
					h.logger.Error("failed to log regex flag share event", zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
					return
				}
				h.logger.Warn("FLAG SHARE DETECTED (regex)",
					zap.String("submitter", uid.String()),
					zap.String("prior_user", priorUserID),
					zap.String("challenge_id", challengeID),
				)
			case errors.Is(shareErr, pgx.ErrNoRows):
			default:
				h.logger.Error("failed to check regex flag sharing", zap.String("challenge_id", challengeID), zap.Error(shareErr))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
				return
			}
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
		} else if errors.Is(err, pgx.ErrNoRows) {
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
					h.logger.Error("failed to log flag share event", zap.Error(logErr))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
					return
				} else {
					h.logger.Warn("FLAG SHARE DETECTED",
						zap.String("submitter", uid.String()),
						zap.String("owner", ownerUserID),
						zap.String("challenge_id", challengeID),
					)
				}
			} else if !errors.Is(shareErr, pgx.ErrNoRows) {
				h.logger.Error("failed to check dynamic flag sharing", zap.String("challenge_id", challengeID), zap.Error(shareErr))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
				return
			}
		} else {
			h.logger.Error("failed to query dynamic flag", zap.String("challenge_id", challengeID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
		}
	}
	if found && matchedFlag.Points < 0 {
		h.logger.Error("matched flag has negative points", zap.String("flag_id", matchedFlag.ID), zap.Int("points", matchedFlag.Points))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}

	// Record the attempt and increment the challenge counter in one statement so
	// neither half can be persisted without the other.
	attemptID := uuid.New()
	var matchedFlagID any
	if found {
		matchedFlagID = matchedFlag.ID
	}
	attemptResult, err := h.db.Pool.Exec(c.Request.Context(),
		`WITH recorded_attempt AS (
			INSERT INTO flag_attempts
				(id, user_id, challenge_id, flag_id, submitted_flag, is_correct, ip_address, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			RETURNING 1
		)
		UPDATE challenges
		SET total_attempts = total_attempts + 1
		WHERE id = $3 AND EXISTS (SELECT 1 FROM recorded_attempt)`,
		attemptID, uid, challengeID, matchedFlagID, submittedFlag, found, c.ClientIP())
	if err != nil {
		h.logger.Error("failed to record flag attempt", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}
	if attemptResult.RowsAffected() != 1 {
		h.logger.Error("flag attempt recording affected an unexpected number of challenges", zap.Int64("rows_affected", attemptResult.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}

	if !found {
		// ── Update brute-force lockout tracking ───────────────────────────────
		var newCount int
		if upsertErr := h.db.Pool.QueryRow(c.Request.Context(),
			`INSERT INTO flag_attempt_lockouts (user_id, challenge_id, wrong_attempts, first_attempt_at, locked_until, updated_at)
			 VALUES ($1, $2, 1, NOW(), NULL, NOW())
			 ON CONFLICT (user_id, challenge_id) DO UPDATE
			 SET wrong_attempts = CASE
			       WHEN flag_attempt_lockouts.first_attempt_at < NOW() - ($3 * INTERVAL '1 second')
			         OR flag_attempt_lockouts.locked_until <= NOW() THEN 1
			       ELSE flag_attempt_lockouts.wrong_attempts + 1
			     END,
			     first_attempt_at = CASE
			       WHEN flag_attempt_lockouts.first_attempt_at < NOW() - ($3 * INTERVAL '1 second')
			         OR flag_attempt_lockouts.locked_until <= NOW() THEN NOW()
			       ELSE flag_attempt_lockouts.first_attempt_at
			     END,
			     locked_until = CASE
			       WHEN flag_attempt_lockouts.first_attempt_at < NOW() - ($3 * INTERVAL '1 second')
			         OR flag_attempt_lockouts.locked_until <= NOW() THEN NULL
			       WHEN flag_attempt_lockouts.wrong_attempts + 1 >= $4
			         THEN NOW() + ($5 * INTERVAL '1 second')
			       ELSE NULL
			     END,
			     updated_at = NOW()
			 RETURNING wrong_attempts`,
			uid, challengeID, int(flagLockoutWindow.Seconds()), flagLockoutThreshold, int(flagLockoutDuration.Seconds()),
		).Scan(&newCount); upsertErr != nil {
			h.logger.Error("failed to update lockout tracking", zap.Error(upsertErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
		}

		attemptsRemaining := flagLockoutThreshold - newCount
		if attemptsRemaining < 0 {
			attemptsRemaining = 0
		}
		c.JSON(http.StatusOK, gin.H{
			"correct":            false,
			"message":            "Incorrect flag. Try again!",
			"attempts_remaining": attemptsRemaining,
		})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin solve transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}
	defer tx.Rollback(ctx)

	// Serialize solves for this challenge so its denormalized counts cannot lose
	// concurrent updates.
	var challengeLock int
	if err := tx.QueryRow(ctx,
		`SELECT 1 FROM challenges WHERE id = $1 FOR UPDATE`, challengeID,
	).Scan(&challengeLock); err != nil {
		h.logger.Error("failed to lock challenge for solve", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM flag_attempt_lockouts WHERE user_id = $1 AND challenge_id = $2`, uid, challengeID,
	); err != nil {
		h.logger.Error("failed to clear flag lockout", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	// Count the source rows rather than trusting total_flags, which admin flag
	// edits update separately and may briefly leave stale.
	var totalFlags int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM flags WHERE challenge_id = $1`, challengeID,
	).Scan(&totalFlags); err != nil {
		h.logger.Error("failed to count challenge flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	// Record solve (with ON CONFLICT to handle duplicate submissions).
	solveID := uuid.New()
	result, err := tx.Exec(ctx,
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
		var solvedFlags int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM solves s
			 JOIN flags f ON s.flag_id = f.id
			 WHERE s.user_id = $1 AND f.challenge_id = $2`, uid, challengeID,
		).Scan(&solvedFlags); err != nil {
			h.logger.Error("failed to count flags for duplicate solve", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
			return
		}
		if err := tx.Commit(ctx); err != nil {
			h.logger.Error("failed to commit duplicate solve transaction", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
			return
		}
		fullySolved := totalFlags > 0 && solvedFlags >= totalFlags
		if fullySolved {
			go h.cleanupSolvedInstance(challengeID, uid)
		}
		c.JSON(http.StatusOK, gin.H{
			"correct":        true,
			"already_solved": true,
			"message":        "Correct! But you've already solved this flag.",
			"flag_name":      matchedFlag.Name,
			"points":         0,
			"fully_solved":   fullySolved,
			"solved_flags":   solvedFlags,
			"total_flags":    totalFlags,
		})
		return
	}

	// Update user and denormalized solve counts only for a newly inserted solve.
	userResult, err := tx.Exec(ctx,
		`UPDATE users SET total_score = total_score + $1, updated_at = NOW() WHERE id = $2`,
		matchedFlag.Points, uid)
	if err != nil {
		h.logger.Error("failed to update user score", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}
	if userResult.RowsAffected() != 1 {
		h.logger.Error("failed to update user score", zap.Int64("rows_affected", userResult.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	flagResult, err := tx.Exec(ctx,
		`UPDATE flags SET total_solves = (
			SELECT COUNT(*) FROM solves WHERE flag_id = $1
		), first_blood_user_id = COALESCE(first_blood_user_id, $2),
		   first_blood_at = COALESCE(first_blood_at, NOW()),
		   updated_at = NOW()
		 WHERE id = $1`, matchedFlag.ID, uid)
	if err != nil {
		h.logger.Error("failed to update flag solve count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}
	if flagResult.RowsAffected() != 1 {
		h.logger.Error("failed to update flag solve count", zap.Int64("rows_affected", flagResult.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	challengeResult, err := tx.Exec(ctx,
		`UPDATE challenges SET total_solves = (
			SELECT COUNT(*) FROM (
				SELECT s.user_id
				FROM solves s
				JOIN flags f ON s.flag_id = f.id
				WHERE f.challenge_id = $1
				GROUP BY s.user_id
				HAVING COUNT(DISTINCT s.flag_id) = $2
			) fully_solved_users
		) WHERE id = $1`, challengeID, totalFlags)
	if err != nil {
		h.logger.Error("failed to update challenge solve count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}
	if challengeResult.RowsAffected() != 1 {
		h.logger.Error("failed to update challenge solve count", zap.Int64("rows_affected", challengeResult.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	// Check if all flags solved (first blood check)
	var solvedFlags int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM solves s JOIN flags f ON s.flag_id = f.id
		 WHERE s.user_id = $1 AND f.challenge_id = $2`, uid, challengeID,
	).Scan(&solvedFlags); err != nil {
		h.logger.Error("failed to count solved flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit solve transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

	response := gin.H{
		"correct":      true,
		"message":      "Correct! Flag captured!",
		"flag_name":    matchedFlag.Name,
		"points":       matchedFlag.Points,
		"fully_solved": solvedFlags >= totalFlags,
		"solved_flags": solvedFlags,
		"total_flags":  totalFlags,
	}

	// Auto-stop and remove instance when challenge is fully solved
	if solvedFlags >= totalFlags && totalFlags > 0 {
		go h.cleanupSolvedInstance(challengeID, uid)
	}

	c.JSON(http.StatusOK, response)
}

// cleanupSolvedInstance stops and removes the user's active instance for
// a fully-solved challenge. Runs in a background goroutine so the flag
// submission response is not delayed.
func (h *ChallengeHandler) cleanupSolvedInstance(challengeID string, userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find the user's active instance for this challenge
	var instanceID, containerID, resourceType string
	var vmNodeID *uuid.UUID
	var reservedVCPU, reservedMemoryMB int
	err := h.db.Pool.QueryRow(ctx,
		`SELECT i.id, COALESCE(i.container_id, ''), i.resource_type,
		        i.vm_node_id, COALESCE(i.reserved_vcpu, 0), COALESCE(i.reserved_memory_mb, 0)
		 FROM instances i
		 WHERE i.user_id = $1 AND i.challenge_id = $2
		   AND i.status NOT IN ('stopped', 'failed', 'expired')
		   AND i.expires_at > NOW()
		 ORDER BY i.created_at DESC LIMIT 1`,
		userID, challengeID).Scan(
		&instanceID, &containerID, &resourceType,
		&vmNodeID, &reservedVCPU, &reservedMemoryMB,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		h.logger.Error("auto-stop: failed to query active instance", zap.Error(err), zap.String("challenge_id", challengeID), zap.String("user_id", userID.String()))
		return
	}

	h.logger.Info("auto-stopping instance for fully solved challenge",
		zap.String("instance_id", instanceID),
		zap.String("user_id", userID.String()),
		zap.String("challenge_id", challengeID),
		zap.String("resource_type", resourceType))

	if containerID == "" {
		h.logger.Warn("auto-stop: active instance has no runtime identifier", zap.String("instance_id", instanceID))
		return
	}
	switch resourceType {
	case "vm":
		if h.vmSvc == nil {
			h.logger.Error("auto-stop: VM service unavailable", zap.String("instance_id", instanceID))
			return
		}
		var destroyErr error
		if vmNodeID != nil {
			node, nodeErr := loadAssignedVMNode(ctx, h.db.Pool, *vmNodeID)
			if nodeErr != nil {
				h.logger.Error("auto-stop: failed to load assigned VM node", zap.Error(nodeErr), zap.String("instance_id", instanceID))
				return
			}
			destroyErr = h.vmSvc.DestroyInstanceByNameOnNode(ctx, containerID, node)
		} else {
			destroyErr = h.vmSvc.DestroyInstanceByName(ctx, containerID)
		}
		if destroyErr != nil {
			h.logger.Error("auto-stop: failed to destroy VM", zap.Error(destroyErr), zap.String("vm_name", containerID))
			return
		}
	case "docker":
		if h.containerSvc == nil {
			h.logger.Error("auto-stop: container service unavailable", zap.String("instance_id", instanceID))
			return
		}
		if err := h.containerSvc.StopInstance(ctx, containerID); err != nil {
			h.logger.Error("auto-stop: failed to stop container", zap.Error(err), zap.String("container_id", containerID))
			return
		}
	default:
		h.logger.Error("auto-stop: unsupported resource type", zap.String("resource_type", resourceType), zap.String("instance_id", instanceID))
		return
	}

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("auto-stop: failed to begin cleanup transaction", zap.Error(err), zap.String("instance_id", instanceID))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx,
		`DELETE FROM instances WHERE id = $1 AND user_id = $2 AND challenge_id = $3`,
		instanceID, userID, challengeID)
	if err != nil {
		h.logger.Error("auto-stop: failed to delete instance", zap.Error(err), zap.String("instance_id", instanceID))
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("auto-stop: instance deletion affected an unexpected number of rows", zap.Int64("rows_affected", result.RowsAffected()), zap.String("instance_id", instanceID))
		return
	}

	if resourceType == "vm" {
		if err := releaseVMNodeCapacity(ctx, tx, vmNodeID, reservedVCPU, reservedMemoryMB); err != nil {
			h.logger.Error("auto-stop: failed to release VM node capacity", zap.Error(err), zap.String("instance_id", instanceID))
			return
		}
	}

	// Clear cooldown so the user doesn't get penalized for an auto-stop
	if _, err := tx.Exec(ctx,
		`DELETE FROM user_cooldowns WHERE user_id = $1 AND challenge_id = $2`,
		userID, challengeID); err != nil {
		h.logger.Error("auto-stop: failed to clear challenge cooldown", zap.Error(err), zap.String("instance_id", instanceID))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("auto-stop: failed to commit cleanup", zap.Error(err), zap.String("instance_id", instanceID))
		return
	}

	h.logger.Info("auto-stop: instance cleaned up successfully",
		zap.String("instance_id", instanceID),
		zap.String("user_id", userID.String()))
}

// GetHints returns hints for a challenge
func (h *ChallengeHandler) GetHints(c *gin.Context) {
	slug := c.Param("slug")

	// Get user ID if authenticated
	var userID *uuid.UUID
	if uid, ok := contextUserID(c); ok {
		userID = &uid
	}

	// Get challenge ID
	var challengeID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges
		 WHERE slug = $1 AND status = 'published'
		   AND (release_date IS NULL OR release_date <= NOW())`, slug).Scan(&challengeID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to query challenge for hints", zap.String("slug", slug), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
		return
	}

	// Get hints
	query := `
		SELECT h.id, h.content, h.cost, h.sort_order, hu.id IS NOT NULL
		FROM hints h
		LEFT JOIN hint_unlocks hu ON hu.hint_id = h.id AND hu.user_id = $2
		WHERE h.challenge_id = $1
		ORDER BY h.sort_order
	`
	rows, err := h.db.Pool.Query(c.Request.Context(), query, challengeID, userID)
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
		if err := rows.Scan(&hint.ID, &content, &hint.Cost, &hint.Order, &hint.IsUnlocked); err != nil {
			h.logger.Error("failed to scan challenge hint", zap.String("challenge_id", challengeID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
			return
		}

		if hint.IsUnlocked {
			hint.Content = &content
		}
		hints = append(hints, hint)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while reading challenge hints", zap.String("challenge_id", challengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
		return
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
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	hid, err := uuid.Parse(hintID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hint ID"})
		return
	}

	// Verify the published challenge exists before opening the unlock transaction.
	var challengeID string
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM challenges
		 WHERE slug = $1 AND status = 'published'
		   AND (release_date IS NULL OR release_date <= NOW())`, slug).Scan(&challengeID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to query challenge for hint unlock", zap.String("slug", slug), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin hint unlock transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}
	defer tx.Rollback(ctx)

	var hintCost int
	var hintContent string
	err = tx.QueryRow(ctx,
		`SELECT h.cost, h.content
		 FROM hints h
		 JOIN challenges c ON c.id = h.challenge_id
		 WHERE h.id = $1 AND h.challenge_id = $2
		   AND c.status = 'published'
		   AND (c.release_date IS NULL OR c.release_date <= NOW())
		 FOR SHARE OF h, c`,
		hid, challengeID).Scan(&hintCost, &hintContent)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "hint not found"})
		return
	} else if err != nil {
		h.logger.Error("failed to query hint for unlock", zap.String("hint_id", hid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}
	if hintCost < 0 {
		h.logger.Error("hint has an invalid negative cost", zap.String("hint_id", hid.String()), zap.Int("cost", hintCost))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	// Serialize all hint purchases for this user so concurrent requests cannot
	// spend the same score or charge twice for one hint.
	var userScore int
	err = tx.QueryRow(ctx, `SELECT total_score FROM users WHERE id = $1 FOR UPDATE`, uid).Scan(&userScore)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	} else if err != nil {
		h.logger.Error("failed to lock user for hint unlock", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	var alreadyUnlocked bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM hint_unlocks WHERE user_id = $1 AND hint_id = $2)`,
		uid, hid,
	).Scan(&alreadyUnlocked); err != nil {
		h.logger.Error("failed to check hint unlock", zap.String("hint_id", hid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}
	if alreadyUnlocked {
		if err := tx.Commit(ctx); err != nil {
			h.logger.Error("failed to finish existing hint unlock", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"content":          hintContent,
			"already_unlocked": true,
		})
		return
	}

	if userScore < hintCost {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":          "insufficient points",
			"required":       hintCost,
			"current_points": userScore,
		})
		return
	}

	result, err := tx.Exec(ctx,
		`UPDATE users SET total_score = total_score - $1, updated_at = NOW() WHERE id = $2`,
		hintCost, uid)
	if err != nil {
		h.logger.Error("failed to deduct hint points", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("hint point deduction affected an unexpected number of users", zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	unlockID := uuid.New()
	result, err = tx.Exec(ctx,
		`INSERT INTO hint_unlocks (id, user_id, hint_id, points_deducted, unlocked_at)
		 VALUES ($1, $2, $3, $4, NOW())`,
		unlockID, uid, hid, hintCost)
	if err != nil {
		h.logger.Error("failed to record hint unlock", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("hint unlock affected an unexpected number of rows", zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit hint unlock", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock hint"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"content":          hintContent,
		"points_spent":     hintCost,
		"remaining_points": userScore - hintCost,
	})
}
