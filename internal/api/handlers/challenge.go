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

func hashFlagForComparison(flag string) string {
	hash := sha256.Sum256([]byte(flag))
	return hex.EncodeToString(hash[:])
}

type ChallengeService struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewChallengeService(cfg *config.Config, db *database.DB, logger *zap.Logger) *ChallengeService {
	return &ChallengeService{config: cfg, db: db, logger: logger}
}

type ChallengeListResponse struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Slug           string  `json:"slug"`
	Description    *string `json:"description,omitempty"`
	Difficulty     string  `json:"difficulty"`
	Category       *string `json:"category,omitempty"`
	CategoryID     *string `json:"category_id,omitempty"`
	BasePoints     int     `json:"base_points"`
	TotalSolves    int     `json:"total_solves"`
	TotalFlags     int     `json:"total_flags"`
	AuthorName     *string `json:"author_name,omitempty"`
	IsSolved       bool    `json:"is_solved"`
	UserSolves     int     `json:"user_solves"`
	ResourceType   string  `json:"resource_type"`             // docker or vm
	SubDescription *string `json:"sub_description,omitempty"` // short one-liner shown on the tile
}

type ChallengeDetailResponse struct {
	ChallengeListResponse
	ExposedPorts    []models.ExposedPort  `json:"exposed_ports"`
	Flags           []FlagResponse        `json:"flags"`
	Hints           []HintResponse        `json:"hints"`
	Attachments     []AttachmentResponse  `json:"attachments"`
	ReleaseDate     *time.Time            `json:"release_date,omitempty"`
	InstanceTimeout *int                  `json:"instance_timeout,omitempty"`
	MaxExtensions   *int                  `json:"max_extensions,omitempty"`
	Status          string                `json:"status"`       // draft, published, archived
	HasInstance     bool                  `json:"has_instance"` // docker w/ image, or active vm template
	Economy         *ChallengeEconomyInfo `json:"economy,omitempty"`
}

// economy state for the caller's team; present only when economy_mode is on.
type ChallengeEconomyInfo struct {
	Enabled    bool    `json:"enabled"`
	Launched   bool    `json:"launched"`
	Solved     bool    `json:"solved"`
	LaunchCost float64 `json:"launch_cost"`
	Credits    float64 `json:"credits"`
}

type FlagResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Points      int    `json:"points"`
	Order       int    `json:"order"`
	IsSolved    bool   `json:"is_solved"`
	SolvedAt    *int64 `json:"solved_at,omitempty"` // unix timestamp
	TotalSolves int    `json:"total_solves"`
}

type HintResponse struct {
	ID         string  `json:"id"`
	Content    *string `json:"content,omitempty"` // only shown if unlocked
	Cost       int     `json:"cost"`
	Order      int     `json:"order"`
	IsUnlocked bool    `json:"is_unlocked"`
}

