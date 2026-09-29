//go:build integration

package api

import (
	"archive/zip"
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

func TestDataPortabilityPreviewExportAndApply(t *testing.T) {
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
	`, adminID, "data-admin-"+adminID.String(), adminID.String()+"@x", userID, "data-user-"+userID.String(), userID.String()+"@x"); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM data_import_jobs WHERE created_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE user_id = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM categories WHERE slug IN ('portable-alpha', 'portable-beta', 'portable-formula')`)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, adminID, userID)
	}()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO categories (slug, name, sort_order) VALUES ('portable-formula', '=2+2', 0)`); err != nil {
		t.Fatalf("seed category: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "data-portability-integration-secret!"
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
	request := func(method, path, auth string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	if got := request(http.MethodGet, "/api/v1/admin/data/summary", userToken, nil).Code; got != http.StatusForbidden {
		t.Fatalf("participant summary status=%d", got)
	}
	bundle := request(http.MethodGet, "/api/v1/admin/data/export?format=bundle&entities=settings,categories", adminToken, nil)
	if bundle.Code != http.StatusOK || bundle.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("bundle status=%d type=%q body=%s", bundle.Code, bundle.Header().Get("Content-Type"), bundle.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle.Body.Bytes()), int64(bundle.Body.Len()))
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	contents := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, _ := io.ReadAll(reader)
		_ = reader.Close()
		contents[file.Name] = string(data)
	}
	if !strings.Contains(contents["manifest.json"], `"secrets": "excluded"`) || strings.Contains(contents["data/settings.json"], "koth_reset_secret") {
		t.Fatalf("unsafe manifest/settings export")
	}
	if !strings.Contains(contents["data/categories.csv"], "'=2+2") {
		t.Fatalf("CSV formula was not neutralized: %s", contents["data/categories.csv"])
	}

	csvContent := "slug,name,description,color,icon,sort_order\nportable-alpha,Alpha,,,mdi:flag,1\nportable-beta,Beta,,,mdi:flag,2\n"
	previewBody, _ := json.Marshal(map[string]any{"entity": "categories", "format": "csv", "mode": "create", "source_name": "categories.csv", "content": csvContent})
	preview := request(http.MethodPost, "/api/v1/admin/data/imports/preview", adminToken, bytes.NewReader(previewBody))
	if preview.Code != http.StatusCreated {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var first struct {
		JobID    string `json:"job_id"`
		Checksum string `json:"checksum"`
		Plan     struct {
			Create int           `json:"create"`
			Errors []interface{} `json:"errors"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &first); err != nil || first.Plan.Create != 2 || len(first.Plan.Errors) != 0 {
		t.Fatalf("preview response=%s error=%v", preview.Body.String(), err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO categories (slug, name) VALUES ('portable-alpha', 'Changed after preview')`); err != nil {
		t.Fatalf("mutate after preview: %v", err)
	}
	applyBody := strings.NewReader(`{"checksum":"` + first.Checksum + `"}`)
	stale := request(http.MethodPost, "/api/v1/admin/data/imports/"+first.JobID+"/apply", adminToken, applyBody)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "data changed after preview") {
		t.Fatalf("stale apply status=%d body=%s", stale.Code, stale.Body.String())
	}

	preview = request(http.MethodPost, "/api/v1/admin/data/imports/preview", adminToken, bytes.NewReader(previewBody))
	if preview.Code != http.StatusCreated {
		t.Fatalf("second preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var second struct {
		JobID    string `json:"job_id"`
		Checksum string `json:"checksum"`
		Plan     struct {
			Create int `json:"create"`
			Skip   int `json:"skip"`
		} `json:"plan"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &second)
	if second.Plan.Create != 1 || second.Plan.Skip != 1 {
		t.Fatalf("second plan=%s", preview.Body.String())
	}
	applyJSON := `{"checksum":"` + second.Checksum + `"}`
	applied := request(http.MethodPost, "/api/v1/admin/data/imports/"+second.JobID+"/apply", adminToken, strings.NewReader(applyJSON))
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), `"created":1`) {
		t.Fatalf("apply status=%d body=%s", applied.Code, applied.Body.String())
	}
	replayed := request(http.MethodPost, "/api/v1/admin/data/imports/"+second.JobID+"/apply", adminToken, strings.NewReader(applyJSON))
	if replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay status=%d header=%q body=%s", replayed.Code, replayed.Header().Get("Idempotent-Replay"), replayed.Body.String())
	}
	var betaCount int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM categories WHERE slug = 'portable-beta'`).Scan(&betaCount); err != nil || betaCount != 1 {
		t.Fatalf("beta count=%d error=%v", betaCount, err)
	}

	badBody, _ := json.Marshal(map[string]any{"entity": "users", "format": "csv", "mode": "merge", "source_name": "users.csv", "content": "username,email,role\n" + "data-admin-" + adminID.String() + ",admin@example.com,user\n"})
	bad := request(http.MethodPost, "/api/v1/admin/data/imports/preview", adminToken, bytes.NewReader(badBody))
	if bad.Code != http.StatusCreated || !strings.Contains(bad.Body.String(), "administrator accounts cannot be changed") {
		t.Fatalf("admin import guard status=%d body=%s", bad.Code, bad.Body.String())
	}
}
