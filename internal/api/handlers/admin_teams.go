package handlers

import (
	"encoding/json"
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

// AdminTeamsHandler manages the regular (teams_mode) teams from the admin panel.
// Distinct from GameAdminHandler, which owns arena/attack-defense teams.
type AdminTeamsHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewAdminTeamsHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *AdminTeamsHandler {
	return &AdminTeamsHandler{config: cfg, db: db, logger: logger}
}

func unixOrNil(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	u := t.Unix()
	return &u
}

// List returns every team with members, score and creator. ?q= filters by name,
// ?sort=score|created|name orders the result (score is the default).
func (h *AdminTeamsHandler) List(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	order := "t.total_score DESC, t.created_at DESC"
	switch c.Query("sort") {
	case "created":
		order = "t.created_at DESC"
	case "name":
		order = "LOWER(t.name) ASC"
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT t.id, t.name, t.join_code, t.total_score, t.max_members,
		       t.join_expires_at, t.created_at, cu.username,
		       COUNT(m.id) AS member_count,
		       COALESCE(
		           json_agg(json_build_object('id', m.id, 'username', m.username) ORDER BY m.username)
		           FILTER (WHERE m.id IS NOT NULL), '[]'
		       ) AS members
		FROM teams t
		LEFT JOIN users m ON m.team_id = t.id
		LEFT JOIN users cu ON cu.id = t.created_by
		WHERE ($1 = '' OR t.name ILIKE '%' || $1 || '%')
		GROUP BY t.id, cu.username
		ORDER BY `+order, q)
	if err != nil {
		h.logger.Error("failed to list teams", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch teams"})
		return
	}
	defer rows.Close()

	teams := []gin.H{}
	for rows.Next() {
		var id, name, joinCode string
		var createdBy *string
		var totalScore, memberCount int
		var maxMembers *int
		var joinExpires *time.Time
		var createdAt time.Time
		var membersJSON []byte

		if err := rows.Scan(&id, &name, &joinCode, &totalScore, &maxMembers,
			&joinExpires, &createdAt, &createdBy, &memberCount, &membersJSON); err != nil {
			h.logger.Error("failed to scan team", zap.Error(err))
			continue
		}

		var members []gin.H
		if err := json.Unmarshal(membersJSON, &members); err != nil {
			members = []gin.H{}
		}

		teams = append(teams, gin.H{
			"id":              id,
			"name":            name,
			"join_code":       joinCode,
			"total_score":     totalScore,
			"max_members":     maxMembers,
			"join_expires_at": unixOrNil(joinExpires),
			"created_at":      createdAt.Unix(),
			"created_by":      createdBy,
			"member_count":    memberCount,
			"members":         members,
		})
	}

	c.JSON(http.StatusOK, gin.H{"teams": teams, "total": len(teams)})
}

// Get returns one team's full detail.
func (h *AdminTeamsHandler) Get(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}

	var name, joinCode string
	var createdBy *string
	var totalScore, memberCount int
	var maxMembers *int
	var joinExpires *time.Time
	var createdAt time.Time
	var membersJSON []byte

	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT t.name, t.join_code, t.total_score, t.max_members,
		       t.join_expires_at, t.created_at, cu.username,
		       COUNT(m.id),
		       COALESCE(
		           json_agg(json_build_object('id', m.id, 'username', m.username) ORDER BY m.username)
		           FILTER (WHERE m.id IS NOT NULL), '[]'
		       )
		FROM teams t
		LEFT JOIN users m ON m.team_id = t.id
		LEFT JOIN users cu ON cu.id = t.created_by
		WHERE t.id = $1
		GROUP BY t.id, cu.username`, teamID).Scan(
		&name, &joinCode, &totalScore, &maxMembers, &joinExpires,
		&createdAt, &createdBy, &memberCount, &membersJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
			return
		}
		h.logger.Error("failed to fetch team", zap.String("team_id", teamID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch team"})
		return
	}

	var members []gin.H
	if err := json.Unmarshal(membersJSON, &members); err != nil {
		members = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              teamID,
		"name":            name,
		"join_code":       joinCode,
		"total_score":     totalScore,
		"max_members":     maxMembers,
		"join_expires_at": unixOrNil(joinExpires),
		"created_at":      createdAt.Unix(),
		"created_by":      createdBy,
		"member_count":    memberCount,
		"members":         members,
	})
}

// Update applies any subset of {name, total_score, max_members, join_expires_at}.
// max_members and join_expires_at accept null to clear (unlimited / no expiry).
func (h *AdminTeamsHandler) Update(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}

	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
		return
	}
	defer tx.Rollback(ctx)

	var currentMembers int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE team_id = $1`, teamID).Scan(&currentMembers)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
		return
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id = $1 FOR UPDATE)`, teamID).Scan(&exists); err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}

	changed := map[string]interface{}{}

	if v, ok := raw["name"]; ok {
		var name string
		if err := json.Unmarshal(v, &name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must be a string"})
			return
		}
		name = strings.TrimSpace(name)
		if name == "" || len(name) > maxTeamNameLen {
			c.JSON(http.StatusBadRequest, gin.H{"error": "team name must be 1-100 characters"})
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE teams SET name = $1, updated_at = NOW() WHERE id = $2`, name, teamID); err != nil {
			if postgresErrorCode(err) == "23505" {
				c.JSON(http.StatusConflict, gin.H{"error": "a team with that name already exists"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
			return
		}
		changed["name"] = name
	}

	if v, ok := raw["total_score"]; ok {
		var score int
		if err := json.Unmarshal(v, &score); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "total_score must be an integer"})
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE teams SET total_score = $1, updated_at = NOW() WHERE id = $2`, score, teamID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
			return
		}
		changed["total_score"] = score
	}

	if v, ok := raw["max_members"]; ok {
		if string(v) == "null" {
			if _, err := tx.Exec(ctx, `UPDATE teams SET max_members = NULL, updated_at = NOW() WHERE id = $1`, teamID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
				return
			}
			changed["max_members"] = nil
		} else {
			var mm int
			if err := json.Unmarshal(v, &mm); err != nil || mm < 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "max_members must be a positive integer or null"})
				return
			}
			if mm < currentMembers {
				c.JSON(http.StatusConflict, gin.H{"error": "max_members is below the current member count"})
				return
			}
			if _, err := tx.Exec(ctx, `UPDATE teams SET max_members = $1, updated_at = NOW() WHERE id = $2`, mm, teamID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
				return
			}
			changed["max_members"] = mm
		}
	}

	if v, ok := raw["join_expires_at"]; ok {
		if string(v) == "null" {
			if _, err := tx.Exec(ctx, `UPDATE teams SET join_expires_at = NULL, updated_at = NOW() WHERE id = $1`, teamID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
				return
			}
			changed["join_expires_at"] = nil
		} else {
			var ts int64
			if err := json.Unmarshal(v, &ts); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "join_expires_at must be a unix timestamp or null"})
				return
			}
			if _, err := tx.Exec(ctx, `UPDATE teams SET join_expires_at = $1, updated_at = NOW() WHERE id = $2`, time.Unix(ts, 0).UTC(), teamID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
				return
			}
			changed["join_expires_at"] = ts
		}
	}

	if len(changed) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no updatable fields provided"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
		return
	}

	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "team_updated", "team", teamID, changed); err != nil {
			h.logger.Warn("failed to audit team update", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "team updated", "changed": changed})
}

