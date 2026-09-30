package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/distribution/reference"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

type AdminService struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	logger       *zap.Logger
}

const activeAdminMutationLockID int64 = 0x416e76696c41646d

func NewAdminService(cfg *config.Config, db *database.DB, containerSvc *container.Service, logger *zap.Logger) *AdminService {
	return &AdminService{config: cfg, db: db, containerSvc: containerSvc, logger: logger}
}

func (h *AdminUserHandler) List(c *gin.Context) {
	query := `
		SELECT id, username, email, role, status, total_score, created_at
		FROM users
		ORDER BY created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to list users", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch users"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id, username, role, status string
		var email *string
		var totalScore int
		var createdAt time.Time

		if err := rows.Scan(&id, &username, &email, &role, &status, &totalScore, &createdAt); err != nil {
			h.logger.Error("failed to scan user", zap.Error(err))
			continue
		}

		users = append(users, gin.H{
			"id":          id,
			"username":    username,
			"email":       email,
			"role":        role,
			"status":      status,
			"is_banned":   status == "banned",
			"total_score": totalScore,
			"created_at":  createdAt.Unix(),
		})
	}

	if users == nil {
		users = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"users": users, "total": len(users)})
}

func (h *AdminUserHandler) Get(c *gin.Context) {
	userID := c.Param("id")

	var user struct {
		ID         string
		Username   string
		Email      *string
		Role       string
		Status     string
		TotalScore int
		CreatedAt  time.Time
	}

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, username, email, role, status, total_score, created_at
		 FROM users WHERE id = $1`, userID).Scan(
		&user.ID, &user.Username, &user.Email, &user.Role, &user.Status,
		&user.TotalScore, &user.CreatedAt,
	)
	if err != nil {
		h.logger.Warn("failed to fetch user", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          user.ID,
		"username":    user.Username,
		"email":       user.Email,
		"role":        user.Role,
		"status":      user.Status,
		"total_score": user.TotalScore,
		"is_banned":   user.Status == "banned",
		"created_at":  user.CreatedAt.Unix(),
	})
}

// Detail is the per-user support view for the crew: the user's own points/solves,
// the team they belong to (with the team's economy line), and every instance the
// user currently has running. Read-only; one place to answer "what does this
// player have going on" without hand-written SQL.
func (h *AdminUserHandler) Detail(c *gin.Context) {
	userID := c.Param("id")
	if _, err := uuid.Parse(userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	ctx := c.Request.Context()

	var username, role, status string
	var email, displayName, avatarURL, bio, teamID, teamName *string
	var totalScore int
	var emailVerified bool
	var createdAt, updatedAt time.Time
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT u.username, u.email, u.display_name, u.avatar_url, u.bio, u.role, u.status,
		        COALESCE(u.total_score,0), u.email_verified, u.created_at, u.updated_at,
		        u.team_id::text, t.name
		 FROM users u LEFT JOIN teams t ON t.id = u.team_id WHERE u.id = $1`, userID,
	).Scan(&username, &email, &displayName, &avatarURL, &bio, &role, &status, &totalScore,
		&emailVerified, &createdAt, &updatedAt, &teamID, &teamName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var solveCount int
	_ = h.db.Pool.QueryRow(ctx, `SELECT COUNT(DISTINCT flag_id) FROM solves WHERE user_id = $1`, userID).Scan(&solveCount)
	var submissionCount, correctSubmissions int
	_ = h.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE is_correct) FROM submissions WHERE user_id = $1`,
		userID,
	).Scan(&submissionCount, &correctSubmissions)
	var lastLoginAt *time.Time
	var lastLoginIP *string
	_ = h.db.Pool.QueryRow(ctx,
		`SELECT last_login_at, last_login_ip FROM users WHERE id = $1`, userID,
	).Scan(&lastLoginAt, &lastLoginIP)

	team := gin.H(nil)
	if teamID != nil {
		var credits, points float64
		var bailoutUsed bool
		_ = h.db.Pool.QueryRow(ctx,
			`SELECT COALESCE(credits,0), COALESCE(points,0), COALESCE(bailout_used,false)
			 FROM economy_team_score WHERE team_id = $1`, *teamID).Scan(&credits, &points, &bailoutUsed)
		team = gin.H{"id": *teamID, "name": teamName, "credits": credits, "points": points, "bailout_used": bailoutUsed}
	}

	instances := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx,
		`SELECT i.id, c.name, c.slug, i.status::text, i.created_at, i.expires_at,
		        COALESCE(i.container_id,''), i.stopped_at, COALESCE(i.error_message,'')
		 FROM instances i JOIN challenges c ON c.id = i.challenge_id
		 WHERE i.user_id = $1
		 ORDER BY i.created_at DESC LIMIT 200`, userID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, name, slug, st, cid, errorMessage string
			var createdAt time.Time
			var expiresAt, stoppedAt *time.Time
			if rows.Scan(&id, &name, &slug, &st, &createdAt, &expiresAt, &cid, &stoppedAt, &errorMessage) == nil {
				it := gin.H{"id": id, "challenge_name": name, "challenge_slug": slug, "status": st,
					"created_at": createdAt.Unix(), "has_runtime": cid != "", "error_message": errorMessage}
				if expiresAt != nil {
					it["expires_at"] = expiresAt.Unix()
				}
				if stoppedAt != nil {
					it["stopped_at"] = stoppedAt.Unix()
				}
				instances = append(instances, it)
			}
		}
	}

	solves := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx,
		`SELECT c.id, c.name, c.slug, f.name, s.points_awarded, s.solved_at
		 FROM solves s
		 JOIN challenges c ON c.id = s.challenge_id
		 LEFT JOIN flags f ON f.id = s.flag_id
		 WHERE s.user_id = $1
		 ORDER BY s.solved_at DESC LIMIT 200`, userID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var challengeID, challengeName, challengeSlug string
			var flagName *string
			var points int
			var solvedAt time.Time
			if rows.Scan(&challengeID, &challengeName, &challengeSlug, &flagName, &points, &solvedAt) == nil {
				solves = append(solves, gin.H{
					"challenge_id": challengeID, "challenge_name": challengeName, "challenge_slug": challengeSlug,
					"flag_name": flagName, "points": points, "solved_at": solvedAt.Unix(),
				})
			}
		}
	}

	submissions := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx,
		`SELECT s.id, c.name, c.slug, f.name, s.instance_id, s.submitted_flag, s.is_correct,
		        COALESCE(s.points_awarded, 0), s.ip_address, LEFT(COALESCE(s.user_agent, ''), 500), s.created_at
		 FROM submissions s
		 JOIN challenges c ON c.id = s.challenge_id
		 LEFT JOIN flags f ON f.id = s.flag_id
		 WHERE s.user_id = $1
		 ORDER BY s.created_at DESC LIMIT 200`, userID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, challengeName, challengeSlug, submittedFlag, userAgent string
			var flagName, instanceID, ipAddress *string
			var correct bool
			var points int
			var createdAt time.Time
			if rows.Scan(&id, &challengeName, &challengeSlug, &flagName, &instanceID, &submittedFlag,
				&correct, &points, &ipAddress, &userAgent, &createdAt) == nil {
				submissions = append(submissions, gin.H{
					"id": id, "challenge_name": challengeName, "challenge_slug": challengeSlug,
					"flag_name": flagName, "flag_fingerprint": hashFlag(submittedFlag)[:16],
					"flag_length": len(submittedFlag), "correct": correct, "points": points,
					"instance_id": instanceID, "ip_address": ipAddress, "user_agent": userAgent,
					"submitted_at": createdAt.Unix(),
				})
			}
		}
	}

	lastLogin := gin.H{"ip_address": lastLoginIP}
	if lastLoginAt != nil {
		lastLogin["at"] = lastLoginAt.Unix()
	}

	sessions := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx,
		`SELECT id, ip_address, LEFT(COALESCE(user_agent, ''), 500), created_at, expires_at
		 FROM sessions WHERE user_id = $1 ORDER BY created_at DESC LIMIT 200`, userID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, userAgent string
			var ipAddress *string
			var sessionCreated, expiresAt time.Time
			if rows.Scan(&id, &ipAddress, &userAgent, &sessionCreated, &expiresAt) == nil {
				sessions = append(sessions, gin.H{"id": id, "ip_address": ipAddress,
					"user_agent": userAgent, "created_at": sessionCreated.Unix(),
					"expires_at": expiresAt.Unix(), "active": expiresAt.After(time.Now())})
			}
		}
	}

	accessIPs := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		WITH observations AS (
			SELECT ip_address, created_at AS seen_at, 'submission'::text AS source
			FROM submissions WHERE user_id = $1 AND ip_address IS NOT NULL
			UNION ALL
			SELECT ip_address, created_at, 'session'::text
			FROM sessions WHERE user_id = $1 AND ip_address IS NOT NULL
			UNION ALL
			SELECT last_login_ip, last_login_at, 'last_login'::text
			FROM users WHERE id = $1 AND last_login_ip IS NOT NULL AND last_login_at IS NOT NULL
		)
		SELECT ip_address, COUNT(*)::int, MIN(seen_at), MAX(seen_at), ARRAY_AGG(DISTINCT source ORDER BY source)
		FROM observations GROUP BY ip_address ORDER BY MAX(seen_at) DESC`, userID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var ipAddress string
			var count int
			var firstSeen, lastSeen time.Time
			var sources []string
			if rows.Scan(&ipAddress, &count, &firstSeen, &lastSeen, &sources) == nil {
				accessIPs = append(accessIPs, gin.H{"ip_address": ipAddress, "events": count,
					"first_seen_at": firstSeen.Unix(), "last_seen_at": lastSeen.Unix(), "sources": sources})
			}
		}
	}

	warnings := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT n.id, n.body, n.severity, n.pinned, n.created_by, COALESCE(a.username, ''),
		       n.publish_at, r.read_at, r.dismissed_at, n.cancelled_at
		FROM notification_items n
		LEFT JOIN users a ON a.id = n.created_by
		LEFT JOIN notification_receipts r ON r.item_id = n.id AND r.user_id = $1
		WHERE n.user_id = $1 AND n.event_type = 'organizer.warning'
		ORDER BY n.publish_at DESC LIMIT 200`, userID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, body, severity, actor string
			var actorID *uuid.UUID
			var pinned bool
			var publishedAt time.Time
			var readAt, dismissedAt, cancelledAt *time.Time
			if rows.Scan(&id, &body, &severity, &pinned, &actorID, &actor, &publishedAt, &readAt, &dismissedAt, &cancelledAt) == nil {
				entry := gin.H{"id": id, "body": body, "severity": severity, "pinned": pinned,
					"actor_id": actorID, "actor": actor, "published_at": publishedAt.Unix()}
				if readAt != nil {
					entry["read_at"] = readAt.Unix()
				}
				if dismissedAt != nil {
					entry["dismissed_at"] = dismissedAt.Unix()
				}
				if cancelledAt != nil {
					entry["cancelled_at"] = cancelledAt.Unix()
				}
				warnings = append(warnings, entry)
			}
		}
	}

	userUUID, _ := uuid.Parse(userID)
	audit, _ := adminEntityAudit(ctx, h.db, "user", userUUID)

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{"id": userID, "username": username, "email": email, "display_name": displayName,
			"avatar_url": avatarURL, "bio": bio, "email_verified": emailVerified,
			"role": role, "status": status, "total_score": totalScore, "solve_count": solveCount,
			"submission_count": submissionCount, "correct_submissions": correctSubmissions,
			"wrong_submissions": submissionCount - correctSubmissions, "last_login": lastLogin,
			"created_at": createdAt.Unix(), "updated_at": updatedAt.Unix()},
		"team":        team,
		"instances":   instances,
		"solves":      solves,
		"submissions": submissions,
		"sessions":    sessions,
		"access_ips":  accessIPs,
		"warnings":    warnings,
		"audit":       audit,
	})
}

