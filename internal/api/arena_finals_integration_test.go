//go:build integration

package api

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

	"github.com/anvil-lab/anvil/internal/api/middleware"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// finals arena: attack-info in the enowars shape, submit gated on game.state, teams
// resolved through users.team_id, and service admin validation.
// destructive: truncates users/teams/game tables in a *_test database.
func TestFinalsArenaAttackInfoAndSubmitGate(t *testing.T) {
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
	setting := func(key, value string) {
		t.Helper()
		exec(`INSERT INTO platform_settings (key, value) VALUES ($1, $2::jsonb)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	}

	teamA, teamB, teamC := uuid.New(), uuid.New(), uuid.New()
	p1, admin := uuid.New(), uuid.New()
	svc := uuid.New()

	// back to the migration defaults; other api tests rely on these rows existing
	clearGame := func() {
		exec(`DELETE FROM platform_settings WHERE key = 'game.state'`)
		setting("event.start_at", `""`)
		setting("event.end_at", `""`)
		setting("arena_enabled", "false")
	}
	// truncating users would cascade into platform_settings (updated_by) and break the
	// rest of the suite, so this test owns uniquely named rows and removes only those
	p2 := uuid.New()
	sfx := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	exec(`TRUNCATE game_teams, game_services, game_ticks CASCADE`)
	clearGame()
	defer func() {
		exec(`TRUNCATE game_teams, game_services, game_ticks CASCADE`)
		exec(`DELETE FROM users WHERE id IN ($1, $2, $3)`, p1, p2, admin)
		exec(`DELETE FROM teams WHERE id IN ($1, $2, $3)`, teamA, teamB, teamC)
		clearGame() // the event window is shared with every other api test
	}()
	setting("arena_enabled", "true")
	exec(`INSERT INTO teams (id, name, join_code) VALUES ($1, 'alpha'||$4, 'ja'||$4), ($2, 'bravo'||$4, 'jb'||$4), ($3, 'charlie'||$4, 'jc'||$4)`,
		teamA, teamB, teamC, sfx)
	// a team is public (listed for attackers) only with an active non-staff member
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'p1'||$6, 'p1'||$6||'@x', 'user', 'active', $3), ($2, 'ad'||$6, 'ad'||$6||'@x', 'admin', 'active', NULL),
		($5, 'p2'||$6, 'p2'||$6||'@x', 'user', 'active', $4)`, p1, admin, teamA, teamB, p2, sfx)
	exec(`INSERT INTO game_teams (id, name, slug, vulnbox_ip, team_number) VALUES
		($1, 'alpha', 'alpha', '10.60.1.2', 1), ($2, 'bravo', 'bravo', '10.60.2.2', 2), ($3, 'charlie', 'charlie', '10.60.3.2', 3)`,
		teamA, teamB, teamC)
	exec(`INSERT INTO game_services (id, name, slug, category, port, checker_ref, flag_stores)
		VALUES ($1, 'Berth Board', 'berth', 'web', 8080, 'http://checker:9000', 2)`, svc)
	exec(`INSERT INTO game_ticks (tick_number, status) VALUES (1, 'closed'), (2, 'closed'), (3, 'closed')`)
	exec(`INSERT INTO game_flags (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at, attack_info) VALUES
		(3, $1, $3, 0, 'H7CTF{BRAVO0}', 3, 8, NOW(), '{"user":"u0"}'),
		(3, $1, $3, 1, 'H7CTF{BRAVO1}', 3, 8, NOW(), NULL),
		(1, $1, $3, 0, 'H7CTF{STALE}', 1, 2, NOW(), 'stale'),
		(3, $2, $3, 1, 'H7CTF{ALPHA1}', 3, 8, NOW(), 'a1')`, teamB, teamA, svc)
	// charlie was DOWN on everything last tick, so farms should skip it
	exec(`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status) VALUES
		(3, $1, $4, 'OK'), (3, $2, $4, 'FAULTY'), (3, $3, $4, 'DOWN')`, teamA, teamB, teamC, svc)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "integration-secret-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	cfg.Game.Enabled = true
	containerSvc, err := container.NewService(cfg.Container, zap.NewNop())
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, containerSvc, nil, nil, nil, nil, nil, zap.NewNop()).Router()

	token := func(uid uuid.UUID) string {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: uid, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				ID:        uuid.NewString(),
			},
		}).SignedString([]byte(cfg.JWT.Secret))
		return s
	}
	call := func(method, path string, who *uuid.UUID, body string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if who != nil {
			req.Header.Set("Authorization", "Bearer "+token(*who))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code, w.Body.Bytes()
	}

	code, raw := call("GET", "/api/v1/arena/attack-info", nil, "")
	if code != http.StatusOK {
		t.Fatalf("attack-info = %d %s", code, raw)
	}
	var info struct {
		AvailableTeams []string                                             `json:"availableTeams"`
		Services       map[string]map[string]map[string]map[string][]string `json:"services"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("decode attack-info: %v\n%s", err, raw)
	}
	if strings.Join(info.AvailableTeams, ",") != "10.60.1.2,10.60.2.2" {
		t.Errorf("availableTeams = %v, want alpha and bravo (charlie all DOWN)", info.AvailableTeams)
	}
	berth := info.Services["berth"]
	if got := berth["10.60.2.2"]["3"]["0"]; len(got) != 1 || got[0] != `{"user":"u0"}` {
		t.Errorf("bravo tick 3 variant 0 = %v", got)
	}
	if _, ok := berth["10.60.2.2"]["3"]["1"]; ok {
		t.Error("a flag without attack info was published")
	}
	if _, ok := berth["10.60.2.2"]["1"]; ok {
		t.Error("an expired flag was published")
	}
	if got := berth["10.60.1.2"]["3"]["1"]; len(got) != 1 || got[0] != "a1" {
		t.Errorf("alpha tick 3 variant 1 = %v", got)
	}

	submit := func(flag string) (int, string) {
		t.Helper()
		code, raw := call("POST", "/api/v1/arena/submit", &p1, `{"flag":"`+flag+`"}`)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		msg, _ := out["message"].(string)
		if s, ok := out["status"].(string); ok && msg == "" {
			msg = s
		}
		return code, msg
	}
	// legacy mode: no game.state, submit works as before; p1 resolves via users.team_id
	if code, msg := submit("H7CTF{BRAVO0}"); code != http.StatusOK || msg != "accepted" {
		t.Fatalf("legacy submit = %d %q, want 200 accepted", code, msg)
	}
	setting("game.state", `"paused"`)
	if code, msg := submit("H7CTF{BRAVO1}"); code != http.StatusForbidden || msg != "game paused" {
		t.Errorf("paused submit = %d %q", code, msg)
	}
	setting("game.state", `"running"`)
	setting("event.end_at", `"`+time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)+`"`)
	if code, msg := submit("H7CTF{BRAVO1}"); code != http.StatusForbidden || msg != "game over" {
		t.Errorf("post-end submit = %d %q", code, msg)
	}
	setting("event.end_at", `"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"`)
	if code, msg := submit("H7CTF{BRAVO1}"); code != http.StatusOK || msg != "accepted" {
		t.Errorf("live submit = %d %q, want 200 accepted", code, msg)
	}
	setting("game.state", `"stopped"`)
	if code, msg := submit("H7CTF{ALPHA1}"); code != http.StatusForbidden || msg != "game over" {
		t.Errorf("stopped submit = %d %q", code, msg)
	}

	// service admin: enowars checkers may carry variants, exec checkers may not
	ok, _ := call("POST", "/api/v1/admin/arena/services", &admin,
		`{"name":"Tide Pay","category":"crypto","port":9100,"checker_ref":"http://tidepay-checker:9000","flag_stores":2,"noise_variants":1,"havoc_variants":2,"release_at":"2026-10-24T13:30:00Z"}`)
	if ok != http.StatusCreated {
		t.Errorf("create eno service = %d", ok)
	}
	bad, _ := call("POST", "/api/v1/admin/arena/services", &admin,
		`{"name":"Exec Two","category":"web","port":9200,"checker_ref":"/opt/checkers/x","flag_stores":2}`)
	if bad != http.StatusBadRequest {
		t.Errorf("exec checker with 2 stores = %d, want 400", bad)
	}
	var stores, noise, havoc int
	var release *time.Time
	if err := db.Pool.QueryRow(ctx,
		`SELECT flag_stores, noise_variants, havoc_variants, release_at FROM game_services WHERE slug = 'tide-pay'`).
		Scan(&stores, &noise, &havoc, &release); err != nil {
		t.Fatalf("read created service: %v", err)
	}
	if stores != 2 || noise != 1 || havoc != 2 || release == nil {
		t.Errorf("created service = stores %d noise %d havoc %d release %v", stores, noise, havoc, release)
	}
}
