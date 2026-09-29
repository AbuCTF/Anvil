//go:build integration

package api

import (
	"context"
	"encoding/json"
	"math"
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

// Market Pulse is intentionally asymmetric: the caller gets exact team state,
// while field activity is delayed, bucketed, and stripped of hidden QA teams.
// Destructive: truncates the configured *_test database.
func TestMarketPulsePrivacyAndFreeze(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}

	alpha, bravo, charlie, delta, hidden, staff := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	a, b, c, d, qa, admin, teamless := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	category := uuid.New()
	visible, future, draft, hill := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	visibleFlag, futureFlag, draftFlag, hillFlag := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	exec(`TRUNCATE users, teams, challenges, categories CASCADE`)
	exec(`INSERT INTO platform_settings (key, value) VALUES
		('teams_mode', 'true'), ('economy_mode', 'true'), ('market_pulse_enabled', 'true'),
		('scoreboard_frozen', 'false')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO teams (id, name, join_code) VALUES
		($1, 'Alpha', 'a'), ($2, 'Bravo', 'b'), ($3, 'Charlie', 'c'),
		($4, 'Delta', 'd'), ($5, 'zz-qa-pulse', 'qa'), ($6, 'Staff', 'staff')`,
		alpha, bravo, charlie, delta, hidden, staff)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'alpha', 'a@x', 'user', 'active', $8),
		($2, 'bravo', 'b@x', 'user', 'active', $9),
		($3, 'charlie', 'c@x', 'user', 'active', $10),
		($4, 'delta', 'd@x', 'user', 'active', $11),
		($5, 'qa', 'qa@x', 'user', 'active', $12),
		($6, 'admin', 'admin@x', 'admin', 'active', $13),
		($7, 'teamless', 'none@x', 'user', 'active', NULL)`,
		a, b, c, d, qa, admin, teamless, alpha, bravo, charlie, delta, hidden, staff)
	exec(`INSERT INTO categories (id, name, slug) VALUES ($1, 'Web', 'web')`, category)
	exec(`INSERT INTO challenges
		(id, name, slug, difficulty, category_id, status, container_image, base_points, release_date, arena_mode)
		VALUES
		($1, 'Visible', 'visible', 'medium', $5, 'published', '', 250, NOW() - INTERVAL '1 day', 'per_team'),
		($2, 'Future', 'future', 'easy', $5, 'published', '', 100, NOW() + INTERVAL '1 day', 'per_team'),
		($3, 'Draft', 'draft', 'easy', $5, 'draft', '', 100, NOW() - INTERVAL '1 day', 'per_team'),
		($4, 'Hill', 'hill', 'hard', $5, 'published', '', 500, NOW() - INTERVAL '1 day', 'shared')`,
		visible, future, draft, hill, category)
	exec(`INSERT INTO flags (id, challenge_id, name, flag_hash, points) VALUES
		($1, $5, 'flag', 'a', 250), ($2, $6, 'flag', 'b', 100),
		($3, $7, 'flag', 'c', 100), ($4, $8, 'flag', 'd', 500)`,
		visibleFlag, futureFlag, draftFlag, hillFlag, visible, future, draft, hill)
	exec(`INSERT INTO economy_team_score
		(team_id, points, credits, grant_issued, bailout_used, p2c_blocks) VALUES
		($1, 125, 275, true, false, 2), ($2, 0, 4000, true, false, 0)`, alpha, bravo)
	exec(`INSERT INTO economy_challenge_state
		(team_id, challenge_id, status, opened_at, expires_at, extensions_used, wrong_subs, current_value, frac)
		VALUES ($1, $2, 'open', NOW() - INTERVAL '1 hour', NOW() + INTERVAL '20 minutes', 0, 1, 90, 0)`,
		alpha, visible)

	old := time.Now().Add(-2 * time.Hour)
	exec(`INSERT INTO solves (user_id, challenge_id, flag_id, points_awarded, solved_at) VALUES
		($1, $4, $5, 250, $6), ($2, $4, $5, 250, $6), ($3, $4, $5, 250, $6)`,
		a, b, qa, visible, visibleFlag, old)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "pulse-integration-secret-32-bytes!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	cfg.Economy.C2PRate = 0.015
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	token := func(uid uuid.UUID) string {
		signed, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: uid, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return signed
	}
	request := func(who *uuid.UUID) (int, map[string]any, http.Header) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/economy/pulse", nil)
		if who != nil {
			req.Header.Set("Authorization", "Bearer "+token(*who))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Header()
	}
	publicInfo := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("platform info = %d: %s", w.Code, w.Body.String())
		}
		out := map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode platform info: %v", err)
		}
		return out
	}

	exec(`UPDATE platform_settings SET value = 'false'::jsonb WHERE key = 'market_pulse_enabled'`)
	if publicInfo()["market_pulse_enabled"] != false {
		t.Fatalf("public info exposed the wrong disabled state")
	}
	if code, _, _ := request(&a); code != http.StatusNotFound {
		t.Fatalf("disabled pulse = %d, want 404", code)
	}
	exec(`UPDATE platform_settings SET value = 'true'::jsonb WHERE key = 'market_pulse_enabled'`)
	if publicInfo()["market_pulse_enabled"] != true {
		t.Fatalf("public info exposed the wrong enabled state")
	}

	if code, _, _ := request(nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous pulse = %d, want 401", code)
	}
	if code, _, _ := request(&teamless); code != http.StatusForbidden {
		t.Fatalf("teamless pulse = %d, want 403", code)
	}

	code, body, header := request(&a)
	if code != http.StatusOK {
		t.Fatalf("alpha pulse = %d: %v", code, body)
	}
	if header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("cache control = %q", header.Get("Cache-Control"))
	}
	teamBody := body["team"].(map[string]any)
	if teamBody["credits"] != 275.0 || teamBody["points"] != 125.0 || teamBody["open_slots_used"] != 1.0 {
		t.Errorf("exact team state = %v", teamBody)
	}
	if math.Abs(teamBody["next_p2c_rate"].(float64)-0.49) > 1e-9 || math.Abs(teamBody["settlement_exposure"].(float64)-4.125) > 1e-9 {
		t.Errorf("team projections = rate %v exposure %v", teamBody["next_p2c_rate"], teamBody["settlement_exposure"])
	}
	field := body["field"].([]any)
	if len(field) != 1 {
		t.Fatalf("field = %v, want only released ordinary challenge", field)
	}
	visibleBody := field[0].(map[string]any)
	if visibleBody["slug"] != "visible" || visibleBody["solve_band"] != "insufficient_sample" {
		t.Errorf("two public + hidden QA captures = %v, want sub-threshold", visibleBody)
	}

	// A third public team crosses the anonymity threshold. The QA capture never
	// contributes to the band, even though it is an active ordinary user.
	exec(`INSERT INTO solves (user_id, challenge_id, flag_id, points_awarded, solved_at)
		VALUES ($1, $2, $3, 250, $4)`, c, visible, visibleFlag, old)
	code, body, _ = request(&b)
	if code != http.StatusOK {
		t.Fatalf("bravo pulse = %d: %v", code, body)
	}
	visibleBody = body["field"].([]any)[0].(map[string]any)
	if visibleBody["solve_band"] != "few" {
		t.Errorf("three public captures band = %v, want few", visibleBody["solve_band"])
	}
	bravoTeam := body["team"].(map[string]any)
	if bravoTeam["credits"] != 4000.0 || bravoTeam["points"] != 0.0 {
		t.Errorf("field is shared but team state must be caller-specific: %v", bravoTeam)
	}

	exec(`UPDATE platform_settings SET value = 'true'::jsonb WHERE key = 'scoreboard_frozen'`)
	code, body, _ = request(&a)
	if code != http.StatusOK {
		t.Fatalf("frozen pulse = %d: %v", code, body)
	}
	policy := body["policy"].(map[string]any)
	if policy["field_hidden"] != true || len(body["field"].([]any)) != 0 {
		t.Errorf("frozen pulse leaked field: %v", body)
	}
	if body["team"].(map[string]any)["credits"] != 275.0 {
		t.Errorf("freeze should retain exact own state: %v", body["team"])
	}
}
