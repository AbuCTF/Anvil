//go:build integration

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
)

func TestDynamicScoringEndToEnd(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 8, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	teamA, teamB, teamQA := uuid.New(), uuid.New(), uuid.New()
	userA, userB, userQA := uuid.New(), uuid.New(), uuid.New()
	challengeID, flagID := uuid.New(), uuid.New()
	slug := "dynamic-e2e-" + challengeID.String()
	exec := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM challenges WHERE id = $1`, challengeID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{userA, userB, userQA})
		_, _ = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = ANY($1)`, []uuid.UUID{teamA, teamB, teamQA})
		_, _ = db.Pool.Exec(ctx, `UPDATE platform_settings SET value = 'false'::jsonb WHERE key = 'teams_mode'`)
	}()

	exec(`INSERT INTO platform_settings (key, value) VALUES ('teams_mode', 'true'), ('economy_mode', 'false')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO teams (id, name, join_code) VALUES ($1, $2, $3), ($4, $5, $6), ($7, $8, $9)`,
		teamA, "dynamic-a-"+teamA.String(), "a-"+teamA.String(),
		teamB, "dynamic-b-"+teamB.String(), "b-"+teamB.String(),
		teamQA, "zz-dynamic-"+teamQA.String(), "q-"+teamQA.String())
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, $2, $3, 'user', 'active', $4),
		($5, $6, $7, 'user', 'active', $8),
		($9, $10, $11, 'user', 'active', $12)`,
		userA, "dynamic-a-"+userA.String(), userA.String()+"@example.test", teamA,
		userB, "dynamic-b-"+userB.String(), userB.String()+"@example.test", teamB,
		userQA, "dynamic-qa-"+userQA.String(), userQA.String()+"@example.test", teamQA)
	exec(`INSERT INTO challenges (id, name, slug, difficulty, status, container_image, base_points, total_flags, score_type, score_minimum, score_decay)
		VALUES ($1, $2, $3, 'medium', 'published', '', 500, 1, 'dynamic', 100, 4)`, challengeID, "Dynamic E2E "+challengeID.String(), slug)
	flagValue := "flag{" + uuid.NewString() + "}"
	flagHash := sha256.Sum256([]byte(flagValue))
	exec(`INSERT INTO flags (id, challenge_id, name, flag_hash, points, sort_order, flag_type) VALUES ($1, $2, 'Flag', $3, 500, 1, 'static')`, flagID, challengeID, hex.EncodeToString(flagHash[:]))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "dynamic-scoring-integration-secret"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	token := func(userID uuid.UUID) string {
		signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: userID, Role: "user", TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return signed
	}
	submit := func(userID uuid.UUID) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/challenges/"+slug+"/submit", strings.NewReader(`{"flag":"`+flagValue+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token(userID))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("submit status=%d body=%s", response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode submit: %v", err)
		}
		return body
	}

	if points := submit(userA)["points"]; points != float64(475) {
		t.Fatalf("first solve points=%v, want 475", points)
	}
	if points := submit(userB)["points"]; points != float64(400) {
		t.Fatalf("second solve points=%v, want 400", points)
	}
	if points := submit(userQA)["points"]; points != float64(400) {
		t.Fatalf("QA solve points=%v, want 400", points)
	}
	duplicate := submit(userA)
	if duplicate["already_solved"] != true || duplicate["points"] != float64(0) {
		t.Fatalf("duplicate response=%v", duplicate)
	}

	for _, item := range []struct {
		table string
		id    uuid.UUID
		want  int
	}{{"users", userA, 400}, {"users", userB, 400}, {"users", userQA, 400}, {"teams", teamA, 400}, {"teams", teamB, 400}, {"teams", teamQA, 400}} {
		var got int
		if err := db.Pool.QueryRow(ctx, `SELECT total_score FROM `+item.table+` WHERE id = $1`, item.id).Scan(&got); err != nil || got != item.want {
			t.Fatalf("%s %s score=%d error=%v, want %d", item.table, item.id, got, err, item.want)
		}
	}
	var totalSolves int
	if err := db.Pool.QueryRow(ctx, `SELECT total_solves FROM challenges WHERE id = $1`, challengeID).Scan(&totalSolves); err != nil || totalSolves != 2 {
		t.Fatalf("total_solves=%d error=%v, want 2", totalSolves, err)
	}
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/challenges/"+slug, nil)
	detailResponse := httptest.NewRecorder()
	router.ServeHTTP(detailResponse, detailRequest)
	var detail map[string]any
	if detailResponse.Code != http.StatusOK || json.Unmarshal(detailResponse.Body.Bytes(), &detail) != nil || detail["score_type"] != "dynamic" || detail["value"] != float64(400) {
		t.Fatalf("challenge detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
}