func (h *AdminUserHandler) Update(c *gin.Context) {
	userID := c.Param("id")

	var req struct {
		Role       *string `json:"role"`
		TotalScore *int    `json:"total_score"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role == nil && req.TotalScore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no changes provided"})
		return
	}

	var newRole string
	if req.Role != nil {
		newRole = strings.ToLower(strings.TrimSpace(*req.Role))
		if newRole != "admin" && newRole != "user" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role: must be 'admin' or 'user'"})
			return
		}
	}

	ctx := c.Request.Context()
	tx, err := h.beginAdminUserMutation(ctx)
	if err != nil {
		h.logger.Error("failed to begin user update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
		return
	}
	defer tx.Rollback(ctx)

	var currentRole, currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT role, status FROM users WHERE id = $1`,
		userID,
	).Scan(&currentRole, &currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		} else {
			h.logger.Error("failed to fetch user for update", zap.String("user_id", userID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
		}
		return
	}

	if req.Role != nil {
		if currentRole == "admin" && currentStatus == "active" && newRole != "admin" {
			adminCount, err := countActiveAdminUsers(ctx, tx)
			if err != nil {
				h.logger.Error("failed to count admins", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
			if adminCount <= 1 {
				c.JSON(http.StatusConflict, gin.H{"error": "cannot demote the last admin"})
				return
			}
		}

		_, err = tx.Exec(ctx,
			`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`,
			newRole, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if req.TotalScore != nil {
		_, err := tx.Exec(ctx,
			`UPDATE users SET total_score = $1, updated_at = NOW() WHERE id = $2`,
			*req.TotalScore, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit user update", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user updated"})
}

func (h *AdminUserHandler) Ban(c *gin.Context) {
	userID := c.Param("id")
	ctx := c.Request.Context()

	tx, err := h.beginAdminUserMutation(ctx)
	if err != nil {
		h.logger.Error("failed to begin user ban", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ban user"})
		return
	}
	defer tx.Rollback(ctx)

	var role, status string
	err = tx.QueryRow(ctx, `SELECT role, status FROM users WHERE id = $1`, userID).Scan(&role, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		} else {
			h.logger.Error("failed to fetch user for ban", zap.String("user_id", userID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ban user"})
		}
		return
	}
	if role == "admin" && status == "active" {
		adminCount, err := countActiveAdminUsers(ctx, tx)
		if err != nil {
			h.logger.Error("failed to count admins", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ban user"})
			return
		}
		if adminCount <= 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "cannot ban the last active admin"})
			return
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET status = 'banned', updated_at = NOW() WHERE id = $1`, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ban user"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit user ban", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ban user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user banned"})
}

func (h *AdminUserHandler) Unban(c *gin.Context) {
	userID := c.Param("id")

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE users SET status = 'active', updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unban user"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user unbanned"})
}

func (h *AdminUserHandler) Warn(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid warning"})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len([]rune(req.Message)) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "warning must be between 1 and 1000 characters"})
		return
	}
	var username string
	if err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT username FROM users WHERE id = $1`, userID).Scan(&username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to warn user"})
		}
		return
	}
	adminID, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to warn user"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var notificationID uuid.UUID
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO notification_items
		(kind, event_type, title, body, severity, audience, user_id, publish_at, pinned, created_by)
		VALUES ('announcement', 'organizer.warning', 'Organizer warning', $1, 'warning', 'user', $2, NOW(), true, $3)
		RETURNING id`, req.Message, userID, adminID).Scan(&notificationID)
	if err != nil {
		h.logger.Error("failed to warn user", zap.String("user_id", userID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to warn user"})
		return
	}
	metadata, _ := json.Marshal(map[string]interface{}{
		"username": username, "notification_id": notificationID.String(),
	})
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent)
		VALUES ($1, 'user_warned', 'user', $2, $3::jsonb, $4, $5)`,
		adminID, userID, metadata, c.ClientIP(), c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to warn user"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to warn user"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "warning sent", "notification_id": notificationID})
}

func (h *AdminUserHandler) Delete(c *gin.Context) {
	userID := c.Param("id")
	ctx := c.Request.Context()

	tx, err := h.beginAdminUserMutation(ctx)
	if err != nil {
		h.logger.Error("failed to begin user deletion", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	defer tx.Rollback(ctx)

	var currentRole, currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT role, status FROM users WHERE id = $1`,
		userID,
	).Scan(&currentRole, &currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		} else {
			h.logger.Error("failed to fetch user for deletion", zap.String("user_id", userID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		}
		return
	}

	if currentRole == "admin" && currentStatus == "active" {
		adminCount, err := countActiveAdminUsers(ctx, tx)
		if err != nil {
			h.logger.Error("failed to count admins", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
			return
		}
		if adminCount <= 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "cannot delete the last admin"})
			return
		}
	}

	_, err = tx.Exec(ctx,
		`DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit user deletion", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

func (h *AdminUserHandler) beginAdminUserMutation(ctx context.Context) (pgx.Tx, error) {
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, activeAdminMutationLockID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func countActiveAdminUsers(ctx context.Context, tx pgx.Tx) (int, error) {
	var count int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND status = 'active'`,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

type FlagInput struct {
	Name              string `json:"name" binding:"required"`
	Description       string `json:"description"`
	Flag              string `json:"flag"`   // required for static; leave empty for dynamic
	Points            int    `json:"points"` // 0 is valid (e.g. survey/free flags)
	SortOrder         int    `json:"sort_order"`
	FlagType          string `json:"flag_type"`           // "static" (default) | "dynamic"
	DynamicFlagPrefix string `json:"dynamic_flag_prefix"` // e.g. "H7CTF" → "H7CTF{uuid}"
}

type CreateChallengeRequest struct {
	Name           string  `json:"name" binding:"required"`
	Description    string  `json:"description"`
	SubDescription string  `json:"sub_description"` // optional plain-text pre-launch blurb (<=255 chars)
	Difficulty     string  `json:"difficulty" binding:"required"`
	CategoryID     *string `json:"category_id"`
	Category       string  `json:"category"` // human-readable name; auto-resolved to category_id

	// challenge type: "docker" or "vm"
	ChallengeType string `json:"challenge_type"`

	ContainerImage    string `json:"container_image"`
	ContainerTag      string `json:"container_tag"`
	ContainerPlatform string `json:"container_platform"` // e.g. "linux/amd64" for cross-arch
	CPULimit          string `json:"cpu_limit"`
	MemoryLimit       string `json:"memory_limit"`
	ExposedPorts      []struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Service  string `json:"service"`
	} `json:"exposed_ports"`

	// Optional multi-container (compose-style) roles. Empty => single-image
	// challenge (uses ContainerImage/ExposedPorts as before).
	Services []ContainerService `json:"services"`

	VMTemplateID *string `json:"vm_template_id"`
	VCPU         int     `json:"vcpu"`
	MemoryMB     int     `json:"memory_mb"`

	VMTimeoutMinutes   *int `json:"vm_timeout_minutes"`   // nil = use difficulty default
	VMMaxExtensions    *int `json:"vm_max_extensions"`    // default 2
	VMExtensionMinutes *int `json:"vm_extension_minutes"` // default 30
	CooldownMinutes    *int `json:"cooldown_minutes"`     // default 10

	BasePoints      int     `json:"base_points"`
	InstanceTimeout *int    `json:"instance_timeout"`
	MaxExtensions   *int    `json:"max_extensions"`
	AuthorName      string  `json:"author_name"`
	ResourceType    *string `json:"resource_type"` // "docker" or "vm"
	DeliveryType    string  `json:"delivery_type"`

	// arena_mode: "per_team" (default) or "shared" (one contested KotH target the
	// whole field attacks). Empty = leave unchanged (update) / default (create).
	ArenaMode string `json:"arena_mode"`

	// privesc: relax the container securityContext (allowPrivilegeEscalation:true /
	// no_new_privs off) for boot-to-root / SUID-privesc challenges. Caps stay dropped.
	Privesc bool `json:"privesc"`

	// scoring_mode: "flag" (default) or "graded" (an in-instance grader reports a
	// score in [0,1]). Empty = leave unchanged (update) / default (create).
	ScoringMode string `json:"scoring_mode"`

	Flags []FlagInput `json:"flags"`

	// legacy single flag support, kept for backward compatibility
	Flag string `json:"flag"`
}

// ContainerService is one role of a multi-container (compose-style) challenge.
// Stored verbatim as challenges.container_spec (JSONB) and consumed at launch.
type ContainerService struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`   // optional; defaults to the challenge's container_image
	Tag     string   `json:"tag"`     // optional; defaults to container_tag
	Command []string `json:"command"` // compose command -> k8s container args
	Public  bool     `json:"public"`  // only public roles get a route
	Egress  bool     `json:"egress"`  // opt into outbound internet
	Ports   []struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Service  string `json:"service"`
		Internal bool   `json:"internal"` // ClusterIP-only (peer-reachable by role name), no player route
	} `json:"ports"`
	Env         map[string]string `json:"env"`
	CPULimit    string            `json:"cpu_limit"`
	MemoryLimit string            `json:"memory_limit"`
}

var containerServiceName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validateChallengeRequest(req *CreateChallengeRequest, creating bool) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 200 {
		return errors.New("name must be between 1 and 200 characters")
	}
	switch req.Difficulty {
	case "easy", "medium", "hard", "insane":
	default:
		return errors.New("difficulty must be easy, medium, hard, or insane")
	}
	if req.BasePoints < 0 || req.BasePoints > 1_000_000 {
		return errors.New("base_points must be between 0 and 1000000")
	}
	if req.ScoringMode != "" && req.ScoringMode != "flag" && req.ScoringMode != "graded" {
		return errors.New("scoring_mode must be flag or graded")
	}
	if req.ArenaMode != "" && req.ArenaMode != "per_team" && req.ArenaMode != "shared" {
		return errors.New("arena_mode must be per_team or shared")
	}
	if strings.TrimSpace(req.ContainerImage) != "" {
		if err := validateContainerImageReference(req.ContainerImage); err != nil {
			return fmt.Errorf("container_image %w", err)
		}
	}
	seenPorts := map[string]bool{}
	for _, port := range req.ExposedPorts {
		if port.Port < 1 || port.Port > 65535 {
			return fmt.Errorf("exposed port %d is outside 1-65535", port.Port)
		}
		protocol := strings.ToLower(strings.TrimSpace(port.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		if protocol != "tcp" && protocol != "udp" {
			return fmt.Errorf("exposed port %d has an unsupported protocol", port.Port)
		}
		service := strings.ToLower(strings.TrimSpace(port.Service))
		if service != "" && service != "tcp" && service != "http" {
			return fmt.Errorf("exposed port %d has an unsupported service", port.Port)
		}
		key := fmt.Sprintf("%d/%s", port.Port, protocol)
		if seenPorts[key] {
			return fmt.Errorf("exposed port %s is duplicated", key)
		}
		seenPorts[key] = true
	}
	serviceNames := map[string]bool{}
	for index := range req.Services {
		service := &req.Services[index]
		service.Name = strings.ToLower(strings.TrimSpace(service.Name))
		if !containerServiceName.MatchString(service.Name) {
			return fmt.Errorf("service %d needs a DNS-safe name", index+1)
		}
		if serviceNames[service.Name] {
			return fmt.Errorf("service name %q is duplicated", service.Name)
		}
		serviceNames[service.Name] = true
		if strings.TrimSpace(service.Image) == "" && strings.TrimSpace(req.ContainerImage) == "" {
			return fmt.Errorf("service %q needs an image", service.Name)
		}
		if strings.TrimSpace(service.Image) != "" {
			if err := validateContainerImageReference(service.Image); err != nil {
				return fmt.Errorf("service %q image %w", service.Name, err)
			}
		}
		for _, port := range service.Ports {
			if port.Port < 1 || port.Port > 65535 {
				return fmt.Errorf("service %q has a port outside 1-65535", service.Name)
			}
			if port.Service != "" && port.Service != "tcp" && port.Service != "http" {
				return fmt.Errorf("service %q has an unsupported route type", service.Name)
			}
		}
	}
	if creating {
		for index := range req.Flags {
			flag := &req.Flags[index]
			flag.Name = strings.TrimSpace(flag.Name)
			if flag.Name == "" {
				return fmt.Errorf("flag %d needs a name", index+1)
			}
			if flag.Points < 0 || flag.Points > 1_000_000 {
				return fmt.Errorf("flag %q has invalid points", flag.Name)
			}
			if flag.FlagType == "" {
				flag.FlagType = "static"
			}
			switch flag.FlagType {
			case "static":
				if flag.Flag == "" {
					return fmt.Errorf("flag %q needs a value", flag.Name)
				}
			case "regex":
				if flag.Flag == "" {
					return fmt.Errorf("flag %q needs a regular expression", flag.Name)
				}
				if _, err := regexp.Compile(flag.Flag); err != nil {
					return fmt.Errorf("flag %q has an invalid regular expression", flag.Name)
				}
			case "dynamic":
				if strings.TrimSpace(flag.DynamicFlagPrefix) == "" {
					return fmt.Errorf("flag %q needs a dynamic prefix", flag.Name)
				}
			default:
				return fmt.Errorf("flag %q has an unsupported type", flag.Name)
			}
		}
	}
	return nil
}

func validateContainerImageReference(value string) error {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return errors.New("must be at most 512 characters")
	}
	if _, err := reference.ParseNormalizedNamed(value); err != nil {
		return errors.New("must be a valid registry image reference")
	}
	return nil
}

func (h *AdminChallengeHandler) List(c *gin.Context) {
	query := `
		SELECT c.id, c.name, c.slug, c.description, c.difficulty, c.status, c.base_points,
		       c.total_solves, c.total_flags, c.resource_type, c.created_at,
		       c.category_id, cat.name AS category_name,
		       c.container_image, c.container_tag, c.container_platform,
		       c.cpu_limit, c.memory_limit, c.exposed_ports,
		       c.instance_timeout, c.max_extensions, c.cooldown_minutes,
		       c.author_name, c.scoring_mode, c.sub_description, c.container_spec,
		       c.privesc, c.arena_mode, c.release_date, c.total_attempts,
		       c.economy_solve_count, c.supports_docker, c.supports_vm,
		       c.vm_timeout_minutes, c.vm_max_extensions, c.vm_extension_minutes,
		       c.delivery_type,
		       EXISTS (SELECT 1 FROM challenge_attachments ca WHERE ca.challenge_id = c.id),
		       (SELECT cr.vm_template_id::text FROM challenge_resources cr
		        WHERE cr.challenge_id = c.id AND cr.resource_type = 'vm' AND cr.is_active
		        ORDER BY cr.created_at DESC LIMIT 1)
		FROM challenges c
		LEFT JOIN categories cat ON cat.id = c.category_id
		ORDER BY c.created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to list challenges", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenges"})
		return
	}
	defer rows.Close()

	var challenges []gin.H
	for rows.Next() {
		var ch struct {
			ID                 string
			Name               string
			Slug               string
			Description        *string
			Difficulty         string
			Status             string
			BasePoints         int
			TotalSolves        int
			TotalFlags         int
			ResourceType       string
			CreatedAt          time.Time
			CategoryID         *string
			CategoryName       *string
			ContainerImage     *string
			ContainerTag       *string
			ContainerPlatform  *string
			CPULimit           *string
			MemoryLimit        *string
			ExposedPorts       []byte
			InstanceTimeout    *int
			MaxExtensions      *int
			CooldownMinutes    *int
			AuthorName         *string
			ScoringMode        string
			SubDescription     *string
			ContainerSpec      []byte
			Privesc            bool
			ArenaMode          string
			ReleaseDate        *time.Time
			TotalAttempts      int
			EconomySolves      int
			SupportsDocker     bool
			SupportsVM         bool
			VMTimeoutMinutes   *int
			VMMaxExtensions    *int
			VMExtensionMinutes *int
			DeliveryType       string
			HasAttachments     bool
			VMTemplateID       *string
		}

		if err := rows.Scan(
			&ch.ID, &ch.Name, &ch.Slug, &ch.Description, &ch.Difficulty,
			&ch.Status, &ch.BasePoints, &ch.TotalSolves, &ch.TotalFlags, &ch.ResourceType, &ch.CreatedAt,
			&ch.CategoryID, &ch.CategoryName,
			&ch.ContainerImage, &ch.ContainerTag, &ch.ContainerPlatform,
			&ch.CPULimit, &ch.MemoryLimit, &ch.ExposedPorts,
			&ch.InstanceTimeout, &ch.MaxExtensions, &ch.CooldownMinutes,
			&ch.AuthorName, &ch.ScoringMode, &ch.SubDescription, &ch.ContainerSpec,
			&ch.Privesc, &ch.ArenaMode, &ch.ReleaseDate, &ch.TotalAttempts,
			&ch.EconomySolves, &ch.SupportsDocker, &ch.SupportsVM,
			&ch.VMTimeoutMinutes, &ch.VMMaxExtensions, &ch.VMExtensionMinutes, &ch.DeliveryType, &ch.HasAttachments, &ch.VMTemplateID,
		); err != nil {
			h.logger.Warn("failed to scan challenge row", zap.Error(err))
			continue
		}

		var exposedPorts interface{}
		if len(ch.ExposedPorts) > 0 {
			_ = json.Unmarshal(ch.ExposedPorts, &exposedPorts)
		}
		services := []ContainerService{}
		if len(ch.ContainerSpec) > 0 {
			_ = json.Unmarshal(ch.ContainerSpec, &services)
		}
		deliveryType := ch.DeliveryType
		if deliveryType == "docker" && len(services) > 0 {
			deliveryType = "multi"
		} else if deliveryType == "docker" {
			deliveryType = "container"
		}

		challenges = append(challenges, gin.H{
			"id":                   ch.ID,
			"name":                 ch.Name,
			"slug":                 ch.Slug,
			"description":          ch.Description,
			"difficulty":           ch.Difficulty,
			"status":               ch.Status,
			"base_points":          ch.BasePoints,
			"total_solves":         ch.TotalSolves,
			"total_flags":          ch.TotalFlags,
			"resource_type":        ch.ResourceType,
			"created_at":           ch.CreatedAt.Unix(),
			"category_id":          ch.CategoryID,
			"category_name":        ch.CategoryName,
			"container_image":      ch.ContainerImage,
			"container_tag":        ch.ContainerTag,
			"container_platform":   ch.ContainerPlatform,
			"cpu_limit":            ch.CPULimit,
			"memory_limit":         ch.MemoryLimit,
			"exposed_ports":        exposedPorts,
			"instance_timeout":     ch.InstanceTimeout,
			"max_extensions":       ch.MaxExtensions,
			"cooldown_minutes":     ch.CooldownMinutes,
			"author_name":          ch.AuthorName,
			"scoring_mode":         ch.ScoringMode,
			"sub_description":      ch.SubDescription,
			"services":             services,
			"privesc":              ch.Privesc,
			"arena_mode":           ch.ArenaMode,
			"release_date":         unixOrNil(ch.ReleaseDate),
			"total_attempts":       ch.TotalAttempts,
			"economy_solve_count":  ch.EconomySolves,
			"supports_docker":      ch.SupportsDocker,
			"supports_vm":          ch.SupportsVM,
			"vm_timeout_minutes":   ch.VMTimeoutMinutes,
			"vm_max_extensions":    ch.VMMaxExtensions,
			"vm_extension_minutes": ch.VMExtensionMinutes,
			"vm_template_id":       ch.VMTemplateID,
			"delivery_type":        deliveryType,
			"has_attachments":      ch.HasAttachments,
		})
	}

	if challenges == nil {
		challenges = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"challenges": challenges, "total": len(challenges)})
}

var reMilliMemory = regexp.MustCompile(`^(\d+)m$`)

// normalizeMemoryLimit guards the "512m" footgun: a bare "<n>m" suffix is
// millibytes (~0 bytes), which silently bricks the gVisor sandbox at launch
// ("failed to create systemd scope"). Nobody means millibytes for memory, so
// treat it as the mebibytes that were intended.
func normalizeMemoryLimit(mem string) string {
	if m := reMilliMemory.FindStringSubmatch(mem); m != nil {
		return m[1] + "Mi"
	}
	return mem
}

func (h *AdminChallengeHandler) Create(c *gin.Context) {
	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	challengeType := req.ChallengeType
	if challengeType == "" {
		challengeType = "docker" // default
	}
	if challengeType != "docker" && challengeType != "vm" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "challenge_type must be docker or vm"})
		return
	}
	deliveryType := strings.ToLower(strings.TrimSpace(req.DeliveryType))
	if deliveryType == "" {
		switch {
		case challengeType == "vm":
			deliveryType = "vm"
		case strings.TrimSpace(req.ContainerImage) != "" || len(req.Services) > 0:
			deliveryType = "docker"
		default:
			deliveryType = "static"
		}
	}
	if deliveryType != "docker" && deliveryType != "static" && deliveryType != "external" && deliveryType != "vm" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "delivery_type must be docker, static, external, or vm"})
		return
	}
	if (challengeType == "vm") != (deliveryType == "vm") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VM resource and delivery types must be selected together"})
		return
	}
	if deliveryType == "static" || deliveryType == "external" {
		req.ContainerImage = ""
		req.Services = nil
		req.ExposedPorts = nil
		req.Privesc = false
	}

	resourceType := "docker"
	supportsDocker := true
	supportsVM := false

	if challengeType == "vm" {
		resourceType = "vm"
		supportsDocker = false
		supportsVM = true
		if req.VCPU == 0 {
			req.VCPU = 1
		}
		if req.MemoryMB == 0 {
			req.MemoryMB = 1024
		}
	} else {
		if req.ContainerTag == "" {
			req.ContainerTag = "latest"
		}
		if req.CPULimit == "" {
			req.CPULimit = "1"
		}
		if req.MemoryLimit == "" {
			req.MemoryLimit = "512Mi"
		}
		req.MemoryLimit = normalizeMemoryLimit(req.MemoryLimit)
	}

	if req.BasePoints == 0 {
		req.BasePoints = 100
	}
	if challengeType == "vm" && len(req.Services) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VM challenges cannot define container services"})
		return
	}
	if err := validateChallengeRequest(&req, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	challengeSlug := slug.Make(req.Name)

	// category_id takes precedence over category (name) when both are supplied
	if req.Category != "" && req.CategoryID == nil {
		catID, err := h.resolveOrCreateCategory(c.Request.Context(), req.Category)
		if err != nil {
			h.logger.Error("failed to resolve category", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve category"})
			return
		}
		req.CategoryID = catID
	}

	portsJSON, _ := json.Marshal(req.ExposedPorts)
	var containerSpec []byte
	if len(req.Services) > 0 {
		containerSpec, _ = json.Marshal(req.Services)
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	challengeID := uuid.New()

	_, err = tx.Exec(c.Request.Context(),
		`INSERT INTO challenges (
			id, name, slug, description, difficulty, category_id, status,
			container_image, container_tag, container_platform, cpu_limit, memory_limit,
			exposed_ports, base_points, instance_timeout, max_extensions,
			vm_timeout_minutes, vm_max_extensions, vm_extension_minutes, cooldown_minutes,
			author_name, resource_type, delivery_type, supports_docker, supports_vm,
			total_flags, sub_description, container_spec, privesc, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, NOW(), NOW())`,
		challengeID, req.Name, challengeSlug, req.Description, req.Difficulty, req.CategoryID,
		req.ContainerImage, req.ContainerTag, req.ContainerPlatform, req.CPULimit, req.MemoryLimit,
		portsJSON, req.BasePoints, req.InstanceTimeout, req.MaxExtensions,
		req.VMTimeoutMinutes, req.VMMaxExtensions, req.VMExtensionMinutes, req.CooldownMinutes,
		req.AuthorName, resourceType, deliveryType, supportsDocker, supportsVM, len(req.Flags), subDescriptionOrNil(req.SubDescription),
		nilIfEmpty(containerSpec), req.Privesc,
	)
	if err != nil {
		h.logger.Error("failed to create challenge", zap.Error(err))
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "a challenge with this name or slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge: " + err.Error()})
		return
	}

	if req.ScoringMode != "" {
		if req.ScoringMode != "flag" && req.ScoringMode != "graded" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scoring_mode must be flag or graded"})
			return
		}
		if _, err = tx.Exec(c.Request.Context(),
			`UPDATE challenges SET scoring_mode = $1 WHERE id = $2`, req.ScoringMode, challengeID); err != nil {
			h.logger.Error("failed to set scoring_mode", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
			return
		}
	}

	// arena_mode defaults to 'per_team' in the schema; only flip a KotH challenge to 'shared'.
	if req.ArenaMode == "shared" {
		if _, err = tx.Exec(c.Request.Context(),
			`UPDATE challenges SET arena_mode = 'shared' WHERE id = $1`, challengeID); err != nil {
			h.logger.Error("failed to set arena_mode", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
			return
		}
	}

	flagsToCreate := req.Flags
	if len(flagsToCreate) == 0 && req.Flag != "" {
		flagsToCreate = []FlagInput{{
			Name:      "Flag",
			Flag:      req.Flag,
			Points:    req.BasePoints,
			SortOrder: 1,
		}}
	}

	for i, flag := range flagsToCreate {
		flagID := uuid.New()
		sortOrder := flag.SortOrder
		if sortOrder == 0 {
			sortOrder = i + 1
		}
		flagType := flag.FlagType
		if flagType == "" {
			flagType = "static"
		}
		// hash only for static flags; regex stores the pattern as-is; dynamic has no pre-set value
		flagHash := ""
		if flagType == "static" {
			flagHash = hashFlag(flag.Flag)
		} else if flagType == "regex" {
			if _, err := regexp.Compile(flag.Flag); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid regex pattern for flag '" + flag.Name + "': " + err.Error()})
				return
			}
			flagHash = flag.Flag
		}
		var dynPrefix *string
		if flag.DynamicFlagPrefix != "" {
			s := flag.DynamicFlagPrefix
			dynPrefix = &s
		}

		_, err = tx.Exec(c.Request.Context(),
			`INSERT INTO flags (id, challenge_id, name, description, flag_hash, points, sort_order,
			                    flag_type, dynamic_flag_prefix, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())`,
			flagID, challengeID, flag.Name, flag.Description, flagHash, flag.Points, sortOrder,
			flagType, dynPrefix,
		)
		if err != nil {
			h.logger.Error("failed to create flag", zap.Error(err), zap.String("flag_name", flag.Name))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag: " + err.Error()})
			return
		}
	}

	_, err = tx.Exec(c.Request.Context(),
		`UPDATE challenges SET total_flags = $1 WHERE id = $2`,
		len(flagsToCreate), challengeID,
	)
	if err != nil {
		h.logger.Error("failed to update flag count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
		return
	}

	if challengeType == "vm" && req.VMTemplateID != nil {
		resourceID := uuid.New()
		_, err = tx.Exec(c.Request.Context(),
			`INSERT INTO challenge_resources (id, challenge_id, resource_type, vm_template_id, cpu_limit, memory_limit, sort_order, is_active, created_at)
			 VALUES ($1, $2, 'vm', $3, $4, $5, 0, true, NOW())`,
			resourceID, challengeID, req.VMTemplateID,
			fmt.Sprintf("%d", req.VCPU), fmt.Sprintf("%dMB", req.MemoryMB),
		)
		if err != nil {
			h.logger.Error("failed to link VM template", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to link VM template"})
			return
		}
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		h.logger.Error("failed to commit transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create challenge"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          challengeID.String(),
		"slug":        challengeSlug,
		"total_flags": len(flagsToCreate),
		"message":     "challenge created",
	})
}

// normalizes an optional pre-launch sub_description: trims, caps to the column width (255 chars, rune-safe), and returns nil for an empty value so the column stores null rather than an empty string
// nilIfEmpty returns nil (→ SQL NULL) for empty bytes, else the bytes — so an
// absent multi-container spec stores NULL rather than invalid JSONB.
func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func subDescriptionOrNil(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if r := []rune(s); len(r) > 255 {
		s = string(r[:255])
	}
	return &s
}

func hashFlag(flag string) string {
	hash := sha256.Sum256([]byte(flag))
	return hex.EncodeToString(hash[:])
}

// looks up a category by name (case-insensitive) or creates it if absent; returns nil when categoryName is blank
func (h *AdminChallengeHandler) resolveOrCreateCategory(ctx context.Context, categoryName string) (*string, error) {
	trimmed := strings.TrimSpace(categoryName)
	if trimmed == "" {
		return nil, nil
	}

	var id string
	err := h.db.Pool.QueryRow(ctx,
		`SELECT id FROM categories WHERE LOWER(name) = LOWER($1)`, trimmed).Scan(&id)
	if err == nil {
		return &id, nil
	}

	// store a display-cased name (first letter upper) so categories read as
	// "Blockchain" not "blockchain"; admins can rename freely afterwards. lookup
	// stays case-insensitive so re-imports reuse the same category.
	displayName := trimmed
	if len(displayName) > 0 {
		displayName = strings.ToUpper(displayName[:1]) + displayName[1:]
	}
	newID := uuid.New().String()
	categorySlug := slug.Make(trimmed)
	_, err = h.db.Pool.Exec(ctx,
		`INSERT INTO categories (id, name, slug) VALUES ($1, $2, $3)`,
		newID, displayName, categorySlug)
	if err != nil {
		// another request may have created it concurrently; retry the lookup
		if retryErr := h.db.Pool.QueryRow(ctx,
			`SELECT id FROM categories WHERE LOWER(name) = LOWER($1)`, trimmed).Scan(&id); retryErr == nil {
			return &id, nil
		}
		return nil, fmt.Errorf("failed to create category: %w", err)
	}

	return &newID, nil
}

func (h *AdminChallengeHandler) CreateOVAChallenge(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 20<<30) // 20gb limit

	name := c.PostForm("name")
	description := c.PostForm("description")
	subDescription := c.PostForm("sub_description")
	difficulty := c.PostForm("difficulty")
	basePointsStr := c.PostForm("base_points")
	categoryName := c.PostForm("category")
	flagsJSON := c.PostForm("flags")

	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}
	if difficulty == "" {
		difficulty = "medium"
	}

	basePoints := 100
	if basePointsStr != "" {
		if bp, err := json.Number(basePointsStr).Int64(); err == nil {
			basePoints = int(bp)
		}
	}

	var categoryID *string
	if categoryName != "" {
		catID, err := h.resolveOrCreateCategory(c.Request.Context(), categoryName)
		if err != nil {
			h.logger.Error("failed to resolve category", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve category"})
			return
		}
		categoryID = catID
	}

	var flags []FlagInput
	if flagsJSON != "" {
		if err := json.Unmarshal([]byte(flagsJSON), &flags); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid flags format: " + err.Error()})
			return
		}
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		h.logger.Error("failed to get form file", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "OVA file is required: " + err.Error()})
		return
	}
	defer file.Close()

	h.logger.Info("received OVA file",
		zap.String("name", name),
		zap.String("filename", header.Filename),
		zap.Int64("size", header.Size),
	)

	challengeSlug := slug.Make(name)
	challengeID := uuid.New()

	tempDir := "/tmp/ova_uploads"
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		h.logger.Error("failed to create temp directory", zap.Error(err))
	}

	safeFilename := sanitiseFilename(header.Filename)
	tempPath := filepath.Join(tempDir, challengeID.String()+"_"+safeFilename)
	dst, err := os.Create(tempPath)
	if err != nil {
		h.logger.Error("failed to create temp file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		h.logger.Error("failed to write file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file: " + err.Error()})
		return
	}

	h.logger.Info("saved OVA file",
		zap.String("path", tempPath),
		zap.Int64("bytes", written),
	)

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create challenge"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	// container_image is set to empty string to satisfy the NOT NULL constraint
	_, err = tx.Exec(c.Request.Context(),
		`INSERT INTO challenges (
			id, name, slug, description, difficulty, category_id, status,
			base_points, resource_type, delivery_type, supports_docker, supports_vm,
			total_flags, container_image, sub_description, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, 'vm', 'vm', false, true, $8, '', $9, NOW(), NOW())`,
		challengeID, name, challengeSlug, description, difficulty, categoryID, basePoints, len(flags), subDescriptionOrNil(subDescription),
	)
	if err != nil {
		h.logger.Error("failed to create OVA challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create challenge: " + err.Error()})
		return
	}

	for i, flag := range flags {
		flagID := uuid.New()
		flagHash := hashFlag(flag.Flag)
		sortOrder := i + 1

		_, err = tx.Exec(c.Request.Context(),
			`INSERT INTO flags (id, challenge_id, name, description, flag_hash, points, sort_order, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())`,
			flagID, challengeID, flag.Name, flag.Description, flagHash, flag.Points, sortOrder,
		)
		if err != nil {
			h.logger.Error("failed to create flag", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create flag"})
			return
		}
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		h.logger.Error("failed to commit transaction", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create challenge"})
		return
	}

	h.logger.Info("OVA challenge created",
		zap.String("challenge_id", challengeID.String()),
		zap.String("name", name),
		zap.String("file", header.Filename),
		zap.Int64("size", header.Size),
		zap.Int("flags", len(flags)),
	)

	c.JSON(http.StatusCreated, gin.H{
		"id":          challengeID.String(),
		"slug":        challengeSlug,
		"total_flags": len(flags),
		"file_path":   tempPath,
		"message":     "OVA challenge created. Processing in background.",
	})
}

func (h *AdminChallengeHandler) Get(c *gin.Context) {
	challengeID := c.Param("id")

	query := `
		SELECT id, name, slug, description, difficulty, category_id, status,
		       container_image, container_tag, cpu_limit, memory_limit,
		       exposed_ports, base_points, instance_timeout, max_extensions,
		       author_name, total_solves, total_flags, created_at, scoring_mode
		FROM challenges WHERE id = $1
	`

	var ch struct {
		ID              string
		Name            string
		Slug            string
		Description     *string
		Difficulty      string
		CategoryID      *string
		Status          string
		ContainerImage  string
		ContainerTag    string
		CPULimit        string
		MemoryLimit     string
		ExposedPorts    []byte
		BasePoints      int
		InstanceTimeout *int
		MaxExtensions   *int
		AuthorName      *string
		TotalSolves     int
		TotalFlags      int
		CreatedAt       time.Time
		ScoringMode     string
	}

	err := h.db.Pool.QueryRow(c.Request.Context(), query, challengeID).Scan(
		&ch.ID, &ch.Name, &ch.Slug, &ch.Description, &ch.Difficulty, &ch.CategoryID, &ch.Status,
		&ch.ContainerImage, &ch.ContainerTag, &ch.CPULimit, &ch.MemoryLimit,
		&ch.ExposedPorts, &ch.BasePoints, &ch.InstanceTimeout, &ch.MaxExtensions,
		&ch.AuthorName, &ch.TotalSolves, &ch.TotalFlags, &ch.CreatedAt, &ch.ScoringMode,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":               ch.ID,
		"name":             ch.Name,
		"slug":             ch.Slug,
		"description":      ch.Description,
		"difficulty":       ch.Difficulty,
		"category_id":      ch.CategoryID,
		"status":           ch.Status,
		"container_image":  ch.ContainerImage,
		"container_tag":    ch.ContainerTag,
		"cpu_limit":        ch.CPULimit,
		"memory_limit":     ch.MemoryLimit,
		"base_points":      ch.BasePoints,
		"instance_timeout": ch.InstanceTimeout,
		"max_extensions":   ch.MaxExtensions,
		"author_name":      ch.AuthorName,
		"total_solves":     ch.TotalSolves,
		"total_flags":      ch.TotalFlags,
		"created_at":       ch.CreatedAt.Unix(),
		"scoring_mode":     ch.ScoringMode,
	})
}

func (h *AdminChallengeHandler) Detail(c *gin.Context) {
	challengeID := c.Param("id")
	if _, err := uuid.Parse(challengeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge id"})
		return
	}
	ctx := c.Request.Context()
	var name, slugValue string
	if err := h.db.Pool.QueryRow(ctx, `SELECT name, slug FROM challenges WHERE id = $1`, challengeID).Scan(&name, &slugValue); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge"})
		}
		return
	}

	stats := gin.H{}
	var submissionCount, correctSubmissions, wrongSubmissions, uniqueUsers, uniqueTeams int
	var instanceCount, runningInstances, failedInstances, openTeams, solvedTeams, flagCount, hintCount, attachmentCount int
	var creditsSpent float64
	if err := h.db.Pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM submissions WHERE challenge_id = $1)::int,
			(SELECT COUNT(*) FROM submissions WHERE challenge_id = $1 AND is_correct)::int,
			(SELECT COUNT(*) FROM submissions WHERE challenge_id = $1 AND NOT is_correct)::int,
			(SELECT COUNT(DISTINCT user_id) FROM submissions WHERE challenge_id = $1 AND user_id IS NOT NULL)::int,
			(SELECT COUNT(DISTINCT u.team_id) FROM submissions s JOIN users u ON u.id = s.user_id WHERE s.challenge_id = $1 AND u.team_id IS NOT NULL)::int,
			(SELECT COUNT(*) FROM instances WHERE challenge_id = $1)::int,
			(SELECT COUNT(*) FROM instances WHERE challenge_id = $1 AND status IN ('pending','creating','running','stopping'))::int,
			(SELECT COUNT(*) FROM instances WHERE challenge_id = $1 AND status = 'failed')::int,
			(SELECT COUNT(*) FROM economy_challenge_state WHERE challenge_id = $1 AND status = 'open')::int,
			(SELECT COUNT(*) FROM economy_challenge_state WHERE challenge_id = $1 AND holds_solve)::int,
			(SELECT COUNT(*) FROM flags WHERE challenge_id = $1)::int,
			(SELECT COUNT(*) FROM hints WHERE challenge_id = $1)::int,
			(SELECT COUNT(*) FROM challenge_attachments WHERE challenge_id = $1)::int,
			COALESCE((SELECT SUM(-amount) FROM economy_credit_events WHERE challenge_id = $1 AND amount < 0), 0)
	`, challengeID).Scan(&submissionCount, &correctSubmissions, &wrongSubmissions, &uniqueUsers,
		&uniqueTeams, &instanceCount, &runningInstances, &failedInstances, &openTeams,
		&solvedTeams, &flagCount, &hintCount, &attachmentCount, &creditsSpent); err != nil {
		h.logger.Error("failed to fetch challenge stats", zap.String("challenge_id", challengeID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch challenge activity"})
		return
	}
	stats = gin.H{
		"submissions": submissionCount, "correct_submissions": correctSubmissions,
		"wrong_submissions": wrongSubmissions, "unique_users": uniqueUsers,
		"unique_teams": uniqueTeams, "instances": instanceCount,
		"running_instances": runningInstances, "failed_instances": failedInstances,
		"open_teams": openTeams, "solved_teams": solvedTeams, "flags": flagCount,
		"hints": hintCount, "attachments": attachmentCount, "credits_spent": creditsSpent,
	}

	submissions := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT s.id, u.id, u.username, t.id, t.name, f.name, s.instance_id,
		       s.submitted_flag, s.is_correct, COALESCE(s.points_awarded, 0),
		       s.ip_address, LEFT(COALESCE(s.user_agent, ''), 500), s.created_at
		FROM submissions s
		LEFT JOIN users u ON u.id = s.user_id
		LEFT JOIN teams t ON t.id = u.team_id
		LEFT JOIN flags f ON f.id = s.flag_id
		WHERE s.challenge_id = $1 ORDER BY s.created_at DESC LIMIT 500`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, submittedFlag, userAgent string
			var userID, username, teamID, teamName, flagName, instanceID, ipAddress *string
			var correct bool
			var points int
			var submittedAt time.Time
			if rows.Scan(&id, &userID, &username, &teamID, &teamName, &flagName, &instanceID,
				&submittedFlag, &correct, &points, &ipAddress, &userAgent, &submittedAt) == nil {
				submissions = append(submissions, gin.H{
					"id": id, "user_id": userID, "username": username, "team_id": teamID,
					"team_name": teamName, "flag_name": flagName, "instance_id": instanceID,
					"flag_fingerprint": hashFlag(submittedFlag)[:16], "flag_length": len(submittedFlag),
					"correct": correct, "points": points, "ip_address": ipAddress,
					"user_agent": userAgent, "submitted_at": submittedAt.Unix(),
				})
			}
		}
	}

	solves := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT s.id, u.id, u.username, t.id, t.name, f.id, f.name,
		       s.points_awarded, s.solved_at
		FROM solves s
		LEFT JOIN users u ON u.id = s.user_id
		LEFT JOIN teams t ON t.id = u.team_id
		LEFT JOIN flags f ON f.id = s.flag_id
		WHERE s.challenge_id = $1 ORDER BY s.solved_at DESC LIMIT 500`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			var userID, username, teamID, teamName, flagID, flagName *string
			var points int
			var solvedAt time.Time
			if rows.Scan(&id, &userID, &username, &teamID, &teamName, &flagID, &flagName,
				&points, &solvedAt) == nil {
				solves = append(solves, gin.H{
					"id": id, "user_id": userID, "username": username, "team_id": teamID,
					"team_name": teamName, "flag_id": flagID, "flag_name": flagName,
					"points": points, "solved_at": solvedAt.Unix(),
				})
			}
		}
	}

	instances := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT i.id, u.id, u.username, COALESCE(i.team_id, u.team_id), t.name,
		       i.status::text, COALESCE(i.ip_address, ''), i.created_at, i.started_at,
		       i.expires_at, i.stopped_at, COALESCE(i.error_message, ''),
		       COALESCE(i.container_id, '') <> '', i.extensions_used, i.reset_count
		FROM instances i
		LEFT JOIN users u ON u.id = i.user_id
		LEFT JOIN teams t ON t.id = COALESCE(i.team_id, u.team_id)
		WHERE i.challenge_id = $1 ORDER BY i.created_at DESC LIMIT 500`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, status, target, errorMessage string
			var userID, username, teamID, teamName *string
			var createdAt time.Time
			var startedAt, expiresAt, stoppedAt *time.Time
			var hasRuntime bool
			var extensions, resets int
			if rows.Scan(&id, &userID, &username, &teamID, &teamName, &status, &target,
				&createdAt, &startedAt, &expiresAt, &stoppedAt, &errorMessage, &hasRuntime,
				&extensions, &resets) == nil {
				row := gin.H{"id": id, "user_id": userID, "username": username,
					"team_id": teamID, "team_name": teamName, "status": status, "target": target,
					"created_at": createdAt.Unix(), "error_message": errorMessage,
					"has_runtime": hasRuntime, "extensions_used": extensions, "reset_count": resets}
				if startedAt != nil {
					row["started_at"] = startedAt.Unix()
				}
				if expiresAt != nil {
					row["expires_at"] = expiresAt.Unix()
				}
				if stoppedAt != nil {
					row["stopped_at"] = stoppedAt.Unix()
				}
				instances = append(instances, row)
			}
		}
	}

	economy := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT e.team_id, t.name, e.status, e.opened_at, e.expires_at, e.extensions_used,
		       e.wrong_subs, e.holds_solve, e.current_value, e.frac
		FROM economy_challenge_state e JOIN teams t ON t.id = e.team_id
		WHERE e.challenge_id = $1 ORDER BY COALESCE(e.opened_at, 'epoch'::timestamptz) DESC
		LIMIT 500`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var teamID, teamName, status string
			var openedAt, expiresAt *time.Time
			var extensions, wrongSubs int
			var holds bool
			var value, fraction float64
			if rows.Scan(&teamID, &teamName, &status, &openedAt, &expiresAt, &extensions,
				&wrongSubs, &holds, &value, &fraction) == nil {
				row := gin.H{"team_id": teamID, "team_name": teamName, "status": status,
					"extensions_used": extensions, "wrong_submissions": wrongSubs,
					"holds_solve": holds, "current_value": value, "fraction": fraction}
				if openedAt != nil {
					row["opened_at"] = openedAt.Unix()
				}
				if expiresAt != nil {
					row["expires_at"] = expiresAt.Unix()
				}
				economy = append(economy, row)
			}
		}
	}

	flags := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT id, name, description, points, sort_order, flag_type, dynamic_flag_prefix,
		       total_solves, first_blood_at
		FROM flags WHERE challenge_id = $1 ORDER BY sort_order, created_at`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, flagName, flagType string
			var description, dynamicPrefix *string
			var flagPoints, sortOrder, totalSolves int
			var firstBloodAt *time.Time
			if rows.Scan(&id, &flagName, &description, &flagPoints, &sortOrder, &flagType,
				&dynamicPrefix, &totalSolves, &firstBloodAt) == nil {
				flags = append(flags, gin.H{
					"id": id, "name": flagName, "description": description, "points": flagPoints,
					"sort_order": sortOrder, "flag_type": flagType, "dynamic_flag_prefix": dynamicPrefix,
					"total_solves": totalSolves, "first_blood_at": unixOrNil(firstBloodAt),
				})
			}
		}
	}

	hints := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT id, flag_id, content, cost, sort_order, unlock_after_attempts, unlock_after_time, created_at
		FROM hints WHERE challenge_id = $1 ORDER BY sort_order, created_at`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, content string
			var flagID *uuid.UUID
			var cost, sortOrder int
			var unlockAttempts, unlockTime *int
			var hintCreated time.Time
			if rows.Scan(&id, &flagID, &content, &cost, &sortOrder, &unlockAttempts, &unlockTime, &hintCreated) == nil {
				hints = append(hints, gin.H{
					"id": id, "flag_id": flagID, "content": content, "cost": cost,
					"sort_order": sortOrder, "unlock_after_attempts": unlockAttempts,
					"unlock_after_time": unlockTime, "created_at": hintCreated.Unix(),
				})
			}
		}
	}

	attachments := []gin.H{}
	if rows, err := h.db.Pool.Query(ctx, `
		SELECT id, filename, file_size, COALESCE(content_type, ''), description,
		       sort_order, url, sha256, created_at
		FROM challenge_attachments WHERE challenge_id = $1 ORDER BY sort_order, created_at`, challengeID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, filename, contentType string
			var description, externalURL, checksum *string
			var fileSize int64
			var sortOrder int
			var attachmentCreated time.Time
			if rows.Scan(&id, &filename, &fileSize, &contentType, &description, &sortOrder,
				&externalURL, &checksum, &attachmentCreated) == nil {
				attachments = append(attachments, gin.H{
					"id": id, "filename": filename, "file_size": fileSize, "content_type": contentType,
					"description": description, "sort_order": sortOrder, "url": externalURL,
					"sha256": checksum, "created_at": attachmentCreated.Unix(),
				})
			}
		}
	}
	challengeUUID, _ := uuid.Parse(challengeID)
	audit, _ := adminEntityAudit(ctx, h.db, "challenge", challengeUUID)

	c.JSON(http.StatusOK, gin.H{
		"challenge": gin.H{"id": challengeID, "name": name, "slug": slugValue},
		"stats":     stats, "submissions": submissions, "solves": solves,
		"instances": instances, "economy": economy, "flags": flags, "hints": hints,
		"attachments": attachments, "audit": audit,
	})
}

