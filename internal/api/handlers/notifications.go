package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	defaultNotificationLimit = 30
	maxNotificationLimit     = 100
	maxAnnouncementBody      = 5000
)

type NotificationHandler struct {
	db     *database.DB
	logger *zap.Logger
}

func NewNotificationHandler(db *database.DB, logger *zap.Logger) *NotificationHandler {
	return &NotificationHandler{db: db, logger: logger}
}

type notificationItem struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	EventType string          `json:"event_type"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Severity  string          `json:"severity"`
	Audience  string          `json:"audience"`
	Href      string          `json:"href,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	PublishAt time.Time       `json:"publish_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Pinned    bool            `json:"pinned"`
	Read      bool            `json:"read"`
	CreatedAt time.Time       `json:"created_at"`
}

type notificationListResponse struct {
	Items       []notificationItem `json:"items"`
	UnreadCount int                `json:"unread_count"`
}

const notificationVisibilitySQL = `(
	ni.audience = 'all'
	OR (ni.audience = 'participants' AND $2 = 'user')
	OR (ni.audience = 'staff' AND $2 IN ('admin', 'author'))
	OR (ni.audience = 'team' AND ni.team_id = $3)
	OR (ni.audience = 'user' AND ni.user_id = $1)
)`

const activeNotificationSQL = `
	ni.cancelled_at IS NULL
	AND ni.publish_at <= NOW()
	AND (ni.expires_at IS NULL OR ni.expires_at > NOW())`

func notificationContext(c *gin.Context, db *database.DB) (uuid.UUID, string, *uuid.UUID, bool) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.Nil, "", nil, false
	}
	role, _ := c.Get("role")
	roleName, _ := role.(string)
	teamID, err := resolveTeamID(c.Request.Context(), db, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notifications unavailable"})
		return uuid.Nil, "", nil, false
	}
	return uid, roleName, teamID, true
}

