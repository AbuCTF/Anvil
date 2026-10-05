//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
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
	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestEventProfileAndBrandingLifecycle(t *testing.T) {
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
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, role, status) VALUES
		($1, $2, $3, 'admin', 'active'), ($4, $5, $6, 'user', 'active')
	`, adminID, "brand-admin-"+adminID.String(), adminID.String()+"@x", userID, "brand-user-"+userID.String(), userID.String()+"@x"); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE user_id IN ($1, $2)`, adminID, userID)
		_, _ = db.Pool.Exec(ctx, `UPDATE platform_settings SET updated_by = NULL WHERE updated_by IN ($1, $2)`, adminID, userID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, adminID, userID)
	}()

	store, err := storage.NewLocalStorage(t.TempDir(), zap.NewNop())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "branding-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, store, nil, zap.NewNop()).Router()
	token := func(uid uuid.UUID) string {
		signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: uid, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return signed
	}
	adminToken, userToken := token(adminID), token(userID)

	settingsBody := `{"settings":{"platform_name":"Sample Cyber Challenge","platform_description":"Enterprise cyber range","event.slug":"sample-cyber-2026","event.timezone":"Asia/Kolkata","event.contact_email":"ctf@example.com","event.rules_url":"https://example.com/rules","event.start_at":"2026-10-01T04:30:00Z","event.end_at":"2026-10-02T04:30:00Z","event.profile_managed":true,"event.setup_completed":true}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", strings.NewReader(settingsBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	infoResponse := httptest.NewRecorder()
	router.ServeHTTP(infoResponse, infoReq)
	var info map[string]any
	if err := json.Unmarshal(infoResponse.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info["name"] != "Sample Cyber Challenge" || info["slug"] != "sample-cyber-2026" || info["timezone"] != "Asia/Kolkata" {
		t.Fatalf("event profile not public: %v", info)
	}
	if _, exists := info["logo_url"]; exists {
		t.Fatalf("logo URL present before upload: %v", info["logo_url"])
	}

	picture := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			picture.Set(x, y, color.RGBA{R: 245, G: 158, B: 11, A: 255})
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, picture); err != nil {
		t.Fatalf("encode logo: %v", err)
	}
	upload := func(auth string, data []byte, filename string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("logo", filename)
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		_, _ = part.Write(data)
		_ = writer.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/branding/logo", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	if got := upload("", pngData.Bytes(), "logo.png").Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous upload status=%d", got)
	}
	if got := upload(userToken, pngData.Bytes(), "logo.png").Code; got != http.StatusForbidden {
		t.Fatalf("participant upload status=%d", got)
	}
	if got := upload(adminToken, []byte("not an image"), "logo.png").Code; got != http.StatusBadRequest {
		t.Fatalf("invalid image status=%d", got)
	}
	valid := upload(adminToken, pngData.Bytes(), "logo.png")
	if valid.Code != http.StatusOK {
		t.Fatalf("valid logo status=%d body=%s", valid.Code, valid.Body.String())
	}

	infoResponse = httptest.NewRecorder()
	router.ServeHTTP(infoResponse, infoReq)
	_ = json.Unmarshal(infoResponse.Body.Bytes(), &info)
	logoURL, ok := info["logo_url"].(string)
	if !ok || !strings.HasPrefix(logoURL, "/api/v1/branding/logo?v=") {
		t.Fatalf("logo URL=%v", info["logo_url"])
	}
	logoReq := httptest.NewRequest(http.MethodGet, logoURL, nil)
	logoResponse := httptest.NewRecorder()
	router.ServeHTTP(logoResponse, logoReq)
	if logoResponse.Code != http.StatusOK || logoResponse.Header().Get("Content-Type") != "image/png" || !bytes.Equal(logoResponse.Body.Bytes(), pngData.Bytes()) {
		t.Fatalf("logo response status=%d type=%q bytes=%d", logoResponse.Code, logoResponse.Header().Get("Content-Type"), logoResponse.Body.Len())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/branding/logo", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteReq)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/branding/logo", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted logo status=%d", missing.Code)
	}
}