func (h *AdminChallengeHandler) Update(c *gin.Context) {
	challengeID := c.Param("id")

	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// category_id takes precedence over category (name) when both are supplied
	if req.Category != "" && req.CategoryID == nil {
		catID, err := h.resolveOrCreateCategory(c.Request.Context(), req.Category)
		if err != nil {
			h.logger.Error("failed to resolve category", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve category"})
			return
		}
		req.CategoryID = catID
	}
	if req.ResourceType == nil || (*req.ResourceType != "docker" && *req.ResourceType != "vm") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resource_type must be docker or vm"})
		return
	}
	req.DeliveryType = strings.ToLower(strings.TrimSpace(req.DeliveryType))
	if req.DeliveryType == "" {
		if *req.ResourceType == "vm" {
			req.DeliveryType = "vm"
		} else if strings.TrimSpace(req.ContainerImage) != "" || len(req.Services) > 0 {
			req.DeliveryType = "docker"
		} else {
			req.DeliveryType = "static"
		}
	}
	if req.DeliveryType != "docker" && req.DeliveryType != "static" && req.DeliveryType != "external" && req.DeliveryType != "vm" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "delivery_type must be docker, static, external, or vm"})
		return
	}
	if (*req.ResourceType == "vm") != (req.DeliveryType == "vm") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VM resource and delivery types must be selected together"})
		return
	}
	if req.DeliveryType == "static" || req.DeliveryType == "external" {
		req.ContainerImage = ""
		req.Services = nil
		req.ExposedPorts = nil
		req.Privesc = false
	}
	if *req.ResourceType == "vm" && len(req.Services) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VM challenges cannot define container services"})
		return
	}
	if err := validateChallengeRequest(&req, false); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var containerSpec []byte
	if len(req.Services) > 0 {
		var marshalErr error
		containerSpec, marshalErr = json.Marshal(req.Services)
		if marshalErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid container services"})
			return
		}
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin challenge update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
		return
	}
	defer tx.Rollback(ctx)
	if req.ScoringMode != "" {
		var currentMode string
		var activity int
		if err := tx.QueryRow(ctx, `
			SELECT scoring_mode,
			       (SELECT COUNT(*) FROM submissions WHERE challenge_id = challenges.id) +
			       (SELECT COUNT(*) FROM solves WHERE challenge_id = challenges.id) +
			       (SELECT COUNT(*) FROM graded_evaluations WHERE challenge_id = challenges.id) +
			       (SELECT COUNT(*) FROM instances WHERE challenge_id = challenges.id AND status IN ('creating', 'running', 'stopping'))
			FROM challenges WHERE id = $1 FOR UPDATE`, challengeID).Scan(&currentMode, &activity); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to inspect challenge scoring"})
			return
		}
		if req.ScoringMode != currentMode && activity > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "scoring model is locked after submissions, evaluations, solves, or a live instance; create a new challenge to use another scoring model"})
			return
		}
	}

	var result pgconn.CommandTag
	if req.ResourceType != nil && *req.ResourceType == "vm" {
		result, err = tx.Exec(ctx,
			`UPDATE challenges SET
				name = $1, description = $2, difficulty = $3, category_id = $4,
				base_points = $5, instance_timeout = $6, max_extensions = $7,
				vm_timeout_minutes = $8, vm_max_extensions = $9, vm_extension_minutes = $10,
				cooldown_minutes = $11, author_name = $12, resource_type = $13, delivery_type = 'vm',
				supports_vm = true, supports_docker = false, sub_description = $14,
				container_spec = NULL, privesc = false,
				arena_mode = COALESCE(NULLIF($15, ''), arena_mode), updated_at = NOW()
			WHERE id = $16`,
			req.Name, req.Description, req.Difficulty, req.CategoryID,
			req.BasePoints, req.InstanceTimeout, req.MaxExtensions,
			req.VMTimeoutMinutes, req.VMMaxExtensions, req.VMExtensionMinutes,
			req.CooldownMinutes, req.AuthorName, req.ResourceType,
			subDescriptionOrNil(req.SubDescription), req.ArenaMode, challengeID,
		)
		if err != nil {
			h.logger.Error("failed to update VM challenge", zap.Error(err))
			if postgresErrorCode(err) == "23503" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "category does not exist"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
			return
		}
		if result.RowsAffected() == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
			return
		}

		if req.VMTemplateID != nil {
			if _, err = tx.Exec(ctx,
				`UPDATE challenge_resources SET is_active = false, updated_at = NOW()
				 WHERE challenge_id = $1 AND resource_type = 'vm' AND is_active = true`, challengeID); err != nil {
				h.logger.Error("failed to deactivate old VM resource", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
				return
			}

			_, err = tx.Exec(ctx,
				`INSERT INTO challenge_resources (challenge_id, resource_type, vm_template_id, created_at, updated_at)
				 VALUES ($1, 'vm', $2, NOW(), NOW())`,
				challengeID, req.VMTemplateID,
			)
			if err != nil {
				h.logger.Error("failed to associate VM template", zap.Error(err))
				if postgresErrorCode(err) == "23503" {
					c.JSON(http.StatusBadRequest, gin.H{"error": "VM template does not exist"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
				return
			}
		}
	} else {
		req.MemoryLimit = normalizeMemoryLimit(req.MemoryLimit)
		portsJSON, marshalErr := json.Marshal(req.ExposedPorts)
		if marshalErr != nil {
			h.logger.Error("failed to marshal exposed ports", zap.Error(marshalErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to process exposed ports"})
			return
		}
		result, err = tx.Exec(ctx,
			`UPDATE challenges SET
				name = $1, description = $2, difficulty = $3, category_id = $4,
				container_image = $5, container_tag = $6, container_platform = $7,
				cpu_limit = $8, memory_limit = $9, exposed_ports = $10,
				base_points = $11, instance_timeout = $12, max_extensions = $13,
				cooldown_minutes = $14, author_name = $15, resource_type = 'docker', delivery_type = $20,
				supports_vm = false, supports_docker = true, sub_description = $16,
				container_spec = $17, privesc = $18,
				arena_mode = COALESCE(NULLIF($19, ''), arena_mode), updated_at = NOW()
			WHERE id = $21`,
			req.Name, req.Description, req.Difficulty, req.CategoryID,
			req.ContainerImage, req.ContainerTag, req.ContainerPlatform,
			req.CPULimit, req.MemoryLimit, portsJSON,
			req.BasePoints, req.InstanceTimeout, req.MaxExtensions,
			req.CooldownMinutes, req.AuthorName, subDescriptionOrNil(req.SubDescription),
			nilIfEmpty(containerSpec), req.Privesc, req.ArenaMode, req.DeliveryType, challengeID,
		)
		if err == nil && result.RowsAffected() == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
			return
		}
		if err == nil {
			_, err = tx.Exec(ctx,
				`UPDATE challenge_resources SET is_active = false, updated_at = NOW()
				 WHERE challenge_id = $1 AND resource_type = 'vm' AND is_active = true`, challengeID)
		}
	}
	if err != nil {
		h.logger.Error("failed to update challenge", zap.Error(err))
		if postgresErrorCode(err) == "23503" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "category does not exist"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
		return
	}
	if req.ScoringMode != "" {
		if req.ScoringMode != "flag" && req.ScoringMode != "graded" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scoring_mode must be flag or graded"})
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE challenges SET scoring_mode = $1 WHERE id = $2`, req.ScoringMode, challengeID); err != nil {
			h.logger.Error("failed to update scoring_mode", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
			return
		}
	}
	if req.ArenaMode != "" {
		if req.ArenaMode != "shared" && req.ArenaMode != "per_team" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "arena_mode must be per_team or shared"})
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE challenges SET arena_mode = $1 WHERE id = $2`, req.ArenaMode, challengeID); err != nil {
			h.logger.Error("failed to update arena_mode", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit challenge update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge updated"})
}

func (h *AdminChallengeHandler) Delete(c *gin.Context) {
	challengeID := c.Param("id")
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
		return
	}
	defer tx.Rollback(ctx)

	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM challenges WHERE id = $1 FOR UPDATE`, challengeID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		} else {
			h.logger.Error("failed to lock challenge for deletion", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
		}
		return
	}

	var activeInstances int
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM instances WHERE challenge_id = $1 AND status IN ('pending','creating','running','stopping')) +
			(SELECT COUNT(*) FROM vm_instances WHERE challenge_id = $1 AND status IN ('provisioning','starting','running','paused','stopping'))
	`, challengeID).Scan(&activeInstances); err != nil {
		h.logger.Error("failed to check active challenge instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
		return
	}
	if activeInstances > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "stop active challenge instances before deleting the challenge"})
		return
	}

	cleanupStatements := []string{
		`DELETE FROM solved_flags WHERE challenge_id = $1`,
		`DELETE FROM submissions WHERE challenge_id = $1`,
		`DELETE FROM vm_instances WHERE challenge_id = $1`,
		`DELETE FROM instances WHERE challenge_id = $1`,
		`DELETE FROM docker_builds WHERE challenge_id = $1`,
		`UPDATE uploads SET challenge_id = NULL WHERE challenge_id = $1`,
	}
	for _, statement := range cleanupStatements {
		if _, err := tx.Exec(ctx, statement, challengeID); err != nil {
			h.logger.Error("failed to remove challenge data", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
			return
		}
	}

	result, err := tx.Exec(ctx,
		`DELETE FROM challenges WHERE id = $1`, challengeID)
	if err != nil {
		h.logger.Error("failed to delete challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
		return
	}
	if result.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit challenge deletion", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge deleted"})
}

func (h *AdminChallengeHandler) Publish(c *gin.Context) {
	challengeID := c.Param("id")
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish challenge"})
		return
	}
	defer tx.Rollback(ctx)

	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM challenges WHERE id = $1 FOR UPDATE`, challengeID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish challenge"})
		}
		return
	}

	// graded challenges score through their grader and need no flag.
	var flagCount int
	var graded bool
	if err := tx.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM flags WHERE challenge_id = $1),
		        (SELECT scoring_mode = 'graded' FROM challenges WHERE id = $1)`,
		challengeID).Scan(&flagCount, &graded); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish challenge"})
		return
	}

	if flagCount == 0 && !graded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "challenge must have at least one flag"})
		return
	}

	result, err := tx.Exec(ctx,
		`UPDATE challenges SET status = 'published', release_date = NOW(), updated_at = NOW() WHERE id = $1`,
		challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish challenge"})
		return
	}
	if result.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to publish challenge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge published"})
}

