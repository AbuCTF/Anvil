//go:build integration

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/anvil-lab/anvil/internal/services/mailer"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func TestImportedAccountActivation(t *testing.T) {
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
	adminID := uuid.New()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	adminName, importedName, activatedName := "activation-admin-"+suffix, "invited-"+suffix, "activate-"+suffix
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'admin', 'active')`, adminID, adminName, adminName+"@example.test"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM data_import_jobs WHERE created_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_deliveries WHERE created_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_providers WHERE created_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE user_id IN (SELECT id FROM users WHERE username IN ($1, $2, $3))`, adminName, importedName, activatedName)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE username IN ($1, $2, $3)`, adminName, importedName, activatedName)
	}()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "activation-integration-secret-0123456789"
	cfg.Secrets.EncryptionKey = "activation-encryption-secret-0123456789"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	mailSvc, err := mailer.NewService(cfg.Secrets.EncryptionKey, db, zap.NewNop())
	if err != nil {
		t.Fatalf("mail service: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop(), mailSvc).Router()
	signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
		UserID: adminID, Role: "admin", TokenType: "user",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte(cfg.JWT.Secret))
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
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	provider := request(http.MethodPost, "/api/v1/admin/mail/providers", signed, map[string]any{
		"name": "Activation SMTP", "host": "smtp.example.test", "port": 587,
		"username": "apikey", "password": "mail-secret", "security": "starttls",
		"from_name": "Anvil", "from_address": "ctf@example.test", "priority": 1, "is_active": true,
	})
	if provider.Code != http.StatusCreated {
		t.Fatalf("provider status=%d body=%s", provider.Code, provider.Body.String())
	}
	var providerResult struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(provider.Body.Bytes(), &providerResult)
	if _, err := db.Pool.Exec(ctx, `UPDATE mail_providers SET is_healthy = TRUE WHERE id = $1`, providerResult.ID); err != nil {
		t.Fatalf("mark provider healthy: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE platform_settings SET value = '"https://ctf.example.test"'::jsonb WHERE key = 'event.public_url'`); err != nil {
		t.Fatalf("set public url: %v", err)
	}

	csv := "username,email,display_name,role,status,email_verified\n" + importedName + "," + importedName + "@example.test,Invited Player,user,active,false\n"
	preview := request(http.MethodPost, "/api/v1/admin/data/imports/preview", signed, map[string]any{
		"entity": "users", "format": "csv", "mode": "create", "source_name": "participants.csv", "content": csv, "provisioning": "activation_email",
	})
	if preview.Code != http.StatusCreated {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var previewResult struct {
		JobID    string `json:"job_id"`
		Checksum string `json:"checksum"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &previewResult)
	applied := request(http.MethodPost, "/api/v1/admin/data/imports/"+previewResult.JobID+"/apply", signed, map[string]string{"checksum": previewResult.Checksum})
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), `"activation_emails_queued":1`) {
		t.Fatalf("apply status=%d body=%s", applied.Code, applied.Body.String())
	}
	var importedID uuid.UUID
	var importedPassword *string
	var importedVerified bool
	if err := db.Pool.QueryRow(ctx, `SELECT id, password_hash, email_verified FROM users WHERE username = $1`, importedName).Scan(&importedID, &importedPassword, &importedVerified); err != nil || importedPassword != nil || importedVerified {
		t.Fatalf("imported user id=%s password=%v verified=%t error=%v", importedID, importedPassword, importedVerified, err)
	}
	preActivationLogin := request(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": importedName, "password": "not-active-yet"})
	if preActivationLogin.Code != http.StatusUnauthorized {
		t.Fatalf("pre-activation login status=%d body=%s", preActivationLogin.Code, preActivationLogin.Body.String())
	}
	preActivationReset := request(http.MethodPost, "/api/v1/auth/password-reset/request", "", map[string]string{"email": importedName + "@example.test"})
	var preActivationResetCount int
	if preActivationReset.Code != http.StatusAccepted {
		t.Fatalf("pre-activation reset status=%d body=%s", preActivationReset.Code, preActivationReset.Body.String())
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM account_activation_tokens WHERE user_id = $1 AND purpose = 'password_reset'`, importedID).Scan(&preActivationResetCount); err != nil || preActivationResetCount != 0 {
		t.Fatalf("pre-activation reset count=%d error=%v", preActivationResetCount, err)
	}
	var tokenCount, deliveryCount int
	var encryptedPayload []byte
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM account_activation_tokens WHERE user_id = $1 AND used_at IS NULL`, importedID).Scan(&tokenCount); err != nil || tokenCount != 1 {
		t.Fatalf("activation count=%d error=%v", tokenCount, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*)::int, (ARRAY_AGG(payload_ciphertext))[1] FROM mail_deliveries WHERE created_by = $1 AND recipient = $2`, adminID, importedName+"@example.test").Scan(&deliveryCount, &encryptedPayload); err != nil || deliveryCount != 1 || bytes.Contains(encryptedPayload, []byte("ctf.example.test")) {
		t.Fatalf("delivery count=%d encrypted=%t error=%v", deliveryCount, !bytes.Contains(encryptedPayload, []byte("ctf.example.test")), err)
	}

	activationID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'user', 'active')`, activationID, activatedName, activatedName+"@example.test"); err != nil {
		t.Fatalf("seed activation user: %v", err)
	}
	knownToken := strings.Repeat("a", 64)
	digest := sha256.Sum256([]byte(knownToken))
	if _, err := db.Pool.Exec(ctx, `INSERT INTO account_activation_tokens (user_id, token_hash, expires_at, created_by) VALUES ($1, $2, NOW() + INTERVAL '1 hour', $3)`, activationID, digest[:], adminID); err != nil {
		t.Fatalf("seed activation token: %v", err)
	}
	inspected := request(http.MethodPost, "/api/v1/auth/activation/inspect", "", map[string]string{"token": knownToken})
	if inspected.Code != http.StatusOK || !strings.Contains(inspected.Body.String(), activatedName) {
		t.Fatalf("inspect status=%d body=%s", inspected.Code, inspected.Body.String())
	}
	completed := request(http.MethodPost, "/api/v1/auth/activation/complete", "", map[string]string{"token": knownToken, "password": "correct-horse-battery"})
	if completed.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", completed.Code, completed.Body.String())
	}
	var passwordHash string
	var verified bool
	if err := db.Pool.QueryRow(ctx, `SELECT password_hash, email_verified FROM users WHERE id = $1`, activationID).Scan(&passwordHash, &verified); err != nil || !verified || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("correct-horse-battery")) != nil {
		t.Fatalf("activated verified=%t error=%v", verified, err)
	}
	activatedLogin := request(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": activatedName, "password": "correct-horse-battery"})
	if activatedLogin.Code != http.StatusOK {
		t.Fatalf("activated login status=%d body=%s", activatedLogin.Code, activatedLogin.Body.String())
	}
	resetRequested := request(http.MethodPost, "/api/v1/auth/password-reset/request", "", map[string]string{"email": activatedName + "@example.test"})
	unknownReset := request(http.MethodPost, "/api/v1/auth/password-reset/request", "", map[string]string{"email": "missing-" + activatedName + "@example.test"})
	if resetRequested.Code != http.StatusAccepted || unknownReset.Code != http.StatusAccepted || resetRequested.Body.String() != unknownReset.Body.String() {
		t.Fatalf("reset responses existing=%d/%s unknown=%d/%s", resetRequested.Code, resetRequested.Body.String(), unknownReset.Code, unknownReset.Body.String())
	}
	var resetTokenCount int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM account_activation_tokens WHERE user_id = $1 AND purpose = 'password_reset' AND used_at IS NULL`, activationID).Scan(&resetTokenCount); err != nil || resetTokenCount != 1 {
		t.Fatalf("reset token count=%d error=%v", resetTokenCount, err)
	}
	knownResetToken := strings.Repeat("b", 64)
	resetDigest := sha256.Sum256([]byte(knownResetToken))
	if _, err := db.Pool.Exec(ctx, `UPDATE account_activation_tokens SET used_at = NOW() WHERE user_id = $1 AND purpose = 'password_reset' AND used_at IS NULL`, activationID); err != nil {
		t.Fatalf("expire reset token: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO account_activation_tokens (user_id, purpose, token_hash, expires_at) VALUES ($1, 'password_reset', $2, NOW() + INTERVAL '1 hour')`, activationID, resetDigest[:]); err != nil {
		t.Fatalf("replace reset token: %v", err)
	}
	resetInspected := request(http.MethodPost, "/api/v1/auth/password-reset/inspect", "", map[string]string{"token": knownResetToken})
	if resetInspected.Code != http.StatusOK || !strings.Contains(resetInspected.Body.String(), activatedName) {
		t.Fatalf("reset inspect status=%d body=%s", resetInspected.Code, resetInspected.Body.String())
	}
	resetCompleted := request(http.MethodPost, "/api/v1/auth/password-reset/complete", "", map[string]string{"token": knownResetToken, "password": "new-correct-password"})
	if resetCompleted.Code != http.StatusOK {
		t.Fatalf("reset complete status=%d body=%s", resetCompleted.Code, resetCompleted.Body.String())
	}
	oldPasswordLogin := request(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": activatedName, "password": "correct-horse-battery"})
	newPasswordLogin := request(http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": activatedName, "password": "new-correct-password"})
	if oldPasswordLogin.Code != http.StatusUnauthorized || newPasswordLogin.Code != http.StatusOK {
		t.Fatalf("post-reset old=%d new=%d", oldPasswordLogin.Code, newPasswordLogin.Code)
	}
	replayed := request(http.MethodPost, "/api/v1/auth/activation/complete", "", map[string]string{"token": knownToken, "password": "another-password"})
	if replayed.Code != http.StatusBadRequest {
		t.Fatalf("replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
}
