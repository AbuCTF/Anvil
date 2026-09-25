//go:build integration

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// kickoff phase marker, live economy values, teamless flag, and the solve toast's
// earned delta. destructive: truncates users/teams/challenges in a *_test database.
func TestKickoffAndEconomyDisplay(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	setWindow := func(start time.Time) {
		t.Helper()
		exec(`INSERT INTO platform_settings (key, value) VALUES ('event.start_at', to_jsonb($1::text)), ('event.end_at', to_jsonb($2::text))
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
			start.UTC().Format(time.RFC3339), start.Add(12*time.Hour).UTC().Format(time.RFC3339))
	}

	teamA := uuid.New()
	p1, p3 := uuid.New(), uuid.New()
	multi, cat := uuid.New(), uuid.New()
	h1 := sha256.Sum256([]byte("flag{part-one}"))
	h2 := sha256.Sum256([]byte("flag{part-two}"))

	exec(`TRUNCATE users, teams, challenges, categories CASCADE`)
	exec(`INSERT INTO platform_settings (key, value) VALUES ('economy_mode', 'true'), ('teams_mode', 'true')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO teams (id, name, join_code, total_score, koth_score) VALUES ($1, 'alpha', 'ja', 0, 0)`, teamA)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'p1', 'p1@x', 'user', 'active', $3), ($2, 'p3', 'p3@x', 'user', 'active', NULL)`, p1, p3, teamA)
	exec(`INSERT INTO categories (id, name, slug) VALUES ($1, 'web', 'web')`, cat)
	exec(`INSERT INTO challenges (id, name, slug, description, difficulty, category_id, status, container_image, total_flags, base_points)
		VALUES ($1, 'Deputy', 'deputy', 'BRIEF', 'medium', $2, 'published', '', 2, 1500)`, multi, cat)
	exec(`INSERT INTO flags (challenge_id, name, flag_hash, points, flag_type, sort_order) VALUES
		($1, 'part one', $2, 10, 'static', 1), ($1, 'part two', $3, 30, 'static', 2)`,
		multi, hex.EncodeToString(h1[:]), hex.EncodeToString(h2[:]))
	exec(`INSERT INTO economy_team_score (team_id, points, credits, grant_issued) VALUES ($1, 0, 4000, true)`, teamA)
	exec(`INSERT INTO economy_challenge_state (team_id, challenge_id, status, opened_at, expires_at)
		VALUES ($1, $2, 'open', NOW(), NOW() + INTERVAL '1 hour')`, teamA, multi)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "integration-secret-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	containerSvc, err := container.NewService(cfg.Container, zap.NewNop())
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, containerSvc, nil, nil, nil, nil, nil, zap.NewNop()).Router()

	call := func(method, path string, who *uuid.UUID, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if who != nil {
			s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
				UserID: *who, TokenType: "user",
				RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
			}).SignedString([]byte(cfg.JWT.Secret))
			req.Header.Set("Authorization", "Bearer "+s)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	near := func(what string, got any, want float64) {
		t.Helper()
		if g, ok := got.(float64); !ok || math.Abs(g-want) > 0.01 {
			t.Errorf("%s = %v, want %.3f", what, got, want)
		}
	}
	// medium band: ceiling x crowd decay, no wrong subs
	ec := cfg.Economy
	valueAt := func(crowd float64) float64 {
		return ec.Ceilings[1] * (ec.CrowdFloors[1] + (1-ec.CrowdFloors[1])*math.Pow(0.5, crowd/ec.CrowdHalflives[1]))
	}

	// pre-start: the list and detail say why they're empty
	setWindow(time.Now().Add(time.Hour))
	code, body := call("GET", "/api/v1/challenges", &p1, "")
	if code != 200 || body["phase"] != "scheduled" || len(body["challenges"].([]any)) != 0 {
		t.Errorf("pre-start list = %d %v", code, body)
	}
	code, body = call("GET", "/api/v1/challenges/deputy", &p1, "")
	if code != 404 || body["phase"] != "scheduled" {
		t.Errorf("pre-start detail = %d %v", code, body)
	}

	// live: tiles and detail carry the band value, not base_points
	setWindow(time.Now().Add(-time.Minute))
	code, body = call("GET", "/api/v1/challenges", nil, "")
	if code != 200 || body["phase"] != nil || len(body["challenges"].([]any)) != 1 {
		t.Fatalf("live list = %d %v", code, body)
	}
	near("fresh list value", body["challenges"].([]any)[0].(map[string]any)["value"], valueAt(0))

	code, body = call("GET", "/api/v1/challenges/deputy", &p3, "")
	eco, _ := body["economy"].(map[string]any)
	if code != 200 || eco == nil || eco["has_team"] != false {
		t.Errorf("teamless detail = %d %v", code, body)
	}
	code, body = call("GET", "/api/v1/challenges/deputy", &p1, "")
	eco, _ = body["economy"].(map[string]any)
	if code != 200 || eco == nil || eco["has_team"] != true || eco["share"] != nil || eco["earned"] != nil {
		t.Fatalf("opener detail = %d %v", code, body)
	}
	near("fresh detail value", eco["value"], valueAt(0))

	// capturing the 10/40 flag pays a quarter of the (now decayed) value
	code, body = call("POST", "/api/v1/challenges/deputy/submit", &p1, `{"flag":"flag{part-one}"}`)
	if code != 200 || body["correct"] != true {
		t.Fatalf("submit = %d %v", code, body)
	}
	if want := math.Round(valueAt(0.25) * 0.25); body["points"] != want {
		t.Errorf("submit points = %v, want earned delta %v (flag weight is 10)", body["points"], want)
	}
	code, body = call("GET", "/api/v1/challenges/deputy", &p1, "")
	eco, _ = body["economy"].(map[string]any)
	if code != 200 || eco == nil {
		t.Fatalf("holder detail = %d %v", code, body)
	}
	near("holder share", eco["share"], 0.25)
	near("holder earned", eco["earned"], valueAt(0.25)*0.25)
	near("decayed value", eco["value"], valueAt(0.25))
	_, body = call("GET", "/api/v1/challenges", &p3, "")
	near("decayed list value", body["challenges"].([]any)[0].(map[string]any)["value"], valueAt(0.25))
}
