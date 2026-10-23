//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// the admin arena lists carry what the ops page needs: team number, member count,
// an 8-char token prefix (never the token), service schedule and hill windows.
func TestAdminArenaListsCarryOpsFields(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sfx := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	team, svc, hill, u1, u2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	token := strings.Repeat("ab", 32)
	exec(`INSERT INTO teams (id, name, join_code) VALUES ($1, $2, $3)`, team, "ops-"+sfx, "ops-join-"+sfx)
	exec(`INSERT INTO game_teams (id, name, slug, team_number, vulnbox_ip, token) VALUES ($1, $2, $3, 211, '10.60.211.2', $4)`,
		team, "ops-"+sfx, "ops-"+sfx, token)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES ($1, $2, $3, 'user', 'active', $4)`,
		u1, "ops1"+sfx, "ops1"+sfx+"@example.test", team)
	exec(`INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'user', 'active')`,
		u2, "ops2"+sfx, "ops2"+sfx+"@example.test")
	exec(`INSERT INTO game_team_members (team_id, user_id) VALUES ($1, $2), ($1, $3)`, team, u1, u2)
	release := time.Now().Add(6 * time.Hour).UTC().Truncate(time.Second)
	exec(`INSERT INTO game_services (id, name, slug, category, port, checker_ref, release_at, advisory_at, advisories)
	      VALUES ($1, $2, $3, 'web', 7009, 'http://c:9000', $4, $4, '{"0":"line"}')`, svc, "Ops "+sfx, "ops-"+sfx, release)
	exec(`INSERT INTO game_koth_hills (id, name, slug, port) VALUES ($1, $2, $3, 7199)`, hill, "Ops "+sfx, "ops-"+sfx)
	exec(`INSERT INTO game_koth_windows (hill_id, opens_at, closes_at) VALUES ($1, $2, $2::timestamptz + interval '2 hours')`, hill, release)
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_hills WHERE id = $1`, hill)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_services WHERE id = $1`, svc)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_team_members WHERE team_id = $1`, team)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, u1, u2)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_teams WHERE id = $1`, team)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, team)
	}()

	handler := NewGameAdminHandler(&config.Config{}, db, nil, zap.NewNop())
	call := func(handle gin.HandlerFunc) map[string][]map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		gc, _ := gin.CreateTestContext(rec)
		gc.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		handle(gc)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), token) {
			t.Fatalf("full team token leaked: %s", rec.Body.String())
		}
		out := map[string][]map[string]any{}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	find := func(rows []map[string]any, id uuid.UUID) map[string]any {
		t.Helper()
		for _, row := range rows {
			if row["id"] == id.String() {
				return row
			}
		}
		t.Fatalf("row %s missing", id)
		return nil
	}

	row := find(call(handler.ListTeams)["teams"], team)
	if row["team_number"] != float64(211) || row["token_prefix"] != token[:8] || row["member_count"] != float64(2) {
		t.Fatalf("team row = %#v", row)
	}
	row = find(call(handler.ListServices)["services"], svc)
	if row["release_at"] == nil || row["advisory_at"] == nil || row["advisories"].(map[string]any)["0"] != "line" {
		t.Fatalf("service row = %#v", row)
	}
	row = find(call(handler.ListHills)["hills"], hill)
	windows, _ := row["windows"].([]any)
	if len(windows) != 1 || windows[0].(map[string]any)["opens_at"] == nil {
		t.Fatalf("hill row = %#v", row)
	}
}
