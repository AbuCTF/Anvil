package handlers

import (
	"errors"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/mailer"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type MailHandler struct {
	db        *database.DB
	logger    *zap.Logger
	cipher    *mailer.Cipher
	cipherErr error
}

type mailProviderRequest struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Security    string `json:"security"`
	FromName    string `json:"from_name"`
	FromAddress string `json:"from_address"`
	ReplyTo     string `json:"reply_to"`
	Priority    int    `json:"priority"`
	DailyLimit  *int   `json:"daily_limit"`
	HourlyLimit *int   `json:"hourly_limit"`
	MinuteLimit *int   `json:"minute_limit"`
	IsActive    bool   `json:"is_active"`
}

type mailTemplateRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Subject     string `json:"subject"`
	BodyHTML    string `json:"body_html"`
	BodyText    string `json:"body_text"`
	IsActive    bool   `json:"is_active"`
}

type storedMailProvider struct {
	ID                 uuid.UUID
	Name               string
	Host               string
	Port               int
	Username           string
	PasswordCiphertext []byte
	Security           string
	FromName           string
	FromAddress        string
	ReplyTo            string
	Priority           int
	DailyLimit         *int
	HourlyLimit        *int
	MinuteLimit        *int
	IsActive           bool
}

var mailTemplateSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
var mailTemplateVariablePattern = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

var mailTemplateVariables = map[string]bool{
	"participant_name":   true,
	"event_name":         true,
	"activation_url":     true,
	"reset_url":          true,
	"login_url":          true,
	"username":           true,
	"temporary_password": true,
	"expires_at":         true,
	"event_start":        true,
	"event_end":          true,
	"support_email":      true,
	"team_name":          true,
	"update_title":       true,
	"update_body":        true,
	"rank":               true,
	"score":              true,
}

func NewMailHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *MailHandler {
	handler := &MailHandler{db: db, logger: logger}
	if cfg == nil {
		handler.cipherErr = errors.New("mail encryption is unavailable")
		return handler
	}
	secret := strings.TrimSpace(cfg.Secrets.EncryptionKey)
	if secret == "" {
		secret = cfg.JWT.Secret
	}
	handler.cipher, handler.cipherErr = mailer.NewCipher(secret)
	return handler
}