func notificationLimit(c *gin.Context) (int, bool) {
	limit := defaultNotificationLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxNotificationLimit {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func (h *NotificationHandler) List(c *gin.Context) {
	uid, role, teamID, ok := notificationContext(c, h.db)
	if !ok {
		return
	}
	limit, ok := notificationLimit(c)
	if !ok {
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT ni.id, ni.kind, ni.event_type, ni.title, ni.body, ni.severity,
		       ni.audience, COALESCE(ni.href, ''), ni.payload, ni.publish_at,
		       ni.expires_at, ni.pinned, nr.read_at IS NOT NULL, ni.created_at
		FROM notification_items ni
		LEFT JOIN notification_receipts nr ON nr.item_id = ni.id AND nr.user_id = $1
		WHERE `+activeNotificationSQL+` AND `+notificationVisibilitySQL+`
		ORDER BY ni.pinned DESC, ni.publish_at DESC, ni.id DESC
		LIMIT $4`, uid, role, teamID, limit)
	if err != nil {
		h.logger.Error("list notifications", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notifications unavailable"})
		return
	}
	defer rows.Close()

	items := []notificationItem{}
	for rows.Next() {
		var item notificationItem
		if err := rows.Scan(&item.ID, &item.Kind, &item.EventType, &item.Title, &item.Body,
			&item.Severity, &item.Audience, &item.Href, &item.Payload, &item.PublishAt,
			&item.ExpiresAt, &item.Pinned, &item.Read, &item.CreatedAt); err != nil {
			h.logger.Error("scan notification", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "notifications unavailable"})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("iterate notifications", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notifications unavailable"})
		return
	}

	unread, err := h.unreadCount(c.Request.Context(), uid, role, teamID)
	if err != nil {
		h.logger.Error("count notifications", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notifications unavailable"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, notificationListResponse{Items: items, UnreadCount: unread})
}

func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	uid, role, teamID, ok := notificationContext(c, h.db)
	if !ok {
		return
	}
	count, err := h.unreadCount(c.Request.Context(), uid, role, teamID)
	if err != nil {
		h.logger.Error("count notifications", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notifications unavailable"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

func (h *NotificationHandler) unreadCount(ctx context.Context, uid uuid.UUID, role string, teamID *uuid.UUID) (int, error) {
	var count int
	err := h.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM notification_items ni
		LEFT JOIN notification_receipts nr ON nr.item_id = ni.id AND nr.user_id = $1
		WHERE `+activeNotificationSQL+` AND `+notificationVisibilitySQL+`
		  AND nr.read_at IS NULL`, uid, role, teamID).Scan(&count)
	return count, err
}

func (h *NotificationHandler) MarkRead(c *gin.Context) {
	uid, role, teamID, ok := notificationContext(c, h.db)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notification id"})
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO notification_receipts (item_id, user_id, read_at)
		SELECT ni.id, $1, NOW()
		FROM notification_items ni
		WHERE ni.id = $4 AND `+activeNotificationSQL+` AND `+notificationVisibilitySQL+`
		ON CONFLICT (item_id, user_id) DO UPDATE
		SET read_at = COALESCE(notification_receipts.read_at, EXCLUDED.read_at), updated_at = NOW()`,
		uid, role, teamID, itemID)
	if err != nil {
		h.logger.Error("mark notification read", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update notification"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	uid, role, teamID, ok := notificationContext(c, h.db)
	if !ok {
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO notification_receipts (item_id, user_id, read_at)
		SELECT ni.id, $1, NOW()
		FROM notification_items ni
		WHERE `+activeNotificationSQL+` AND `+notificationVisibilitySQL+`
		ON CONFLICT (item_id, user_id) DO UPDATE
		SET read_at = COALESCE(notification_receipts.read_at, EXCLUDED.read_at), updated_at = NOW()`,
		uid, role, teamID)
	if err != nil {
		h.logger.Error("mark all notifications read", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update notifications"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "updated": result.RowsAffected()})
}

type createAnnouncementRequest struct {
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Severity  string     `json:"severity"`
	Audience  string     `json:"audience"`
	Href      string     `json:"href"`
	PublishAt *time.Time `json:"publish_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Pinned    bool       `json:"pinned"`
}

func validateAnnouncement(req *createAnnouncementRequest, now time.Time) error {
	req.Title = strings.TrimSpace(req.Title)
	req.Body = strings.TrimSpace(req.Body)
	req.Severity = strings.ToLower(strings.TrimSpace(req.Severity))
	req.Audience = strings.ToLower(strings.TrimSpace(req.Audience))
	req.Href = strings.TrimSpace(req.Href)
	if req.Title == "" || len([]rune(req.Title)) > 160 {
		return errors.New("title must be between 1 and 160 characters")
	}
	if req.Body == "" || len([]rune(req.Body)) > maxAnnouncementBody {
		return errors.New("body must be between 1 and 5000 characters")
	}
	if req.Severity == "" {
		req.Severity = "info"
	}
	if req.Severity != "info" && req.Severity != "success" && req.Severity != "warning" && req.Severity != "critical" {
		return errors.New("severity must be info, success, warning, or critical")
	}
	if req.Audience == "" {
		req.Audience = "all"
	}
	if req.Audience != "all" && req.Audience != "participants" && req.Audience != "staff" {
		return errors.New("announcement audience must be all, participants, or staff")
	}
	if req.Href != "" {
		parsed, err := url.Parse(req.Href)
		if err != nil || (parsed.IsAbs() && (parsed.Scheme != "https" || parsed.Host == "")) || (!parsed.IsAbs() && (!strings.HasPrefix(req.Href, "/") || strings.HasPrefix(req.Href, "//"))) {
			return errors.New("href must be a local path or an https URL")
		}
	}
	publishAt := now
	if req.PublishAt != nil {
		publishAt = req.PublishAt.UTC()
		req.PublishAt = &publishAt
	}
	if req.ExpiresAt != nil {
		expiresAt := req.ExpiresAt.UTC()
		req.ExpiresAt = &expiresAt
		if !expiresAt.After(publishAt) {
			return errors.New("expires_at must be after publish_at")
		}
	}
	return nil
}

func (h *NotificationHandler) CreateAnnouncement(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req createAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid announcement"})
		return
	}
	now := time.Now().UTC()
	if err := validateAnnouncement(&req, now); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	publishAt := now
	if req.PublishAt != nil {
		publishAt = *req.PublishAt
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create announcement"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var id uuid.UUID
	err = tx.QueryRow(c.Request.Context(), `
		INSERT INTO notification_items
		(kind, event_type, title, body, severity, audience, href, publish_at, expires_at, pinned, created_by)
		VALUES ('announcement', 'organizer.announcement', $1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)
		RETURNING id`, req.Title, req.Body, req.Severity, req.Audience, req.Href,
		publishAt, req.ExpiresAt, req.Pinned, uid).Scan(&id)
	if err != nil {
		h.logger.Error("create announcement", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create announcement"})
		return
	}
	metadata, _ := json.Marshal(gin.H{"title": req.Title, "audience": req.Audience, "severity": req.Severity, "publish_at": publishAt})
	if _, err = tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent)
		VALUES ($1, 'announcement_created', 'notification_item', $2, $3::jsonb, $4, $5)`,
		uid, id, metadata, c.ClientIP(), c.Request.UserAgent()); err != nil {
		h.logger.Error("audit announcement creation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create announcement"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create announcement"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "publish_at": publishAt})
}

func (h *NotificationHandler) ListAnnouncements(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, title, body, severity, audience, COALESCE(href, ''), publish_at,
		       expires_at, pinned, cancelled_at, created_at
		FROM notification_items
		WHERE kind = 'announcement'
		ORDER BY created_at DESC
		LIMIT 100`)
	if err != nil {
		h.logger.Error("list announcements", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load announcements"})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var title, body, severity, audience, href string
		var publishAt, createdAt time.Time
		var expiresAt, cancelledAt *time.Time
		var pinned bool
		if err := rows.Scan(&id, &title, &body, &severity, &audience, &href, &publishAt,
			&expiresAt, &pinned, &cancelledAt, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load announcements"})
			return
		}
		items = append(items, gin.H{
			"id": id, "title": title, "body": body, "severity": severity, "audience": audience,
			"href": href, "publish_at": publishAt, "expires_at": expiresAt, "pinned": pinned,
			"cancelled_at": cancelledAt, "created_at": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load announcements"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"announcements": items})
}

func (h *NotificationHandler) CancelAnnouncement(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid announcement id"})
		return
	}
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel announcement"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	result, err := tx.Exec(c.Request.Context(), `
		UPDATE notification_items SET cancelled_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND kind = 'announcement' AND cancelled_at IS NULL`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel announcement"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "announcement not found"})
		return
	}
	if _, err = tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent)
		VALUES ($1, 'announcement_cancelled', 'notification_item', $2, '{}'::jsonb, $3, $4)`,
		uid, id, c.ClientIP(), c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel announcement"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel announcement"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// insertTeamNotification is the transactional producer hook for future Ledger,
// grader, and instance events. Call it with the mutation's transaction so the
// user-visible event commits exactly when the underlying action does. A stable
// dedupKey makes retries safe.
func insertTeamNotification(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, eventType, title, body, severity, href, dedupKey string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO notification_items
		(kind, event_type, title, body, severity, audience, team_id, href, payload, dedup_key)
		VALUES ('event', $1, $2, $3, $4, 'team', $5, NULLIF($6, ''), $7::jsonb, NULLIF($8, ''))
		ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`,
		eventType, title, body, severity, teamID, href, encoded, dedupKey)
	return err
}
