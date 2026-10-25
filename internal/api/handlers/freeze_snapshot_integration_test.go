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

func TestGlobalFreezeUsesSnapshotAndKeepsAdminLive(t *testing.T) {
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
	team, missingTeam, player, missingPlayer, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var tick int
	if err := db.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(tick_number), 0) + 1000 FROM game_ticks`).Scan(&tick); err != nil {
		t.Fatalf("choose tick: %v", err)
	}
	freezeAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	settings := []string{"teams_mode", "scoreboard_frozen", "event.freeze_at"}
	oldSettings := map[string][]byte{}
	rows, err := db.Pool.Query(ctx, `SELECT key, value::text FROM platform_settings WHERE key = ANY($1)`, settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan setting: %v", err)
		}
		oldSettings[key] = append([]byte(nil), value...)
	}
	rows.Close()
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_score_snapshots WHERE team_id IN ($1, $2) AND tick_number IN ($3, $4)`, team, missingTeam, tick, tick+1)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_ticks WHERE tick_number IN ($1, $2)`, tick, tick+1)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2, $3)`, player, missingPlayer, admin)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_teams WHERE id IN ($1, $2)`, team, missingTeam)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id IN ($1, $2)`, team, missingTeam)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM platform_settings WHERE key = ANY($1)`, settings)
		for key, value := range oldSettings {
			_, _ = db.Pool.Exec(ctx, `INSERT INTO platform_settings (key, value) VALUES ($1, $2::jsonb)`, key, string(value))
		}
	}()

	exec(`INSERT INTO platform_settings (key, value) VALUES
	      ('teams_mode', 'true'), ('scoreboard_frozen', 'true'),
	      ('event.freeze_at', to_jsonb($1::text))
	      ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, freezeAt.Format(time.RFC3339))
	exec(`INSERT INTO teams (id, name, join_code, total_score) VALUES
	      ($1, $2, $3, 999), ($4, $5, $6, 888)`,
		team, "freeze-"+sfx, "freeze-join-"+sfx,
		missingTeam, "missing-"+sfx, "missing-join-"+sfx)
	exec(`INSERT INTO game_teams (id, name, slug) VALUES ($1, $2, $3), ($4, $5, $6)`,
		team, "freeze-"+sfx, "freeze-"+sfx,
		missingTeam, "missing-"+sfx, "missing-"+sfx)
	exec(`INSERT INTO users (id, username, email, role, status, team_id, total_score) VALUES
	      ($1, $2, $3, 'user', 'active', $4, 777),
	      ($5, $6, $7, 'user', 'active', $8, 666),
	      ($9, $10, $11, 'admin', 'active', $4, 555)`,
		player, "freeze"+sfx, "freeze"+sfx+"@example.test", team,
		missingPlayer, "missing"+sfx, "missing"+sfx+"@example.test", missingTeam,
		admin, "admin"+sfx, "admin"+sfx+"@example.test")
	exec(`INSERT INTO game_ticks (tick_number, started_at, ended_at, status) VALUES
	      ($1, $3::timestamptz - interval '1 minute', $3::timestamptz - interval '30 seconds', 'closed'),
	      ($2, $3::timestamptz + interval '1 minute', NULL, 'running')`, tick, tick+1, freezeAt)
	exec(`INSERT INTO game_score_snapshots (tick_number, team_id, total, rank)
	      VALUES ($1, $2, 123.456, 7)`, tick, team)

	cfg := &config.Config{}
	cfg.Platform.ScoreboardEnabled = true
	cfg.Platform.ScoreboardPublic = true
	scoreboard := NewScoreboardHandler(cfg, db, zap.NewNop())
	userHandler := NewUserHandler(cfg, db, zap.NewNop())
	teamsHandler := NewTeamsHandler(cfg, db, zap.NewNop())
	call := func(target string, params gin.Params, userID *uuid.UUID, role string, handle gin.HandlerFunc) map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		gc, _ := gin.CreateTestContext(recorder)
		gc.Request = httptest.NewRequest(http.MethodGet, target, nil)
		gc.Params = params
		if userID != nil {
			gc.Set("user_id", *userID)
		}
		if role != "" {
			gc.Set("role", role)
		}
		handle(gc)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status %d: %s", target, recorder.Code, recorder.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", target, err)
		}
		return payload
	}

	teamParams := gin.Params{{Key: "id", Value: team.String()}}
	frozenTeam := call("/teams/"+team.String(), teamParams, nil, "", scoreboard.TeamProfile)
	frozenTeamBody := frozenTeam["team"].(map[string]any)
	if frozenTeamBody["total_score"] != 123.456 || frozenTeamBody["global_rank"] != float64(7) {
		t.Fatalf("frozen team = %#v", frozenTeamBody)
	}
	adminTeam := call("/teams/"+team.String(), teamParams, &admin, "admin", scoreboard.TeamProfile)
	if adminTeam["team"].(map[string]any)["total_score"] != float64(999) {
		t.Fatalf("admin team was not live: %#v", adminTeam)
	}

	missingParams := gin.Params{{Key: "id", Value: missingTeam.String()}}
	missing := call("/teams/"+missingTeam.String(), missingParams, nil, "", scoreboard.TeamProfile)["team"].(map[string]any)
	if _, ok := missing["total_score"]; ok {
		t.Fatalf("missing snapshot fabricated score: %#v", missing)
	}

	profileParams := gin.Params{{Key: "username", Value: "freeze" + sfx}}
	frozenProfile := call("/profile/freeze"+sfx, profileParams, nil, "", scoreboard.Profile)
	profileUser := frozenProfile["user"].(map[string]any)
	if _, ok := profileUser["total_score"]; ok || len(frozenProfile["solves"].([]any)) != 0 {
		t.Fatalf("public profile leaked progress: %#v", frozenProfile)
	}
	adminProfile := call("/profile/freeze"+sfx, profileParams, &admin, "admin", scoreboard.Profile)
	if adminProfile["user"].(map[string]any)["total_score"] != float64(777) {
		t.Fatalf("admin profile was not live: %#v", adminProfile)
	}

	frozenMe := call("/user/me", nil, &player, "user", userHandler.GetProfile)
	if frozenMe["total_score"] != 123.456 || frozenMe["rank"] != float64(7) {
		t.Fatalf("frozen user profile = %#v", frozenMe)
	}
	frozenRank := call("/user/me/rank", nil, &player, "user", userHandler.GetRank)
	if frozenRank["rank"] != float64(7) || frozenRank["frozen"] != true {
		t.Fatalf("frozen user rank = %#v", frozenRank)
	}
	frozenStats := call("/user/me/stats", nil, &player, "user", userHandler.GetStats)
	if frozenStats["total_score"] != 123.456 || frozenStats["rank"] != float64(7) {
		t.Fatalf("frozen user stats = %#v", frozenStats)
	}
	frozenMine := call("/teams/me", nil, &player, "user", teamsHandler.GetMine)
	mineTeam := frozenMine["team"].(map[string]any)
	if mineTeam["total_score"] != 123.456 || mineTeam["global_rank"] != float64(7) {
		t.Fatalf("frozen own team = %#v", frozenMine)
	}
	for _, member := range mineTeam["members"].([]any) {
		if _, ok := member.(map[string]any)["solve_count"]; ok {
			t.Fatalf("frozen own team leaked member solve_count: %#v", frozenMine)
		}
	}
	adminMe := call("/user/me", nil, &admin, "admin", userHandler.GetProfile)
	if adminMe["total_score"] != float64(999) {
		t.Fatalf("admin user profile was not live: %#v", adminMe)
	}
}
