//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestMailProviderAndTemplateAdministration(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	adminID, userID := uuid.New(), uuid.New()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'admin', 'active'), ($4, $5, $6, 'user', 'active')`, adminID, "mail-admin-"+adminID.String(), adminID.String()+"@example.test", userID, "mail-user-"+userID.String(), userID.String()+"@example.test"); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_deliveries WHERE created_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_providers WHERE created_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_templates WHERE slug = 'custom_notice_test'`)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE user_id = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, adminID, userID)
	}()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "mail-integration-secret-0123456789abcdef"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	token := func(uid uuid.UUID, role string) string {
		signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: uid, Role: role, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return signed
	}
	adminToken, userToken := token(adminID, "admin"), token(userID, "user")
	request := func(method, path, auth string, payload any) *httptest.ResponseRecorder {
		var body io.Reader
		if payload != nil {
			encoded, _ := json.Marshal(payload)
			body = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, path, body)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+auth)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	if got := request(http.MethodGet, "/api/v1/admin/mail/providers", userToken, nil).Code; got != http.StatusForbidden {
		t.Fatalf("participant provider status=%d", got)
	}
	secret := "zepto-test-secret"
	created := request(http.MethodPost, "/api/v1/admin/mail/providers", adminToken, map[string]any{
		"name": "ZeptoMail test", "host": "smtp.zeptomail.in", "port": 587,
		"username": "emailapikey", "password": secret, "security": "starttls",
		"from_name": "Anvil", "from_address": "ctf@example.test", "reply_to": "support@example.test",
		"priority": 1, "daily_limit": 5000, "hourly_limit": 2000, "minute_limit": 100, "is_active": true,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider status=%d body=%s", created.Code, created.Body.String())
	}
	var createResult struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &createResult)
	var ciphertext []byte
	if err := db.Pool.QueryRow(ctx, `SELECT password_ciphertext FROM mail_providers WHERE id = $1`, createResult.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("load ciphertext: %v", err)
	}
	if bytes.Contains(ciphertext, []byte(secret)) {
		t.Fatal("provider secret stored in plaintext")
	}
	listed := request(http.MethodGet, "/api/v1/admin/mail/providers", adminToken, nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), secret) || strings.Contains(listed.Body.String(), "password_ciphertext") {
		t.Fatalf("unsafe provider list status=%d body=%s", listed.Code, listed.Body.String())
	}

	templates := request(http.MethodGet, "/api/v1/admin/mail/templates", adminToken, nil)
	if templates.Code != http.StatusOK || !strings.Contains(templates.Body.String(), "account_activation") || !strings.Contains(templates.Body.String(), "password_reset") {
		t.Fatalf("default templates status=%d body=%s", templates.Code, templates.Body.String())
	}
	createdTemplate := request(http.MethodPost, "/api/v1/admin/mail/templates", adminToken, map[string]any{
		"slug": "custom_notice_test", "name": "Custom notice", "subject": "{{event_name}} notice",
		"body_html": "<p>Hello {{participant_name}}</p>", "body_text": "Hello {{participant_name}}", "is_active": true,
	})
	if createdTemplate.Code != http.StatusOK {
		t.Fatalf("create template status=%d body=%s", createdTemplate.Code, createdTemplate.Body.String())
	}
	invalidTemplate := request(http.MethodPut, "/api/v1/admin/mail/templates/custom_notice_test", adminToken, map[string]any{
		"name": "Custom notice", "subject": "Notice", "body_html": "{{database_password}}", "is_active": true,
	})
	if invalidTemplate.Code != http.StatusBadRequest {
		t.Fatalf("unsafe template status=%d body=%s", invalidTemplate.Code, invalidTemplate.Body.String())
	}
	preview := request(http.MethodPost, "/api/v1/admin/mail/templates/preview", adminToken, map[string]any{
		"subject": "Hello {{participant_name}}", "body_html": "<p>{{participant_name}}</p>", "body_text": "{{participant_name}}",
		"values": map[string]string{"participant_name": "<b>Abu</b>"},
	})
	var previewResult struct {
		BodyHTML string `json:"body_html"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &previewResult)
	if preview.Code != http.StatusOK || !strings.Contains(previewResult.BodyHTML, "&lt;b&gt;Abu&lt;/b&gt;") {
		t.Fatalf("template preview status=%d body=%s", preview.Code, preview.Body.String())
	}
}