func (h *MailHandler) ListProviders(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT p.id, p.name, p.host, p.port, COALESCE(p.username, ''), p.security,
		       p.from_name, p.from_address, COALESCE(p.reply_to, ''), p.priority,
		       p.daily_limit, p.hourly_limit, p.minute_limit, p.is_active, p.is_healthy,
		       p.failure_count, p.circuit_open_until, p.last_error_code, p.last_error_at,
		       p.created_at, p.updated_at,
		       (SELECT COUNT(*)::int FROM mail_deliveries d WHERE d.provider_id = p.id AND d.status = 'sent' AND d.sent_at >= date_trunc('day', NOW())),
		       (SELECT COUNT(*)::int FROM mail_deliveries d WHERE d.provider_id = p.id AND d.status = 'sent' AND d.sent_at >= NOW() - INTERVAL '1 hour'),
		       (SELECT COUNT(*)::int FROM mail_deliveries d WHERE d.provider_id = p.id AND d.status = 'sent' AND d.sent_at >= NOW() - INTERVAL '1 minute')
		FROM mail_providers p ORDER BY p.priority, p.created_at
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail providers"})
		return
	}
	defer rows.Close()
	providers := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var name, host, username, security, fromName, fromAddress, replyTo string
		var port, priority, failures, dailyUsed, hourlyUsed, minuteUsed int
		var dailyLimit, hourlyLimit, minuteLimit *int
		var active, healthy bool
		var circuitOpenUntil, lastErrorAt *time.Time
		var lastErrorCode *string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &name, &host, &port, &username, &security, &fromName, &fromAddress, &replyTo, &priority, &dailyLimit, &hourlyLimit, &minuteLimit, &active, &healthy, &failures, &circuitOpenUntil, &lastErrorCode, &lastErrorAt, &createdAt, &updatedAt, &dailyUsed, &hourlyUsed, &minuteUsed); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail providers"})
			return
		}
		providers = append(providers, gin.H{
			"id": id, "name": name, "host": host, "port": port, "username": username,
			"password_configured": true, "security": security, "from_name": fromName,
			"from_address": fromAddress, "reply_to": replyTo, "priority": priority,
			"daily_limit": dailyLimit, "hourly_limit": hourlyLimit, "minute_limit": minuteLimit,
			"daily_used": dailyUsed, "hourly_used": hourlyUsed, "minute_used": minuteUsed,
			"is_active": active, "is_healthy": healthy, "failure_count": failures,
			"circuit_open_until": circuitOpenUntil, "last_error_code": lastErrorCode,
			"last_error_at": lastErrorAt, "created_at": createdAt, "updated_at": updatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

func (h *MailHandler) CreateProvider(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var request mailProviderRequest
	if err := bindMailProviderRequest(c, &request, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.cipherErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mail secret storage is not configured"})
		return
	}
	ciphertext, err := h.cipher.Encrypt(request.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to protect provider credentials"})
		return
	}
	var id uuid.UUID
	err = h.db.Pool.QueryRow(c.Request.Context(), `
		INSERT INTO mail_providers
		(name, host, port, username, password_ciphertext, security, from_name, from_address, reply_to, priority, daily_limit, hourly_limit, minute_limit, is_active, created_by, updated_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, $15, $15)
		RETURNING id
	`, request.Name, request.Host, request.Port, request.Username, ciphertext, request.Security, request.FromName, request.FromAddress, request.ReplyTo, request.Priority, request.DailyLimit, request.HourlyLimit, request.MinuteLimit, request.IsActive, uid).Scan(&id)
	if err != nil {
		h.logger.Error("create mail provider", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create mail provider"})
		return
	}
	_ = logAdminAction(h.db, c, uid.String(), "mail_provider_created", "mail_provider", id.String(), map[string]any{"name": request.Name, "host": request.Host, "priority": request.Priority})
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *MailHandler) UpdateProvider(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})
		return
	}
	var request mailProviderRequest
	if err := bindMailProviderRequest(c, &request, false); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var ciphertext []byte
	if request.Password != "" {
		if h.cipherErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mail secret storage is not configured"})
			return
		}
		ciphertext, err = h.cipher.Encrypt(request.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to protect provider credentials"})
			return
		}
	}
	result, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE mail_providers SET
		name = $2, host = $3, port = $4, username = NULLIF($5, ''),
		password_ciphertext = CASE WHEN $6::bytea IS NULL THEN password_ciphertext ELSE $6 END,
		security = $7, from_name = $8, from_address = $9, reply_to = NULLIF($10, ''),
		priority = $11, daily_limit = $12, hourly_limit = $13, minute_limit = $14,
		is_active = $15, is_healthy = CASE WHEN $6::bytea IS NULL THEN is_healthy ELSE FALSE END,
		failure_count = CASE WHEN $6::bytea IS NULL THEN failure_count ELSE 0 END,
		circuit_open_until = CASE WHEN $6::bytea IS NULL THEN circuit_open_until ELSE NULL END,
		updated_by = $16, updated_at = NOW()
		WHERE id = $1
	`, id, request.Name, request.Host, request.Port, request.Username, nullableBytes(ciphertext), request.Security, request.FromName, request.FromAddress, request.ReplyTo, request.Priority, request.DailyLimit, request.HourlyLimit, request.MinuteLimit, request.IsActive, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update mail provider"})
		return
	}
	if result.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "mail provider not found"})
		return
	}
	_ = logAdminAction(h.db, c, uid.String(), "mail_provider_updated", "mail_provider", id.String(), map[string]any{"name": request.Name, "host": request.Host, "priority": request.Priority, "credential_rotated": request.Password != ""})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *MailHandler) DeleteProvider(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})
		return
	}
	result, err := h.db.Pool.Exec(c.Request.Context(), `DELETE FROM mail_providers WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete mail provider"})
		return
	}
	if result.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "mail provider not found"})
		return
	}
	_ = logAdminAction(h.db, c, uid.String(), "mail_provider_deleted", "mail_provider", id.String(), nil)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *MailHandler) TestProvider(c *gin.Context) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})
		return
	}
	var request struct {
		Recipient string `json:"recipient"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || mailer.ValidateAddress(request.Recipient) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid recipient is required"})
		return
	}
	provider, err := h.loadProvider(c, id)
	if err != nil {
		return
	}
	if h.cipherErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mail secret storage is not configured"})
		return
	}
	password, err := h.cipher.Decrypt(provider.PasswordCiphertext)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "provider credentials cannot be decrypted"})
		return
	}
	deliveryID := uuid.New()
	if _, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO mail_deliveries (id, recipient, status, attempts, max_attempts, provider_id, provider_name, created_by)
		VALUES ($1, $2, 'sending', 1, 1, $3, $4, $5)
	`, deliveryID, strings.ToLower(strings.TrimSpace(request.Recipient)), id, provider.Name, uid); err != nil {
		h.logger.Error("record mail provider test", zap.String("provider_id", id.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "test delivery could not be recorded"})
		return
	}
	messageID, sendErr := mailer.Send(c.Request.Context(), provider.mailerProvider(password), mailer.Message{
		To: request.Recipient, Subject: "Anvil mail delivery test",
		HTML: "<p>Anvil successfully delivered this message through <strong>" + html.EscapeString(provider.Name) + "</strong>.</p>",
		Text: "Anvil successfully delivered this test message through " + provider.Name + ".",
	})
	status, errorCode := "sent", ""
	if sendErr != nil {
		status, errorCode = "failed", mailErrorCode(sendErr)
		h.logger.Warn("mail provider test failed", zap.String("provider_id", id.String()), zap.String("error_code", errorCode), zap.Error(sendErr))
	}
	if _, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE mail_deliveries SET status = $2, error_code = NULLIF($3, ''), message_id = NULLIF($4, ''),
			sent_at = CASE WHEN $2 = 'sent' THEN NOW() ELSE NULL END, updated_at = NOW()
		WHERE id = $1
	`, deliveryID, status, errorCode, messageID); err != nil {
		h.logger.Error("finalize mail provider test", zap.String("delivery_id", deliveryID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "test delivery result could not be recorded"})
		return
	}
	if sendErr != nil {
		_, _ = h.db.Pool.Exec(c.Request.Context(), `UPDATE mail_providers SET is_healthy = FALSE, failure_count = failure_count + 1, last_error_code = $2, last_error_at = NOW(), updated_at = NOW() WHERE id = $1`, id, errorCode)
		c.JSON(http.StatusBadGateway, gin.H{"error": "test delivery failed", "error_code": errorCode})
		return
	}
	_, _ = h.db.Pool.Exec(c.Request.Context(), `UPDATE mail_providers SET is_healthy = TRUE, failure_count = 0, circuit_open_until = NULL, last_error_code = NULL, last_error_at = NULL, updated_at = NOW() WHERE id = $1`, id)
	_ = logAdminAction(h.db, c, uid.String(), "mail_provider_tested", "mail_provider", id.String(), map[string]any{"success": true})
	c.JSON(http.StatusOK, gin.H{"sent": true, "message_id": messageID})
}

func (h *MailHandler) ListTemplates(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `SELECT id, slug, name, COALESCE(description, ''), subject, body_html, COALESCE(body_text, ''), variables, is_active, created_at, updated_at FROM mail_templates ORDER BY slug`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail templates"})
		return
	}
	defer rows.Close()
	templates := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var slug, name, description, subject, bodyHTML, bodyText string
		var variables []string
		var active bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &slug, &name, &description, &subject, &bodyHTML, &bodyText, &variables, &active, &createdAt, &updatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail templates"})
			return
		}
		templates = append(templates, gin.H{"id": id, "slug": slug, "name": name, "description": description, "subject": subject, "body_html": bodyHTML, "body_text": bodyText, "variables": variables, "is_active": active, "created_at": createdAt, "updated_at": updatedAt})
	}
	c.JSON(http.StatusOK, gin.H{"templates": templates, "allowed_variables": sortedMailTemplateVariables()})
}

func (h *MailHandler) CreateTemplate(c *gin.Context) {
	h.saveTemplate(c, true)
}

func (h *MailHandler) UpdateTemplate(c *gin.Context) {
	h.saveTemplate(c, false)
}

func (h *MailHandler) saveTemplate(c *gin.Context, creating bool) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var request mailTemplateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template request"})
		return
	}
	slug := strings.TrimSpace(c.Param("slug"))
	if creating {
		slug = strings.TrimSpace(request.Slug)
	}
	if !mailTemplateSlugPattern.MatchString(slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug must use lowercase letters, numbers, and underscores"})
		return
	}
	variables, err := validateMailTemplateRequest(&request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if creating {
		_, err = h.db.Pool.Exec(c.Request.Context(), `INSERT INTO mail_templates (slug, name, description, subject, body_html, body_text, variables, is_active, updated_by) VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7, $8, $9)`, slug, strings.TrimSpace(request.Name), strings.TrimSpace(request.Description), strings.TrimSpace(request.Subject), request.BodyHTML, request.BodyText, variables, request.IsActive, uid)
	} else {
		result, updateErr := h.db.Pool.Exec(c.Request.Context(), `UPDATE mail_templates SET name = $2, description = NULLIF($3, ''), subject = $4, body_html = $5, body_text = NULLIF($6, ''), variables = $7, is_active = $8, updated_by = $9, updated_at = NOW() WHERE slug = $1`, slug, strings.TrimSpace(request.Name), strings.TrimSpace(request.Description), strings.TrimSpace(request.Subject), request.BodyHTML, request.BodyText, variables, request.IsActive, uid)
		err = updateErr
		if err == nil && result.RowsAffected() != 1 {
			c.JSON(http.StatusNotFound, gin.H{"error": "mail template not found"})
			return
		}
	}
	if err != nil {
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "a template with this slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save mail template"})
		return
	}
	action := "mail_template_updated"
	if creating {
		action = "mail_template_created"
	}
	_ = logAdminAction(h.db, c, uid.String(), action, "mail_template", "", map[string]any{"slug": slug, "variables": variables})
	c.JSON(http.StatusOK, gin.H{"success": true, "slug": slug, "variables": variables})
}

func (h *MailHandler) PreviewTemplate(c *gin.Context) {
	var request struct {
		Subject  string            `json:"subject"`
		BodyHTML string            `json:"body_html"`
		BodyText string            `json:"body_text"`
		Values   map[string]string `json:"values"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid preview request"})
		return
	}
	templateRequest := mailTemplateRequest{Name: "Preview", Subject: request.Subject, BodyHTML: request.BodyHTML, BodyText: request.BodyText, IsActive: true}
	if _, err := validateMailTemplateRequest(&templateRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for key := range request.Values {
		if !mailTemplateVariables[key] || len(request.Values[key]) > 4000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "preview contains an unsupported value"})
			return
		}
	}
	subject, bodyHTML, bodyText := mailer.Render(request.Subject, request.BodyHTML, request.BodyText, request.Values)
	c.JSON(http.StatusOK, gin.H{"subject": subject, "body_html": bodyHTML, "body_text": bodyText})
}