// Delete disbands a team: detach its members, then remove the team. Audited.
func (h *AdminTeamsHandler) Delete(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}
	ctx := c.Request.Context()

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disband team"})
		return
	}
	defer tx.Rollback(ctx)

	var name string
	err = tx.QueryRow(ctx, `SELECT name FROM teams WHERE id = $1 FOR UPDATE`, teamID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disband team"})
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET team_id = NULL WHERE team_id = $1`, teamID); err != nil {
		h.logger.Error("failed to detach team members", zap.String("team_id", teamID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disband team"})
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM teams WHERE id = $1`, teamID); err != nil {
		h.logger.Error("failed to delete team", zap.String("team_id", teamID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disband team"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disband team"})
		return
	}

	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "team_disbanded", "team", teamID, map[string]interface{}{"name": name}); err != nil {
			h.logger.Warn("failed to audit team disband", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "team disbanded"})
}

// AddMember moves a user (by user_id or username) into the team, respecting
// max_members. If the user is on another team they are moved.
func (h *AdminTeamsHandler) AddMember(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}
	var req struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id or username required"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}
	defer tx.Rollback(ctx)

	var teamName string
	var maxMembers *int
	err = tx.QueryRow(ctx, `SELECT name, max_members FROM teams WHERE id = $1 FOR UPDATE`, teamID).Scan(&teamName, &maxMembers)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	// resolve the target user + their current team
	var targetID, username string
	var currentTeam *string
	switch {
	case strings.TrimSpace(req.UserID) != "":
		if _, perr := uuid.Parse(strings.TrimSpace(req.UserID)); perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		err = tx.QueryRow(ctx, `SELECT id, username, team_id FROM users WHERE id = $1 FOR UPDATE`, strings.TrimSpace(req.UserID)).Scan(&targetID, &username, &currentTeam)
	case strings.TrimSpace(req.Username) != "":
		err = tx.QueryRow(ctx, `SELECT id, username, team_id FROM users WHERE username = $1 FOR UPDATE`, strings.TrimSpace(req.Username)).Scan(&targetID, &username, &currentTeam)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id or username required"})
		return
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	if currentTeam != nil && *currentTeam == teamID {
		c.JSON(http.StatusOK, gin.H{"message": "user already on this team"})
		return
	}

	if maxMembers != nil {
		var cnt int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE team_id = $1`, teamID).Scan(&cnt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
			return
		}
		if cnt >= *maxMembers {
			c.JSON(http.StatusConflict, gin.H{"error": "team is at capacity"})
			return
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET team_id = $1 WHERE id = $2`, teamID, targetID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "team_member_added", "team", teamID, map[string]interface{}{"user_id": targetID, "username": username}); err != nil {
			h.logger.Warn("failed to audit team member add", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "member added"})
}

