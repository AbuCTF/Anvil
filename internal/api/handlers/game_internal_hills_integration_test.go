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

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestHillV2CredentialProvisioningIsSeparateAndIdempotent(t *testing.T) {
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
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	challengeID, hillID := uuid.New(), uuid.New()
	teams := []uuid.UUID{uuid.New(), uuid.New()}
	playerID := uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, query)
		}
	}
	exec(`INSERT INTO challenges (id, name, slug, difficulty, status, arena_mode, container_image)
		VALUES ($1, $2, $3, 'hard', 'draft', 'shared', 'registry.invalid/hill@sha256:test')`,
		challengeID, "Hill "+suffix, "hill-"+suffix)
	for index, teamID := range teams {
		exec(`INSERT INTO teams (id, name, join_code) VALUES ($1, $2, $3)`, teamID,
			"hill-team-"+strconv.Itoa(index)+suffix, "join-"+strconv.Itoa(index)+suffix)
		exec(`INSERT INTO game_teams (id, name, slug, status, is_nop) VALUES ($1, $2, $3, 'active', FALSE)`,
			teamID, "Hill Team "+strconv.Itoa(index), "hill-team-"+strconv.Itoa(index)+suffix)
	}
	exec(`INSERT INTO users (id, username, email, role, status, team_id)
		VALUES ($1, $2, $3, 'admin', 'active', $4)`, playerID, "hill-player-"+suffix,
		"hill-player-"+suffix+"@example.test", teams[0])
	exec(`INSERT INTO game_koth_hills
		(id, name, slug, challenge_id, base_url, reset_secret, controller_protocol, enabled)
		VALUES ($1, 'Current Affairs', 'current-affairs', $2, 'https://hill.internal', 'reset-only-secret', 'hill-v2-http', TRUE)`,
		hillID, challengeID)
	defer func() {
		exec(`DELETE FROM game_koth_hills WHERE id = $1`, hillID)
		exec(`DELETE FROM users WHERE id = $1`, playerID)
		exec(`DELETE FROM game_teams WHERE id = ANY($1)`, teams)
		exec(`DELETE FROM teams WHERE id = ANY($1)`, teams)
		exec(`DELETE FROM challenges WHERE id = $1`, challengeID)
	}()

	handler := NewGameInternalHandler(&config.Config{}, db, zap.NewNop())
	call := func() (int, http.Header, []hillTeamCredential, string) {
		t.Helper()
		response := httptest.NewRecorder()
		ginContext, _ := gin.CreateTestContext(response)
		ginContext.Request = httptest.NewRequest(http.MethodPost, "/internal/arena/hills/"+hillID.String()+"/credentials", nil)
		ginContext.Params = gin.Params{{Key: "id", Value: hillID.String()}}
		handler.HillCredentials(ginContext)
		var payload struct {
			ResetToken string               `json:"reset_token"`
			Teams      []hillTeamCredential `json:"teams"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v body=%s", err, response.Body.String())
		}
		return response.Code, response.Header(), payload.Teams, payload.ResetToken
	}

	code, headers, first, reset := call()
	if code != http.StatusOK || headers.Get("Cache-Control") != "no-store" || reset != "reset-only-secret" || len(first) != 2 {
		t.Fatalf("first provision = %d reset=%q teams=%v headers=%v", code, reset, first, headers)
	}
	_, _, second, secondReset := call()
	if secondReset != reset || len(second) != len(first) {
		t.Fatalf("second provision = reset %q teams=%v", secondReset, second)
	}
	for index := range first {
		if first[index] != second[index] || first[index].BearerToken == reset || first[index].Holder == "" || len(first[index].Holder) > 32 {
			t.Fatalf("credential %d not separate/idempotent: first=%+v second=%+v", index, first[index], second[index])
		}
	}
	var legacyValues int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM koth_entries
		 WHERE challenge_id = $1 AND (token IS NOT NULL OR rpc_secret IS NOT NULL)`, challengeID).Scan(&legacyValues); err != nil || legacyValues != 0 {
		t.Fatalf("legacy credentials reused: count=%d err=%v", legacyValues, err)
	}

	response := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(response)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/challenges/hill-"+suffix+"/koth-enter", nil)
	ginContext.Params = gin.Params{{Key: "slug", Value: "hill-" + suffix}}
	ginContext.Set("user_id", playerID)
	ginContext.Set("role", "admin")
	NewChallengeHandler(&config.Config{}, db, nil, nil, nil, zap.NewNop()).EnterKoth(ginContext)
	var entry map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &entry); err != nil {
		t.Fatalf("decode team entry: %v body=%s", err, response.Body.String())
	}
	var expectedBearer string
	if err := db.Pool.QueryRow(ctx,
		`SELECT bearer_token FROM koth_entries WHERE team_id = $1 AND challenge_id = $2`, teams[0], challengeID).
		Scan(&expectedBearer); err != nil {
		t.Fatalf("load expected bearer: %v", err)
	}
	if response.Code != http.StatusOK || entry["protocol"] != "hill-v2-http" ||
		entry["bearer_token"] != expectedBearer || entry["koth_token"] != nil {
		t.Fatalf("team entry = %d %v", response.Code, entry)
	}
}