func (h *AdminChallengeHandler) Unpublish(c *gin.Context) {
	challengeID := c.Param("id")

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET status = 'draft', updated_at = NOW() WHERE id = $1`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unpublish challenge"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge unpublished"})
}

func (h *AdminChallengeHandler) Archive(c *gin.Context) {
	challengeID := c.Param("id")

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE challenges SET status = 'archived', updated_at = NOW() WHERE id = $1`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to archive challenge"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "challenge archived"})
}

type CreateFlagRequest struct {
	Name              string `json:"name" binding:"required"`
	Flag              string `json:"flag"` // empty when FlagType == "dynamic"
	Points            int    `json:"points"`
	Order             int    `json:"order"`
	CaseSensitive     *bool  `json:"case_sensitive"`
	FlagType          string `json:"flag_type"`           // "static" | "dynamic"
	DynamicFlagPrefix string `json:"dynamic_flag_prefix"` // e.g. "H7CTF"
}

func (h *AdminChallengeHandler) ListFlags(c *gin.Context) {
	challengeID := c.Param("id")

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, flag_hash, points, sort_order, case_sensitive,
		        flag_type, COALESCE(dynamic_flag_prefix, '')
		 FROM flags WHERE challenge_id = $1 ORDER BY sort_order`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
		return
	}
	defer rows.Close()

	var flags []gin.H
	for rows.Next() {
		var id, name, flag, flagType, dynPrefix string
		var points, order int
		var caseSensitive bool
		if err := rows.Scan(&id, &name, &flag, &points, &order, &caseSensitive, &flagType, &dynPrefix); err != nil {
			h.logger.Error("failed to scan flag", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
			return
		}
		flags = append(flags, gin.H{
			"id":                  id,
			"name":                name,
			"has_value":           flag != "",
			"points":              points,
			"order":               order,
			"case_sensitive":      caseSensitive,
			"flag_type":           flagType,
			"dynamic_flag_prefix": dynPrefix,
		})
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flags"})
		return
	}

	if flags == nil {
		flags = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"flags": flags})
}

func (h *AdminChallengeHandler) CreateFlag(c *gin.Context) {
	challengeID := c.Param("id")

	var req CreateFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Points == 0 {
		req.Points = 100
	}
	if req.Points < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "points must be positive"})
		return
	}

	flagType, flagHash, err := prepareNewFlag(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	caseSensitive := requestedCaseSensitivity(req.CaseSensitive, true)
	var dynPrefix *string
	if strings.TrimSpace(req.DynamicFlagPrefix) != "" {
		s := strings.TrimSpace(req.DynamicFlagPrefix)
		dynPrefix = &s
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin flag creation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
		return
	}
	defer tx.Rollback(ctx)

	var lockedChallengeID string
	if err := tx.QueryRow(ctx, `SELECT id FROM challenges WHERE id = $1 FOR UPDATE`, challengeID).Scan(&lockedChallengeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		} else {
			h.logger.Error("failed to lock challenge for flag creation", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
		}
		return
	}
	order := req.Order
	if order <= 0 {
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(sort_order), 0) + 1 FROM flags WHERE challenge_id = $1`,
			challengeID).Scan(&order); err != nil {
			h.logger.Error("failed to choose flag order", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
			return
		}
	}

	flagID := uuid.New()
	_, err = tx.Exec(ctx,
		`INSERT INTO flags (id, challenge_id, name, flag_hash, points, sort_order, case_sensitive,
		                    flag_type, dynamic_flag_prefix, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())`,
		flagID, challengeID, req.Name, flagHash, req.Points, order, caseSensitive,
		flagType, dynPrefix)
	if err != nil {
		h.logger.Error("failed to create flag", zap.Error(err))
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "another flag already uses this order"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
		return
	}

	if err := recalculateChallengeFlagTotals(ctx, tx, challengeID); err != nil {
		h.logger.Error("failed to update challenge flag count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit flag creation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create flag"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": flagID.String(), "message": "flag created"})
}

func (h *AdminChallengeHandler) UpdateFlag(c *gin.Context) {
	challengeID := c.Param("id")
	flagID := c.Param("flag_id")

	var req CreateFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update flag"})
		return
	}
	defer tx.Rollback(ctx)

	var currentType, currentHash string
	var currentCaseSensitive bool
	var currentOrder int
	if err := tx.QueryRow(ctx,
		`SELECT flag_type, flag_hash, case_sensitive, sort_order FROM flags WHERE id = $1 AND challenge_id = $2 FOR UPDATE`,
		flagID, challengeID).Scan(&currentType, &currentHash, &currentCaseSensitive, &currentOrder); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "flag not found"})
		} else {
			h.logger.Error("failed to load flag for update", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update flag"})
		}
		return
	}

	flagType := currentType
	if req.FlagType != "" {
		flagType = strings.ToLower(strings.TrimSpace(req.FlagType))
	}
	if !validFlagType(flagType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag_type must be static, regex, or dynamic"})
		return
	}
	if req.Points < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "points must be positive"})
		return
	}
	order := req.Order
	if order <= 0 {
		order = currentOrder
	}
	caseSensitive := requestedCaseSensitivity(req.CaseSensitive, currentCaseSensitive)
	flagHash := currentHash
	if req.Flag != "" {
		flagHash, err = encodeFlagValue(flagType, req.Flag, caseSensitive)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else if flagType != currentType {
		if flagType != "dynamic" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required when changing to static or regex"})
			return
		}
		flagHash = ""
	} else if caseSensitive != currentCaseSensitive && flagType != "dynamic" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "flag is required when changing case sensitivity"})
		return
	}

	result, err := tx.Exec(ctx,
		`UPDATE flags SET name = $1, flag_hash = $2, points = $3, sort_order = $4,
		 case_sensitive = $5, flag_type = $6, dynamic_flag_prefix = NULLIF($7, ''), updated_at = NOW()
		 WHERE id = $8 AND challenge_id = $9`,
		req.Name, flagHash, req.Points, order, caseSensitive, flagType,
		strings.TrimSpace(req.DynamicFlagPrefix), flagID, challengeID)
	if err != nil {
		h.logger.Error("failed to update flag", zap.Error(err))
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "another flag already uses this order"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update flag"})
		return
	}
	if result.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "flag not found"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit flag update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update flag"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "flag updated"})
}