func (h *ChallengeHandler) List(c *gin.Context) {
	var userID *uuid.UUID
	if uid, ok := contextUserID(c); ok {
		userID = &uid
	}

	query := `
		SELECT 
			c.id, c.name, c.slug, c.description, c.difficulty,
			c.base_points, c.total_solves, c.total_flags, c.author_name,
			c.resource_type, c.sub_description, cat.id as category_id, cat.name as category_name,
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
			&ch.ResourceType, &ch.SubDescription, &categoryID, &categoryName, &ch.UserSolves,
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

func (h *ChallengeHandler) Get(c *gin.Context) {
	slug := c.Param("slug")

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
			c.resource_type, c.status, c.sub_description,
			(
				(c.resource_type = 'docker' AND COALESCE(c.container_image, '') <> '')
				OR (c.resource_type = 'vm' AND EXISTS (
					SELECT 1 FROM challenge_resources cr
					WHERE cr.challenge_id = c.id AND cr.resource_type = 'vm' AND cr.is_active = TRUE))
			) AS has_instance,
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
		&ch.ResourceType, &ch.Status, &ch.SubDescription,
		&ch.HasInstance,
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

	// economy enrichment: when the economy is live, tell the client whether the
	// caller's team has launched this challenge (which gates the full description,
	// files, submission, and instance), the launch cost, and the team's credits.
	if on, _ := isEconomyMode(c.Request.Context(), h.db); on {
		info := &ChallengeEconomyInfo{Enabled: true, LaunchCost: launchCost(h.config.Economy, ch.Difficulty)}
		if uid, ok := contextUserID(c); ok {
			if teamID, tErr := resolveTeamID(c.Request.Context(), h.db, uid); tErr == nil && teamID != nil {
				var status string
				_ = h.db.Pool.QueryRow(c.Request.Context(),
					`SELECT status FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
					*teamID, ch.ID).Scan(&status)
				info.Launched = status == "open" || status == "solved"
				info.Solved = status == "solved"
				_ = h.db.Pool.QueryRow(c.Request.Context(),
					`SELECT COALESCE(credits, 0) FROM economy_team_score WHERE team_id = $1`, *teamID).Scan(&info.Credits)
			}
		}
		ch.Economy = info
	}

	c.JSON(http.StatusOK, ch)
}

func (h *ChallengeHandler) GetFlags(c *gin.Context) {
	slug := c.Param("slug")

	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

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

type SubmitFlagRequest struct {
	Flag string `json:"flag" binding:"required"`
}

const maxSubmittedFlagLength = 4096

// economy launch/open gate: charge launch cost, take a concurrency slot, start the band timer, and mark the challenge open for the team (enables submission, reveals the full challenge). container challenges also open via start-instance; this covers opening in general, incl. static-download challenges with no instance.
func (h *ChallengeHandler) OpenChallenge(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	slug := c.Param("slug")
	ctx := c.Request.Context()

	on, err := isEconomyMode(ctx, h.db)
	if err != nil {
		h.logger.Error("failed to read economy_mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch challenge"})
		return
	}
	if !on {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the economy is not enabled"})
		return
	}

	teamID, err := resolveTeamID(ctx, h.db, uid)
	if err != nil {
		h.logger.Error("failed to resolve team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch challenge"})
		return
	}
	if teamID == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "join a team before launching a challenge"})
		return
	}

	var chalID uuid.UUID
	var difficulty string
	err = h.db.Pool.QueryRow(ctx,
		`SELECT id, difficulty FROM challenges
		 WHERE slug = $1 AND status = 'published' AND (release_date IS NULL OR release_date <= NOW())`,
		slug).Scan(&chalID, &difficulty)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load challenge for open", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch challenge"})
		return
	}

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch challenge"})
		return
	}
	defer tx.Rollback(ctx)

	if opErr := openChallengeEconomy(ctx, tx, *teamID, chalID, difficulty, h.config.Economy); opErr != nil {
		c.JSON(opErr.Status, gin.H{"error": opErr.Message})
		return
	}
	var credits float64
	_ = tx.QueryRow(ctx, `SELECT credits FROM economy_team_score WHERE team_id = $1`, *teamID).Scan(&credits)
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit challenge open", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to launch challenge"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "open", "credits": credits, "message": "challenge launched"})
}

// resolves the economy context for a challenge action (economy on, caller's team, challenge id + difficulty). writes the error response and returns ok=false on any failure.
func (h *ChallengeHandler) economyChallengeCtx(c *gin.Context) (teamID, chalID uuid.UUID, difficulty string, ok bool) {
	uid, uok := contextUserID(c)
	if !uok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ctx := c.Request.Context()
	on, err := isEconomyMode(ctx, h.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "economy unavailable"})
		return
	}
	if !on {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the economy is not enabled"})
		return
	}
	tid, err := resolveTeamID(ctx, h.db, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "economy unavailable"})
		return
	}
	if tid == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "join a team to use the economy"})
		return
	}
	err = h.db.Pool.QueryRow(ctx,
		`SELECT id, difficulty FROM challenges
		 WHERE slug = $1 AND status = 'published' AND (release_date IS NULL OR release_date <= NOW())`,
		c.Param("slug")).Scan(&chalID, &difficulty)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load challenge"})
		return
	}
	return *tid, chalID, difficulty, true
}

