package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type TeamsHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewTeamsHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *TeamsHandler {
	return &TeamsHandler{config: cfg, db: db, logger: logger}
}

func isTeamsMode(ctx context.Context, db *database.DB) (bool, error) {
	var enabled bool
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(
			(SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'teams_mode'),
			false)`,
	).Scan(&enabled)
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// boolSettingOrDefault reads a boolean platform_settings value, returning def when
// the key is absent. On a query error it returns def plus the error.
func boolSettingOrDefault(ctx context.Context, db *database.DB, key string, def bool) (bool, error) {
	var enabled bool
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT value = 'true'::jsonb FROM platform_settings WHERE key = $1), $2)`,
		key, def).Scan(&enabled)
	if err != nil {
		return def, err
	}
	return enabled, nil
}

func resolveTeamID(ctx context.Context, db *database.DB, userID uuid.UUID) (*uuid.UUID, error) {
	var teamID *uuid.UUID
	err := db.Pool.QueryRow(ctx,
		`SELECT team_id FROM users WHERE id = $1`, userID,
	).Scan(&teamID)
	if err != nil {
		return nil, err
	}
	return teamID, nil
}

const maxTeamNameLen = 100

type createTeamRequest struct {
	Name string `json:"name" binding:"required"`
}

type joinTeamRequest struct {
	JoinCode string `json:"join_code" binding:"required"`
}

func (h *TeamsHandler) requireTeamsMode(c *gin.Context) bool {
	on, err := isTeamsMode(c.Request.Context(), h.db)
	if err != nil {
		h.logger.Error("failed to read teams_mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check team mode"})
		return false
	}
	if !on {
		c.JSON(http.StatusForbidden, gin.H{"error": "team mode is not enabled"})
		return false
	}
	return true
}

func (h *TeamsHandler) Create(c *gin.Context) {
	if !h.requireTeamsMode(c) {
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req createTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team name is required"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxTeamNameLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team name must be 1-100 characters"})
		return
	}

	joinCode, err := generateSecureToken(5) // 10 hex chars
	if err != nil {
		h.logger.Error("failed to generate join code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}
	defer tx.Rollback(ctx)

	var existingTeam *uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT team_id FROM users WHERE id = $1 FOR UPDATE`, uid,
	).Scan(&existingTeam); err != nil {
		h.logger.Error("failed to load user for team create", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}
	if existingTeam != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "you are already on a team; leave it first"})
		return
	}

	teamID := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO teams (id, name, join_code, created_by) VALUES ($1, $2, $3, $4)`,
		teamID, name, joinCode, uid,
	); err != nil {
		if postgresErrorCode(err) == "23505" { // unique_violation
			c.JSON(http.StatusConflict, gin.H{"error": "a team with that name already exists"})
			return
		}
		h.logger.Error("failed to insert team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET team_id = $1 WHERE id = $2`, teamID, uid,
	); err != nil {
		h.logger.Error("failed to join creator to team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit team create", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        teamID.String(),
		"name":      name,
		"join_code": joinCode,
		"message":   "team created",
	})
}

func (h *TeamsHandler) Join(c *gin.Context) {
	if !h.requireTeamsMode(c) {
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req joinTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "join_code is required"})
		return
	}
	code := strings.TrimSpace(req.JoinCode)
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "join_code is required"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join team"})
		return
	}
	defer tx.Rollback(ctx)

	var teamID uuid.UUID
	var teamName string
	var expiresAt *time.Time
	var maxMembers *int
	err = tx.QueryRow(ctx,
		`SELECT id, name, join_expires_at, max_members FROM teams WHERE join_code = $1 FOR UPDATE`,
		code,
	).Scan(&teamID, &teamName, &expiresAt, &maxMembers)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid join code"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load team by join code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join team"})
		return
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		c.JSON(http.StatusForbidden, gin.H{"error": "this join code has expired"})
		return
	}

	var existingTeam *uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT team_id FROM users WHERE id = $1 FOR UPDATE`, uid,
	).Scan(&existingTeam); err != nil {
		h.logger.Error("failed to load user for team join", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join team"})
		return
	}
	if existingTeam != nil {
		if *existingTeam == teamID {
			c.JSON(http.StatusOK, gin.H{"id": teamID.String(), "name": teamName, "message": "already on this team"})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "you are already on a team; leave it first"})
		return
	}

	if maxMembers != nil {
		var memberCount int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE team_id = $1`, teamID,
		).Scan(&memberCount); err != nil {
			h.logger.Error("failed to count team members", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join team"})
			return
		}
		if memberCount >= *maxMembers {
			c.JSON(http.StatusForbidden, gin.H{"error": "this team is full"})
			return
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET team_id = $1 WHERE id = $2`, teamID, uid,
	); err != nil {
		h.logger.Error("failed to join team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join team"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit team join", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to join team"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": teamID.String(), "name": teamName, "message": "joined team"})
}

func (h *TeamsHandler) Leave(c *gin.Context) {
	if !h.requireTeamsMode(c) {
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	// membership is locked while the event is live: leaving to found a fresh team
	// (new credit grant, clean wrong-sub slate) and rejoining gamed the economy.
	// teamless players can still create or join.
	if phase, staff := eventPlayState(c, h.db); !staff && phase == "live" {
		c.JSON(http.StatusForbidden, gin.H{"error": "teams are locked during the event; ask an organizer to move you"})
		return
	}
	if _, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET team_id = NULL WHERE id = $1`, uid,
	); err != nil {
		h.logger.Error("failed to leave team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to leave team"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "left team"})
}