func (h *AdminChallengeHandler) DeleteFlag(c *gin.Context) {
	challengeID := c.Param("id")
	flagID := c.Param("flag_id")

	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete flag"})
		return
	}
	defer tx.Rollback(ctx)

	var lockedChallengeID string
	if err := tx.QueryRow(ctx, `SELECT id FROM challenges WHERE id = $1 FOR UPDATE`, challengeID).Scan(&lockedChallengeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete flag"})
		}
		return
	}

	result, err := tx.Exec(ctx,
		`DELETE FROM flags WHERE id = $1 AND challenge_id = $2`, flagID, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete flag"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "flag not found"})
		return
	}

	if err := recalculateChallengeFlagTotals(ctx, tx, challengeID); err != nil {
		h.logger.Error("failed to update challenge flag count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete flag"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit flag deletion", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete flag"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "flag deleted"})
}

func recalculateChallengeFlagTotals(ctx context.Context, tx pgx.Tx, challengeID string) error {
	result, err := tx.Exec(ctx, `
		UPDATE challenges
		SET total_flags = (SELECT COUNT(*) FROM flags WHERE challenge_id = $1),
			total_solves = (
				SELECT COUNT(*) FROM (
					SELECT s.user_id
					FROM solves s
					JOIN flags f ON f.id = s.flag_id
					WHERE f.challenge_id = $1
					GROUP BY s.user_id
					HAVING COUNT(DISTINCT s.flag_id) = (
						SELECT COUNT(*) FROM flags WHERE challenge_id = $1
					)
				) completed
			),
			updated_at = NOW()
		WHERE id = $1
	`, challengeID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("challenge total update affected %d rows", result.RowsAffected())
	}
	return nil
}

