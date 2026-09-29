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

	"github.com/anvil-lab/anvil/internal/api/handlers"
	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestReleaseReadinessAndImmutableCandidate(t *testing.T) {
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
	if _, err := db.Pool.Exec(ctx, `TRUNCATE users, teams, challenges, categories CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	adminID, userID, categoryID, challengeID, staticID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, username, email, role, status) VALUES ($1, 'readiness-admin', 'admin@readiness.test', 'admin', 'active'), ($2, 'readiness-user', 'user@readiness.test', 'user', 'active')`, []any{adminID, userID}},
		{`INSERT INTO categories (id, name, slug) VALUES ($1, 'Readiness', 'readiness')`, []any{categoryID}},
		{`INSERT INTO challenges (id, name, slug, difficulty, category_id, status, container_image, resource_type, scoring_mode) VALUES ($1, 'Release probe', 'release-probe', 'easy', $2, 'published', 'registry.invalid/release-probe:1', 'docker', 'flag')`, []any{challengeID, categoryID}},
		{`INSERT INTO flags (challenge_id, name, flag_hash, points) VALUES ($1, 'Flag', 'hash', 100)`, []any{challengeID}},
		{`INSERT INTO challenges (id, name, slug, difficulty, category_id, status, container_image, resource_type, scoring_mode, exposed_ports) VALUES ($1, 'Static probe', 'static-probe', 'easy', $2, 'published', '', 'docker', 'flag', 'null'::jsonb)`, []any{staticID, categoryID}},
		{`INSERT INTO flags (challenge_id, name, flag_hash, points) VALUES ($1, 'Flag', 'hash', 100)`, []any{staticID}},
	}
	for _, statement := range seed {
		if _, err := db.Pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed content: %v", err)
		}
	}
	settings := map[string]any{
		"platform_name": "Readiness Event", "event.slug": "readiness-event", "event.timezone": "UTC",
		"event.start_at":    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		"event.end_at":      time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339),
		"registration_mode": "open", "economy_mode": true, "market_pulse_enabled": false,
		"event.contact_email": "ops@readiness.test", "event.rules_url": "https://readiness.test/rules",
		"event.privacy_url": "https://readiness.test/privacy", "event.terms_url": "https://readiness.test/terms",
		"event.setup_completed": true,
	}
	for key, value := range settings {
		encoded, _ := json.Marshal(value)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO platform_settings (key, value) VALUES ($1, $2::jsonb) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, encoded); err != nil {
			t.Fatalf("setting %s: %v", key, err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "readiness-integration-secret!"
	cfg.JWT.Issuer = ""
	handler := handlers.NewReadinessHandler(cfg, db, true, true, zap.NewNop())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin", middleware.Auth(cfg, db), middleware.RequireRole("admin"))
	admin.GET("/readiness", handler.Report)
	admin.GET("/release-candidates", handler.ListReleases)
	admin.POST("/release-candidates", handler.CreateRelease)
	token := func(id uuid.UUID, role string) string {
		signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: id, Role: role, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return signed
	}
	request := func(method, path, auth string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+auth)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	adminToken := token(adminID, "admin")
	if got := request(http.MethodGet, "/api/v1/admin/readiness", token(userID, "user"), nil).Code; got != http.StatusForbidden {
		t.Fatalf("participant readiness status=%d", got)
	}
	report := request(http.MethodGet, "/api/v1/admin/readiness", adminToken, nil)
	if report.Code != http.StatusOK || !strings.Contains(report.Body.String(), `"ready":true`) || !strings.Contains(report.Body.String(), `"operations.restore"`) {
		t.Fatalf("readiness status=%d body=%s", report.Code, report.Body.String())
	}
	withoutWaiver := request(http.MethodPost, "/api/v1/admin/release-candidates", adminToken, bytes.NewBufferString(`{"waive_warnings":[]}`))
	if withoutWaiver.Code != http.StatusConflict || !strings.Contains(withoutWaiver.Body.String(), "explicit waiver") {
		t.Fatalf("unwaived status=%d body=%s", withoutWaiver.Code, withoutWaiver.Body.String())
	}
	created := request(http.MethodPost, "/api/v1/admin/release-candidates", adminToken, bytes.NewBufferString(`{"waive_warnings":["operations.restore"]}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var candidate struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &candidate); err != nil || candidate.ID == "" {
		t.Fatalf("candidate response=%s error=%v", created.Body.String(), err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE release_candidates SET event_checksum = repeat('0', 64) WHERE id = $1`, candidate.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("release candidate mutation err=%v", err)
	}
	listed := request(http.MethodGet, "/api/v1/admin/release-candidates", adminToken, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), candidate.ID) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
}