// RemoveMember kicks a user from the team (nulls their team_id).
func (h *AdminTeamsHandler) RemoveMember(c *gin.Context) {
	teamID := c.Param("id")
	userID := c.Param("userId")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	tag, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET team_id = NULL WHERE id = $1 AND team_id = $2`, userID, teamID)
	if err != nil {
		h.logger.Error("failed to remove team member", zap.String("team_id", teamID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove member"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user is not a member of this team"})
		return
	}

	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "team_member_removed", "team", teamID, map[string]interface{}{"user_id": userID}); err != nil {
			h.logger.Warn("failed to audit team member remove", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

// RotateCode regenerates the team join code (same 10-hex generator as user create).
func (h *AdminTeamsHandler) RotateCode(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}

	code, err := generateSecureToken(5) // 10 hex chars
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rotate join code"})
		return
	}

	tag, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE teams SET join_code = $1, updated_at = NOW() WHERE id = $2`, code, teamID)
	if err != nil {
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "join code collision, try again"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rotate join code"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}

	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "team_code_rotated", "team", teamID, nil); err != nil {
			h.logger.Warn("failed to audit team code rotate", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"join_code": code})
}

// Solves lists every solve by a current member of the team, newest first.
// Solves are per-flag (a multi-flag challenge yields one row per solved flag).
// Read-only, so no audit.
func (h *AdminTeamsHandler) Solves(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := uuid.Parse(teamID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid team id"})
		return
	}
	ctx := c.Request.Context()

	var exists bool
	if err := h.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id = $1)`, teamID).Scan(&exists); err != nil {
		h.logger.Error("failed to check team", zap.String("team_id", teamID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch solves"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}

	rows, err := h.db.Pool.Query(ctx, `
		SELECT c.id, c.name, c.slug, f.name, s.user_id, u.username, s.points_awarded, s.solved_at
		FROM solves s
		JOIN users u ON u.id = s.user_id AND u.team_id = $1
		JOIN challenges c ON c.id = s.challenge_id
		LEFT JOIN flags f ON f.id = s.flag_id
		ORDER BY s.solved_at DESC`, teamID)
	if err != nil {
		h.logger.Error("failed to list team solves", zap.String("team_id", teamID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch solves"})
		return
	}
	defer rows.Close()

	solves := []gin.H{}
	for rows.Next() {
		var challengeID, challengeName, challengeSlug, solverID, solverUsername string
		var flagName *string
		var points int
		var solvedAt time.Time
		if err := rows.Scan(&challengeID, &challengeName, &challengeSlug, &flagName,
			&solverID, &solverUsername, &points, &solvedAt); err != nil {
			h.logger.Error("failed to scan team solve", zap.Error(err))
			continue
		}
		solves = append(solves, gin.H{
			"challenge_id":    challengeID,
			"challenge_name":  challengeName,
			"challenge_slug":  challengeSlug,
			"flag_name":       flagName,
			"solver_id":       solverID,
			"solver_username": solverUsername,
			"points":          points,
			"solved_at":       solvedAt.Unix(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"solves": solves, "total": len(solves)})
}