func prepareNewFlag(req CreateFlagRequest) (string, string, error) {
	flagType := strings.ToLower(strings.TrimSpace(req.FlagType))
	if flagType == "" {
		flagType = "static"
	}
	if !validFlagType(flagType) {
		return "", "", errors.New("flag_type must be static, regex, or dynamic")
	}
	if flagType == "dynamic" {
		return flagType, "", nil
	}
	if strings.TrimSpace(req.Flag) == "" {
		return "", "", errors.New("flag is required for static and regex flags")
	}
	flagHash, err := encodeFlagValue(flagType, req.Flag, requestedCaseSensitivity(req.CaseSensitive, true))
	return flagType, flagHash, err
}

func validFlagType(flagType string) bool {
	return flagType == "static" || flagType == "regex" || flagType == "dynamic"
}

func requestedCaseSensitivity(requested *bool, fallback bool) bool {
	if requested == nil {
		return fallback
	}
	return *requested
}

func encodeFlagValue(flagType, value string, caseSensitive bool) (string, error) {
	if flagType == "regex" {
		pattern := value
		if !caseSensitive {
			pattern = "(?i:" + value + ")"
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return "", fmt.Errorf("invalid regex pattern: %w", err)
		}
		return value, nil
	}
	if flagType != "static" {
		return "", errors.New("dynamic flags do not store a fixed value")
	}
	if !caseSensitive {
		value = strings.ToLower(value)
	}
	return hashFlag(value), nil
}