func (h *TeamsHandler) GetMine(c *gin.Context) {
	if !h.requireTeamsMode(c) {
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ctx := c.Request.Context()

	teamID, err := resolveTeamID(ctx, h.db, uid)
	if err != nil {
		h.logger.Error("failed to resolve team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team"})
		return
	}
	if teamID == nil {
		c.JSON(http.StatusOK, gin.H{"team": nil})
		return
	}

	economyMode, err := isEconomyMode(ctx, h.db)
	if err != nil {
		h.logger.Error("failed to read economy_mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team"})
		return
	}
	var name, joinCode string
	var totalScore int
	var maxMembers *int
	var expiresAt *time.Time
	// same total the board shows: economy points or jeopardy score, plus koth hold-time
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT name, join_code,
		        CASE WHEN $2 THEN ROUND(COALESCE((SELECT points FROM economy_team_score WHERE team_id = t.id), 0) + koth_score)::int
		             ELSE (total_score + koth_score)::int END,
		        max_members, join_expires_at
		 FROM teams t WHERE id = $1`,
		*teamID, economyMode,
	).Scan(&name, &joinCode, &totalScore, &maxMembers, &expiresAt); err != nil {
		h.logger.Error("failed to load team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team"})
		return
	}

	rows, err := h.db.Pool.Query(ctx,
		`SELECT u.id, u.username, u.display_name,
		        COALESCE((SELECT COUNT(*) FROM solves s WHERE s.user_id = u.id), 0) AS solve_count
		 FROM users u
		 WHERE u.team_id = $1
		 ORDER BY solve_count DESC, u.username ASC`,
		*teamID,
	)
	if err != nil {
		h.logger.Error("failed to load team roster", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team"})
		return
	}
	defer rows.Close()

	members := make([]gin.H, 0)
	for rows.Next() {
		var mid uuid.UUID
		var mname string
		var mdisplay *string
		var solves int
		if err := rows.Scan(&mid, &mname, &mdisplay, &solves); err != nil {
			h.logger.Error("failed to scan team member", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team"})
			return
		}
		members = append(members, gin.H{
			"id": mid.String(), "username": mname, "display_name": mdisplay, "solve_count": solves,
		})
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed iterating team roster", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load team"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"team": gin.H{
			"id":              teamID.String(),
			"name":            name,
			"join_code":       joinCode,
			"total_score":     totalScore,
			"max_members":     maxMembers,
			"join_expires_at": expiresAt,
			"members":         members,
		},
	})
}
