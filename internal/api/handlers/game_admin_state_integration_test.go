//go:build integration

package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestFinalsAdminStateBootstrapAndPublicInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 2,
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
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'admin', 'active')`,
		adminID, "state"+suffix, "state"+suffix+"@example.test"); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM platform_settings WHERE key = 'game.state'`); err != nil {
		t.Fatalf("clear state: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM audit_log WHERE user_id = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM platform_settings WHERE key = 'game.state'`)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, adminID)
	}()

	cfg := &config.Config{Game: config.GameConfig{Enabled: true}}
	handler := NewGameAdminHandler(cfg, db, nil, zap.NewNop())
	setState := func(body string) int {
		response := httptest.NewRecorder()
		ginContext, _ := gin.CreateTestContext(response)
		ginContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/arena/state", bytes.NewBufferString(body))
		ginContext.Request.Header.Set("Content-Type", "application/json")
		ginContext.Set("user_id", adminID)
		handler.SetState(ginContext)
		return response.Code
	}
	if code := setState(`{"state":"invalid"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid state = %d, want 400", code)
	}
	if code := setState(`{"state":"paused"}`); code != http.StatusOK {
		t.Fatalf("bootstrap pause = %d, want 200", code)
	}

	infoResponse := httptest.NewRecorder()
	infoContext, _ := gin.CreateTestContext(infoResponse)
	infoContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	NewPlatformHandler(cfg, db, zap.NewNop()).GetInfo(infoContext)
	if infoResponse.Code != http.StatusOK || !strings.Contains(infoResponse.Body.String(), `"game_state":"paused"`) ||
		!strings.Contains(infoResponse.Body.String(), `"game_frozen":false`) {
		t.Fatalf("public info = %d %s", infoResponse.Code, infoResponse.Body.String())
	}
	if code := setState(`{"state":"running"}`); code != http.StatusOK {
		t.Fatalf("resume = %d, want 200", code)
	}

	var audits int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE user_id = $1 AND action = 'arena.state_changed'`, adminID,
	).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("state audits = %d err=%v, want 2", audits, err)
	}
}