func (h *AdminChallengeHandler) ListHints(c *gin.Context) {
	challengeID := c.Param("id")

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, challenge_id, cost, content, created_at
		 FROM hints WHERE challenge_id = $1 ORDER BY sort_order, created_at`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
		return
	}
	defer rows.Close()

	hints := make([]map[string]interface{}, 0)
	for rows.Next() {
		var hint struct {
			ID          string
			ChallengeID string
			Cost        int
			Content     string
			CreatedAt   time.Time
		}
		if err := rows.Scan(&hint.ID, &hint.ChallengeID, &hint.Cost, &hint.Content, &hint.CreatedAt); err != nil {
			h.logger.Error("failed to scan hint", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
			return
		}
		hints = append(hints, map[string]interface{}{
			"id":           hint.ID,
			"challenge_id": hint.ChallengeID,
			"cost":         hint.Cost,
			"content":      hint.Content,
			"created_at":   hint.CreatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing hints", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch hints"})
		return
	}

	c.JSON(http.StatusOK, hints)
}

func (h *AdminChallengeHandler) CreateHint(c *gin.Context) {
	challengeID := c.Param("id")

	var req struct {
		Cost    *int   `json:"cost" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if *req.Cost < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cost must be non-negative"})
		return
	}

	var hintID string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO hints (challenge_id, cost, content)
		 SELECT id, $2, $3 FROM challenges WHERE id = $1 RETURNING id`,
		challengeID, *req.Cost, req.Content).Scan(&hintID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
			return
		}
		h.logger.Error("failed to create hint", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create hint"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": hintID, "message": "hint created"})
}

func (h *AdminChallengeHandler) UpdateHint(c *gin.Context) {
	challengeID := c.Param("id")
	hintID := c.Param("hint_id")

	var req struct {
		Cost    int    `json:"cost"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Cost < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cost must be non-negative"})
		return
	}

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE hints SET cost = $1, content = $2 WHERE id = $3 AND challenge_id = $4`,
		req.Cost, req.Content, hintID, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update hint"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "hint not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "hint updated"})
}

func (h *AdminChallengeHandler) DeleteHint(c *gin.Context) {
	challengeID := c.Param("id")
	hintID := c.Param("hint_id")

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM hints WHERE id = $1 AND challenge_id = $2`, hintID, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete hint"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "hint not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "hint deleted"})
}