func (h *MailHandler) ListDeliveries(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `SELECT id, recipient, template_slug, status, attempts, max_attempts, provider_name, error_code, message_id, not_before, sent_at, created_at FROM mail_deliveries ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail deliveries"})
		return
	}
	defer rows.Close()
	deliveries := []gin.H{}
	for rows.Next() {
		var id uuid.UUID
		var recipient, status string
		var templateSlug, providerName, errorCode, messageID *string
		var attempts, maxAttempts int
		var notBefore, createdAt time.Time
		var sentAt *time.Time
		if err := rows.Scan(&id, &recipient, &templateSlug, &status, &attempts, &maxAttempts, &providerName, &errorCode, &messageID, &notBefore, &sentAt, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail deliveries"})
			return
		}
		deliveries = append(deliveries, gin.H{"id": id, "recipient": recipient, "template_slug": templateSlug, "status": status, "attempts": attempts, "max_attempts": maxAttempts, "provider_name": providerName, "error_code": errorCode, "message_id": messageID, "not_before": notBefore, "sent_at": sentAt, "created_at": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"deliveries": deliveries})
}

func bindMailProviderRequest(c *gin.Context, request *mailProviderRequest, creating bool) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(request); err != nil {
		return errors.New("invalid provider request")
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Host = strings.TrimSpace(request.Host)
	request.Username = strings.TrimSpace(request.Username)
	request.Security = strings.ToLower(strings.TrimSpace(request.Security))
	request.FromName = strings.TrimSpace(request.FromName)
	request.FromAddress = strings.ToLower(strings.TrimSpace(request.FromAddress))
	request.ReplyTo = strings.ToLower(strings.TrimSpace(request.ReplyTo))
	if request.Name == "" || len([]rune(request.Name)) > 100 {
		return errors.New("name must be between 1 and 100 characters")
	}
	if creating && request.Password == "" {
		return errors.New("password is required")
	}
	if len(request.Password) > 4096 {
		return errors.New("password is too long")
	}
	if request.Priority < 0 || request.Priority > 1000 {
		return errors.New("priority must be between 0 and 1000")
	}
	for _, limit := range []*int{request.DailyLimit, request.HourlyLimit, request.MinuteLimit} {
		if limit != nil && (*limit < 1 || *limit > 10_000_000) {
			return errors.New("mail limits must be between 1 and 10000000")
		}
	}
	return mailer.ValidateProvider(mailer.Provider{Host: request.Host, Port: request.Port, Username: request.Username, Password: request.Password, Security: request.Security, FromName: request.FromName, FromAddress: request.FromAddress, ReplyTo: request.ReplyTo})
}

func (h *MailHandler) loadProvider(c *gin.Context, id uuid.UUID) (storedMailProvider, error) {
	var provider storedMailProvider
	err := h.db.Pool.QueryRow(c.Request.Context(), `SELECT id, name, host, port, COALESCE(username, ''), password_ciphertext, security, from_name, from_address, COALESCE(reply_to, ''), priority, daily_limit, hourly_limit, minute_limit, is_active FROM mail_providers WHERE id = $1`, id).Scan(&provider.ID, &provider.Name, &provider.Host, &provider.Port, &provider.Username, &provider.PasswordCiphertext, &provider.Security, &provider.FromName, &provider.FromAddress, &provider.ReplyTo, &provider.Priority, &provider.DailyLimit, &provider.HourlyLimit, &provider.MinuteLimit, &provider.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "mail provider not found"})
		return provider, err
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load mail provider"})
		return provider, err
	}
	return provider, nil
}

func (p storedMailProvider) mailerProvider(password string) mailer.Provider {
	return mailer.Provider{Host: p.Host, Port: p.Port, Username: p.Username, Password: password, Security: p.Security, FromName: p.FromName, FromAddress: p.FromAddress, ReplyTo: p.ReplyTo}
}

func validateMailTemplateRequest(request *mailTemplateRequest) ([]string, error) {
	request.Name = strings.TrimSpace(request.Name)
	request.Subject = strings.TrimSpace(request.Subject)
	if request.Name == "" || len([]rune(request.Name)) > 160 {
		return nil, errors.New("template name must be between 1 and 160 characters")
	}
	if request.Subject == "" || len([]rune(request.Subject)) > 500 || strings.ContainsAny(request.Subject, "\r\n") {
		return nil, errors.New("subject must be between 1 and 500 characters without line breaks")
	}
	if strings.TrimSpace(request.BodyHTML) == "" || len(request.BodyHTML) > 200_000 || len(request.BodyText) > 200_000 {
		return nil, errors.New("HTML is required and each template body is limited to 200 KB")
	}
	seen := map[string]bool{}
	for _, source := range []string{request.Subject, request.BodyHTML, request.BodyText} {
		for _, match := range mailTemplateVariablePattern.FindAllStringSubmatch(source, -1) {
			if !mailTemplateVariables[match[1]] {
				return nil, errors.New("unsupported template variable: " + match[1])
			}
			seen[match[1]] = true
		}
	}
	variables := make([]string, 0, len(seen))
	for variable := range seen {
		variables = append(variables, variable)
	}
	sort.Strings(variables)
	return variables, nil
}

func sortedMailTemplateVariables() []string {
	variables := make([]string, 0, len(mailTemplateVariables))
	for variable := range mailTemplateVariables {
		variables = append(variables, variable)
	}
	sort.Strings(variables)
	return variables
}

func mailErrorCode(err error) string {
	return mailer.ErrorCode(err)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