// releases an open challenge early for a partial refund.
func (h *ChallengeHandler) AbandonChallenge(c *gin.Context) {
	teamID, chalID, difficulty, ok := h.economyChallengeCtx(c)
	if !ok {
		return
	}
	h.economyTx(c, func(tx pgx.Tx) *EconomyOpError {
		return abandonChallengeEconomy(c.Request.Context(), tx, teamID, chalID, difficulty, h.config.Economy)
	})
}

// extends an open challenge's timer at an escalating credit cost.
func (h *ChallengeHandler) ExtendChallenge(c *gin.Context) {
	teamID, chalID, difficulty, ok := h.economyChallengeCtx(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "extend failed"})
		return
	}
	defer tx.Rollback(ctx)
	newExpiry, opErr := extendChallengeEconomy(ctx, tx, teamID, chalID, difficulty, h.config.Economy)
	if opErr != nil {
		c.JSON(opErr.Status, gin.H{"error": opErr.Message})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "extend failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "extended", "expires_at": newExpiry.Unix()})
}

func (h *ChallengeHandler) economyTx(c *gin.Context, op func(tx pgx.Tx) *EconomyOpError) {
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
		return
	}
	defer tx.Rollback(ctx)
	if opErr := op(tx); opErr != nil {
		c.JSON(opErr.Status, gin.H{"error": opErr.Message})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *ChallengeHandler) SubmitFlag(c *gin.Context) {
	slug := c.Param("slug")

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

	// economy mode: a team must have launched (opened) the challenge before it can submit; scoring is then dynamic and per-team.
	economyMode, err := isEconomyMode(c.Request.Context(), h.db)
	if err != nil {
		h.logger.Error("failed to read economy_mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
		return
	}
	var ecoTeamID *uuid.UUID
	var ecoDifficulty string
	if economyMode {
		tid, tErr := resolveTeamID(c.Request.Context(), h.db, uid)
		if tErr != nil {
			h.logger.Error("failed to resolve team for submit", zap.Error(tErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
		}
		if tid == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "join a team and launch this challenge before submitting"})
			return
		}
		ecoTeamID = tid
		var st string
		if sErr := h.db.Pool.QueryRow(c.Request.Context(),
			`SELECT COALESCE((SELECT status FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2), 'unopened'),
			        (SELECT difficulty FROM challenges WHERE id = $2)`,
			*tid, challengeID).Scan(&st, &ecoDifficulty); sErr != nil {
			h.logger.Error("failed to read economy challenge state", zap.Error(sErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "submission failed"})
			return
		}
		if st != "open" && st != "solved" {
			c.JSON(http.StatusForbidden, gin.H{"error": "launch this challenge before submitting"})
			return
		}
	}

	// after flagLockoutThreshold consecutive wrong attempts within flagLockoutWindow, the user is locked out for flagLockoutDuration.
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

	// container generates its own dynamic flag; admin defines a regex pattern.
	// any submission matching the regex is correct. duplicate values across
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
			// flag share detection: same exact value previously submitted by another user.
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

	// 1. look for a flag generated for this user → clean solve.
	// 2. if not found, check whether the value exists for any other user's instance
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
			matchedFlag.ID = dynFlagID
			matchedFlag.Name = dynFlagName
			matchedFlag.Points = dynPoints
			found = true
		} else if errors.Is(err, pgx.ErrNoRows) {
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
				// flag share detected — accept transparently, log silently
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

	// record the attempt and increment the challenge counter in one statement so
	// neither half can persist without the other.
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
		// economy: a wrong submission multiplicatively decays this team's eventual
		// point value for the challenge (it costs points, not credits). best-effort.
		if economyMode && ecoTeamID != nil {
			if _, ecErr := h.db.Pool.Exec(c.Request.Context(),
				`UPDATE economy_challenge_state SET wrong_subs = wrong_subs + 1
				 WHERE team_id = $1 AND challenge_id = $2 AND status = 'open'`,
				*ecoTeamID, challengeID); ecErr != nil {
				h.logger.Warn("failed to bump economy wrong_subs", zap.Error(ecErr))
			}
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

	// serialize solves for this challenge so its denormalized counts cannot lose
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

	// count the source rows rather than trusting total_flags, which admin flag
	// edits update separately and may briefly leave stale.
	var totalFlags int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM flags WHERE challenge_id = $1`, challengeID,
	).Scan(&totalFlags); err != nil {
		h.logger.Error("failed to count challenge flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
		return
	}

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

	// rowsAffected == 0 means the insert hit a conflict / already existed
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

	// update user and denormalized solve counts only for a newly inserted solve.
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

	// economy scoring: the team's dynamic point value for this
	// challenge (ceiling × crowd-decay × wrong-sub), retroactively recomputed for
	// all holders, plus the clean-solve credit refund. replaces the flat team
	// scoring below. idempotent per team+challenge.
	if economyMode && ecoTeamID != nil {
		if chalUUID, pErr := uuid.Parse(challengeID); pErr != nil {
			h.logger.Error("failed to parse challenge id for economy solve", zap.Error(pErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
			return
		} else if ecErr := applyEconomySolve(ctx, tx, h.config.Economy, *ecoTeamID, chalUUID, ecoDifficulty); ecErr != nil {
			h.logger.Error("failed to apply economy solve", zap.Error(ecErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record solve"})
			return
		}
	} else if teamsMode, tErr := isTeamsMode(ctx, h.db); tErr != nil {
		// team-aggregated scoring (teams mode, economy off): credit the team once
		// per distinct flag. best-effort; teams.total_score is denormalized.
		h.logger.Warn("teams_mode read failed during solve; skipping team score", zap.Error(tErr))
	} else if teamsMode {
		var teamID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT team_id FROM users WHERE id = $1`, uid).Scan(&teamID); err != nil {
			h.logger.Warn("team lookup failed during solve; skipping team score", zap.Error(err))
		} else if teamID != nil {
			var teammateHasFlag bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(
					SELECT 1 FROM solves s
					JOIN users u ON u.id = s.user_id
					WHERE u.team_id = $1 AND s.flag_id = $2 AND s.user_id <> $3)`,
				*teamID, matchedFlag.ID, uid).Scan(&teammateHasFlag); err != nil {
				h.logger.Warn("team dedup check failed during solve; skipping team score", zap.Error(err))
			} else if !teammateHasFlag {
				if _, err := tx.Exec(ctx,
					`UPDATE teams SET total_score = total_score + $1, updated_at = NOW() WHERE id = $2`,
					matchedFlag.Points, *teamID); err != nil {
					h.logger.Warn("team score update failed during solve", zap.Error(err))
				}
			}
		}
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

	if solvedFlags >= totalFlags && totalFlags > 0 {
		go h.cleanupSolvedInstance(challengeID, uid)
	}

	c.JSON(http.StatusOK, response)
}

// stops and removes the user's active instance for a fully-solved challenge;
// runs in a background goroutine so the submission response is not delayed.
func (h *ChallengeHandler) cleanupSolvedInstance(challengeID string, userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	// clear cooldown so the user isn't penalized for an auto-stop
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

func (h *ChallengeHandler) GetHints(c *gin.Context) {
	slug := c.Param("slug")

	var userID *uuid.UUID
	if uid, ok := contextUserID(c); ok {
		userID = &uid
	}

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

func (h *ChallengeHandler) UnlockHint(c *gin.Context) {
	slug := c.Param("slug")
	hintID := c.Param("hint_id")

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

	// serialize all hint purchases for this user so concurrent requests cannot
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