func (h *AdminChallengeHandler) ListFlagShareEvents(c *gin.Context) {
	challengeIDFilter := c.Query("challenge_id")
	submitterFilter := c.Query("submitter_id")
	limit := 500

	var cid, sid interface{} = nil, nil
	if challengeIDFilter != "" {
		cid = challengeIDFilter
	}
	if submitterFilter != "" {
		sid = submitterFilter
	}

	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT
			fse.id,
			fse.created_at,
			fse.challenge_id,
			ch.name        AS challenge_name,
			fse.flag_id,
			f.name         AS flag_name,
			fse.flag_value,
			fse.owner_user_id,
			owner.username AS owner_username,
			fse.submitter_user_id,
			sub.username   AS submitter_username,
			fse.submitter_ip,
			fse.owner_instance_id
		FROM flag_share_events fse
		JOIN challenges ch  ON ch.id  = fse.challenge_id
		JOIN flags      f   ON f.id   = fse.flag_id
		JOIN users      owner ON owner.id = fse.owner_user_id
		JOIN users      sub   ON sub.id   = fse.submitter_user_id
		WHERE ($1::uuid IS NULL OR fse.challenge_id      = $1)
		  AND ($2::uuid IS NULL OR fse.submitter_user_id = $2)
		ORDER BY fse.created_at DESC
		LIMIT $3`,
		cid, sid, limit)
	if err != nil {
		h.logger.Error("failed to list flag share events", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch flag share events"})
		return
	}
	defer rows.Close()

	type shareRow struct {
		ID                string  `json:"id"`
		CreatedAt         int64   `json:"created_at"`
		ChallengeID       string  `json:"challenge_id"`
		ChallengeName     string  `json:"challenge_name"`
		FlagID            string  `json:"flag_id"`
		FlagName          string  `json:"flag_name"`
		FlagFingerprint   string  `json:"flag_fingerprint"`
		FlagLength        int     `json:"flag_length"`
		OwnerUserID       string  `json:"owner_user_id"`
		OwnerUsername     string  `json:"owner_username"`
		SubmitterUserID   string  `json:"submitter_user_id"`
		SubmitterUsername string  `json:"submitter_username"`
		SubmitterIP       *string `json:"submitter_ip"`
		OwnerInstanceID   *string `json:"owner_instance_id"`
	}

	var results []shareRow
	for rows.Next() {
		var r shareRow
		var flagValue string
		var createdAt time.Time
		if err := rows.Scan(
			&r.ID, &createdAt, &r.ChallengeID, &r.ChallengeName,
			&r.FlagID, &r.FlagName, &flagValue,
			&r.OwnerUserID, &r.OwnerUsername,
			&r.SubmitterUserID, &r.SubmitterUsername,
			&r.SubmitterIP, &r.OwnerInstanceID,
		); err != nil {
			continue
		}
		r.FlagFingerprint = hashFlag(flagValue)[:16]
		r.FlagLength = len(flagValue)
		r.CreatedAt = createdAt.Unix()
		results = append(results, r)
	}
	if results == nil {
		results = []shareRow{}
	}

	c.JSON(http.StatusOK, gin.H{"flag_shares": results, "total": len(results)})
}

// all generated dynamic flags; admin monitoring only
func (h *AdminChallengeHandler) ListInstanceFlags(c *gin.Context) {
	challengeIDFilter := c.Query("challenge_id")
	userIDFilter := c.Query("user_id")
	limit := 500

	query := `
		SELECT
			inf.id,
			inf.instance_id,
			inf.user_id,
			u.username,
			inf.challenge_id,
			ch.name  AS challenge_name,
			inf.flag_id,
			f.name   AS flag_name,
			f.flag_type,
			inf.flag_value,
			inf.created_at,
			i.status AS instance_status
		FROM instance_flags inf
		JOIN users      u  ON u.id  = inf.user_id
		JOIN challenges ch ON ch.id = inf.challenge_id
		JOIN flags      f  ON f.id  = inf.flag_id
		JOIN instances  i  ON i.id  = inf.instance_id
		WHERE ($1::uuid IS NULL OR inf.challenge_id = $1)
		  AND ($2::uuid IS NULL OR inf.user_id      = $2)
		ORDER BY inf.created_at DESC
		LIMIT $3
	`

	var cid, uid interface{} = nil, nil
	if challengeIDFilter != "" {
		cid = challengeIDFilter
	}
	if userIDFilter != "" {
		uid = userIDFilter
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), query, cid, uid, limit)
	if err != nil {
		h.logger.Error("failed to list instance flags", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instance flags"})
		return
	}
	defer rows.Close()

	type row struct {
		ID              string `json:"id"`
		InstanceID      string `json:"instance_id"`
		UserID          string `json:"user_id"`
		Username        string `json:"username"`
		ChallengeID     string `json:"challenge_id"`
		ChallengeName   string `json:"challenge_name"`
		FlagID          string `json:"flag_id"`
		FlagName        string `json:"flag_name"`
		FlagType        string `json:"flag_type"`
		FlagFingerprint string `json:"flag_fingerprint"`
		FlagLength      int    `json:"flag_length"`
		CreatedAt       int64  `json:"created_at"`
		InstanceStatus  string `json:"instance_status"`
	}

	var results []row
	for rows.Next() {
		var r row
		var flagValue string
		var createdAt time.Time
		if err := rows.Scan(
			&r.ID, &r.InstanceID, &r.UserID, &r.Username,
			&r.ChallengeID, &r.ChallengeName,
			&r.FlagID, &r.FlagName, &r.FlagType,
			&flagValue, &createdAt, &r.InstanceStatus,
		); err != nil {
			continue
		}
		r.FlagFingerprint = hashFlag(flagValue)[:16]
		r.FlagLength = len(flagValue)
		r.CreatedAt = createdAt.Unix()
		results = append(results, r)
	}
	if results == nil {
		results = []row{}
	}

	c.JSON(http.StatusOK, gin.H{"instance_flags": results, "total": len(results)})
}

func (h *StatsHandler) Get(c *gin.Context) {
	query := `
		SELECT
			(SELECT COUNT(*) FROM users WHERE role NOT IN ('admin', 'author')) as total_users,
			(SELECT COUNT(*) FROM teams) as total_teams,
			(SELECT COUNT(*) FROM challenges) as total_challenges,
			(SELECT COUNT(*) FROM challenges WHERE status = 'published') as published_challenges,
			(SELECT COUNT(*) FROM challenges WHERE status = 'draft') as draft_challenges,
			(SELECT COUNT(*) FROM solves s WHERE NOT EXISTS (
				SELECT 1 FROM users u WHERE u.id = s.user_id AND u.role IN ('admin', 'author'))) as total_solves,
			(SELECT COUNT(*) FROM submissions) as total_submissions,
			(SELECT COUNT(*) FROM instances) as total_instances,
			(SELECT COUNT(*) FROM instances WHERE status = 'running') as active_instances
	`

	var stats struct {
		TotalUsers          int
		TotalTeams          int
		TotalChallenges     int
		PublishedChallenges int
		DraftChallenges     int
		TotalSolves         int
		TotalSubmissions    int
		TotalInstances      int
		ActiveInstances     int
	}

	err := h.db.Pool.QueryRow(c.Request.Context(), query).Scan(
		&stats.TotalUsers,
		&stats.TotalTeams,
		&stats.TotalChallenges,
		&stats.PublishedChallenges,
		&stats.DraftChallenges,
		&stats.TotalSolves,
		&stats.TotalSubmissions,
		&stats.TotalInstances,
		&stats.ActiveInstances,
	)

	if err != nil {
		h.logger.Error("failed to fetch stats", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_users":          stats.TotalUsers,
		"total_teams":          stats.TotalTeams,
		"total_challenges":     stats.TotalChallenges,
		"published_challenges": stats.PublishedChallenges,
		"draft_challenges":     stats.DraftChallenges,
		"total_solves":         stats.TotalSolves,
		"total_submissions":    stats.TotalSubmissions,
		"total_instances":      stats.TotalInstances,
		"active_instances":     stats.ActiveInstances,
	})
}
