//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	"golang.org/x/crypto/bcrypt"
)

func TestAccountSessionLifecycle(t *testing.T) {
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
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	adminName, username, password := "session-admin-"+suffix, "session-user-"+suffix, "session-test-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, role, status, email_verified) VALUES ($1, $2, $3, $4, 'admin', 'active', TRUE), ($5, $6, $7, $8, 'user', 'active', TRUE)`, adminID, adminName, adminName+"@example.test", string(hash), userID, username, username+"@example.test", string(hash)); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "account-session-integration-secret"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	adminToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
		UserID: adminID, Username: adminName, Role: "admin", TokenType: "user",
		RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte(cfg.JWT.Secret))

	request := func(method, path, token, remote, agent string, payload any) *httptest.ResponseRecorder {
		var body bytes.Buffer
		if payload != nil {
			_ = json.NewEncoder(&body).Encode(payload)
		}
		req := httptest.NewRequest(method, path, &body)
		req.RemoteAddr = remote
		req.Header.Set("User-Agent", agent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	type authResult struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	login := func(remote, agent string) authResult {
		response := request(http.MethodPost, "/api/v1/auth/login", "", remote, agent, map[string]string{"username": username, "password": password})
		if response.Code != http.StatusOK {
			t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
		}
		var result authResult
		_ = json.Unmarshal(response.Body.Bytes(), &result)
		return result
	}
	parse := func(token string) *middleware.Claims {
		claims, err := middleware.ParseAccessToken(token, cfg)
		if err != nil {
			t.Fatalf("parse access token: %v", err)
		}
		return claims
	}

	first := login("203.0.113.40:4100", "Mozilla/5.0 Chrome/154.0.0.0 Windows NT 10.0")
	firstClaims := parse(first.AccessToken)
	if firstClaims.SessionID == uuid.Nil {
		t.Fatal("login access token has no session identity")
	}
	if response := request(http.MethodGet, "/api/v1/user/me", first.AccessToken, "203.0.113.40:4101", "browser", nil); response.Code != http.StatusOK {
		t.Fatalf("fresh access status=%d body=%s", response.Code, response.Body.String())
	}
	var storedIP, storedAgent string
	if err := db.Pool.QueryRow(ctx, `SELECT host(ip_address), user_agent FROM refresh_tokens WHERE user_id = $1 AND session_id = $2 AND revoked = FALSE`, userID, firstClaims.SessionID).Scan(&storedIP, &storedAgent); err != nil || storedIP != "203.0.113.40" || !strings.Contains(storedAgent, "Chrome/154") {
		t.Fatalf("stored session evidence ip=%q agent=%q err=%v", storedIP, storedAgent, err)
	}

	refreshedResponse := request(http.MethodPost, "/api/v1/auth/refresh", "", "203.0.113.41:4200", "Mozilla/5.0 Firefox/145.0 Linux", map[string]string{"refresh_token": first.RefreshToken})
	if refreshedResponse.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshedResponse.Code, refreshedResponse.Body.String())
	}
	var refreshed authResult
	_ = json.Unmarshal(refreshedResponse.Body.Bytes(), &refreshed)
	refreshedClaims := parse(refreshed.AccessToken)
	if refreshedClaims.SessionID != firstClaims.SessionID {
		t.Fatalf("refresh changed session family from %s to %s", firstClaims.SessionID, refreshedClaims.SessionID)
	}
	var activeTokens, revokedTokens int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE revoked = FALSE), COUNT(*) FILTER (WHERE revoked = TRUE) FROM refresh_tokens WHERE user_id = $1 AND session_id = $2`, userID, firstClaims.SessionID).Scan(&activeTokens, &revokedTokens); err != nil || activeTokens != 1 || revokedTokens != 1 {
		t.Fatalf("rotation active=%d revoked=%d err=%v", activeTokens, revokedTokens, err)
	}

	revokeOne := request(http.MethodDelete, "/api/v1/admin/users/"+userID.String()+"/sessions/"+firstClaims.SessionID.String(), adminToken, "203.0.113.1:4300", "admin-browser", nil)
	if revokeOne.Code != http.StatusOK {
		t.Fatalf("revoke session status=%d body=%s", revokeOne.Code, revokeOne.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/user/me", refreshed.AccessToken, "203.0.113.41:4201", "browser", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked access status=%d body=%s", response.Code, response.Body.String())
	}

	second := login("203.0.113.42:4400", "Mozilla/5.0 Safari/605.1.15 Version/18.0 Mac OS X 10_15_7")
	secondClaims := parse(second.AccessToken)
	if secondClaims.SessionID == firstClaims.SessionID {
		t.Fatal("new login reused revoked session identity")
	}
	revokeAll := request(http.MethodPost, "/api/v1/admin/users/"+userID.String()+"/sessions/revoke", adminToken, "203.0.113.1:4301", "admin-browser", nil)
	if revokeAll.Code != http.StatusOK {
		t.Fatalf("revoke all status=%d body=%s", revokeAll.Code, revokeAll.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/user/me", second.AccessToken, "203.0.113.42:4401", "browser", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoke all access status=%d body=%s", response.Code, response.Body.String())
	}

	detail := request(http.MethodGet, "/api/v1/admin/users/"+userID.String()+"/detail", adminToken, "203.0.113.1:4302", "admin-browser", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var dossier map[string]any
	_ = json.Unmarshal(detail.Body.Bytes(), &dossier)
	if len(dossier["auth_sessions"].([]any)) != 2 || len(dossier["login_history"].([]any)) != 2 || dossier["access_summary"].(map[string]any)["active_sessions"].(float64) != 0 {
		t.Fatalf("session dossier mismatch: %#v", dossier)
	}
}
