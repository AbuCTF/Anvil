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
		setting("event.freeze_at", `""`)
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
		clearGame() // game.state records the admin that changed it
		exec(`DELETE FROM audit_log WHERE user_id IN ($1, $2, $3)`, p1, p2, admin)
		exec(`DELETE FROM users WHERE id IN ($1, $2, $3)`, p1, p2, admin)
		exec(`DELETE FROM teams WHERE id IN ($1, $2, $3)`, teamA, teamB, teamC)
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
	// the nop has no members, yet attackers must see it: it is the exploit test target
	nopTeam := uuid.New()
	exec(`INSERT INTO game_teams (id, name, slug, vulnbox_ip, team_number, is_nop) VALUES ($1, 'nop'||$2, 'nop'||$2, '10.60.16.2', 16, TRUE)`, nopTeam, sfx)
	exec(`INSERT INTO game_flags (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at, attack_info) VALUES
		(3, $1, $2, 0, 'H7CTF{NOP0}', 3, 8, NOW(), 'n0')`, nopTeam, svc)
	exec(`INSERT INTO game_flags (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at, attack_info) VALUES
		(3, $1, $3, 0, 'H7CTF{BRAVO0}', 3, 8, NOW(), '{"user":"u0"}'),
		(3, $1, $3, 1, 'H7CTF{BRAVO1}', 3, 8, NOW(), NULL),
		(1, $1, $3, 0, 'H7CTF{STALE}', 1, 2, NOW(), 'stale'),
		(3, $2, $3, 1, 'H7CTF{ALPHA1}', 3, 8, NOW(), 'a1')`, teamB, teamA, svc)
	// charlie was DOWN on everything last tick, so farms should skip it
	exec(`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status) VALUES
		(1, $1, $4, 'OK'), (1, $2, $4, 'FAULTY'), (1, $3, $4, 'DOWN'),
		(3, $1, $4, 'OK'), (3, $2, $4, 'FAULTY'), (3, $3, $4, 'DOWN')`, teamA, teamB, teamC, svc)
	exec(`UPDATE game_services SET advisories = '{"0":"delayed-one"}'::jsonb WHERE id = $1`, svc)
	exec(`INSERT INTO game_events (kind, tick_number, service_id, store_index) VALUES
		('advisory', 1, $1, 0), ('advisory', 3, $1, 0)`, svc)

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

	setting("arena_enabled", "false")
	for _, path := range []string{"/api/v1/arena/me", "/api/v1/arena/me/status", "/api/v1/arena/me/alerts"} {
		if code, _ := call("GET", path, &p1, ""); code != http.StatusNotFound {
			t.Fatalf("pre-release %s = %d, want 404", path, code)
		}
	}
	setting("arena_enabled", "true")

	// a team with a vulnbox address must read back its own identity (inet -> text)
	if code, raw := call("GET", "/api/v1/arena/me", &p1, ""); code != http.StatusOK || !strings.Contains(string(raw), `"vulnbox_ip":"10.60.1.2"`) {
		t.Fatalf("arena/me = %d %s", code, raw)
	}

	for _, endpoint := range []struct {
		method string
		path   string
		who    uuid.UUID
	}{
		{"GET", "/api/v1/arena/me/pcaps", p1},
		{"POST", "/api/v1/arena/me/vulnbox/reboot", p1},
		{"POST", "/api/v1/arena/me/vulnbox/reset", p1},
		{"GET", "/api/v1/arena/me/vulnbox/reset", p1},
		{"POST", "/api/v1/admin/arena/teams/" + teamA.String() + "/pcap", admin},
		{"POST", "/api/v1/admin/arena/teams/" + teamA.String() + "/vulnbox-reset", admin},
		{"POST", "/api/v1/admin/arena/teams/" + teamA.String() + "/vulnbox-reboot", admin},
	} {
		if code, _ := call(endpoint.method, endpoint.path, &endpoint.who, ""); code != http.StatusNotFound {
			t.Fatalf("removed endpoint %s %s = %d, want 404", endpoint.method, endpoint.path, code)
		}
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
	if strings.Join(info.AvailableTeams, ",") != "10.60.1.2,10.60.2.2,10.60.16.2" {
		t.Errorf("availableTeams = %v, want alpha, bravo and the nop (charlie all DOWN)", info.AvailableTeams)
	}
	if got := info.Services["berth"]["10.60.16.2"]["3"]["0"]; len(got) != 1 || got[0] != "n0" {
		t.Errorf("nop tick 3 variant 0 = %v", got)
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
	if code, _ := call("PUT", "/api/v1/admin/arena/state", &admin, `{"state":"invalid"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid admin state = %d, want 400", code)
	}
	if code, raw := call("PUT", "/api/v1/admin/arena/state", &admin, `{"state":"paused"}`); code != http.StatusOK {
		t.Fatalf("bootstrap paused state = %d %s", code, raw)
	}
	if code, raw := call("GET", "/api/v1/info", nil, ""); code != http.StatusOK ||
		!strings.Contains(string(raw), `"game_state":"paused"`) || !strings.Contains(string(raw), `"game_frozen":false`) {
		t.Fatalf("platform info finals state = %d %s", code, raw)
	}
	var stateAudits int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'arena.state_changed' AND user_id = $1`, admin,
	).Scan(&stateAudits); err != nil || stateAudits != 1 {
		t.Fatalf("state transition audit rows = %d err=%v, want 1", stateAudits, err)
	}
	if code, msg := submit("H7CTF{BRAVO1}"); code != http.StatusForbidden || msg != "game paused" {
		t.Errorf("paused submit = %d %q", code, msg)
	}
	if code, raw := call("PUT", "/api/v1/admin/arena/state", &admin, `{"state":"running"}`); code != http.StatusOK {
		t.Fatalf("resume state = %d %s", code, raw)
	}

	// A frozen finals board must not leak its current rows through the direct API.
	// Organizers retain a private, no-store view for the reveal console.
	exec(`INSERT INTO game_standings (team_id, attack, defense, sla, koth, ad, total, rank)
		VALUES ($1, 12, 8, 6, 4, 26, 30, 1)`, teamA)
	if code, raw := call("GET", "/api/v1/arena/scoreboard", nil, ""); code != http.StatusOK || !strings.Contains(string(raw), `"team_name":"alpha"`) {
		t.Fatalf("live public finals board = %d %s", code, raw)
	}
	setting("event.freeze_at", `"2020-01-01T00:00:00Z"`)
	for _, viewer := range []*uuid.UUID{nil, &p1} {
		if code, raw := call("GET", "/api/v1/arena/scoreboard", viewer, ""); code != http.StatusOK || strings.Contains(string(raw), `"team_name"`) || !strings.Contains(string(raw), `"frozen":true`) {
			t.Fatalf("frozen player finals board = %d %s", code, raw)
		}
	}
	if code, raw := call("GET", "/api/v1/arena/scoreboard", &admin, ""); code != http.StatusOK || !strings.Contains(string(raw), `"team_name":"alpha"`) {
		t.Fatalf("frozen admin finals board = %d %s", code, raw)
	}
	// the side doors are blinded too: score history, koth points and matrix ranks
	for _, viewer := range []*uuid.UUID{nil, &p1} {
		if code, raw := call("GET", "/api/v1/arena/history", viewer, ""); code != http.StatusOK || !strings.Contains(string(raw), `"frozen":true`) || !strings.Contains(string(raw), `"series":[]`) {
			t.Fatalf("frozen history = %d %s", code, raw)
		}
		if code, raw := call("GET", "/api/v1/arena/services", viewer, ""); code != http.StatusOK || strings.Contains(string(raw), `"rank"`) {
			t.Fatalf("frozen services matrix = %d %s", code, raw)
		}
		if code, raw := call("GET", "/api/v1/arena/koth/scores", viewer, ""); code != http.StatusOK || !strings.Contains(string(raw), `"frozen":true`) {
			t.Fatalf("frozen koth scores = %d %s", code, raw)
		}
	}
	if code, raw := call("GET", "/api/v1/arena/services", &admin, ""); code != http.StatusOK || !strings.Contains(string(raw), `"rank":1`) {
		t.Fatalf("frozen admin services matrix = %d %s", code, raw)
	}
	setting("event.freeze_at", `""`)
	if code, raw := call("GET", "/api/v1/arena/services", &p1, ""); code != http.StatusOK || !strings.Contains(string(raw), `"rank":1`) {
		t.Fatalf("live services matrix = %d %s", code, raw)
	}

	// The War Room is staff-only: players and anonymous viewers get a plain 404.
	// For staff it is a full, delayed matrix. Tick 3 is current, so public view
	// tick 1 is visible and the tick-3 advisory must remain hidden.
	for _, viewer := range []*uuid.UUID{nil, &p1, &p2} {
		if code, raw := call("GET", "/api/v1/arena/war-room", viewer, ""); code != http.StatusNotFound || !strings.Contains(string(raw), `"not found"`) {
			t.Fatalf("player war room = %d %s, want 404", code, raw)
		}
	}
	type warCell struct {
		TeamID string `json:"team_id"`
		Status string `json:"status"`
		IsOwn  bool   `json:"is_own"`
	}
	type warEvent struct {
		Text string `json:"text"`
		Tick int    `json:"tick"`
	}
	type warPayload struct {
		Tick     int        `json:"tick"`
		ViewTick int        `json:"view_tick"`
		Teams    []any      `json:"teams"`
		Services []any      `json:"services"`
		Grid     []warCell  `json:"grid"`
		Events   []warEvent `json:"events"`
	}
	warCall := func(who uuid.UUID) (int, http.Header, warPayload) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/arena/war-room", nil)
		req.Header.Set("Authorization", "Bearer "+token(who))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var payload warPayload
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode war room: %v\n%s", err, w.Body.Bytes())
		}
		return w.Code, w.Header(), payload
	}
	code, headers, adminWar := warCall(admin)
	if code != http.StatusOK || headers.Get("Cache-Control") != "private, no-store" || headers.Get("X-Anvil-Cache") != "MISS" {
		t.Fatalf("admin war room = %d cache=%q/%q", code, headers.Get("Cache-Control"), headers.Get("X-Anvil-Cache"))
	}
	if adminWar.Tick != 3 || adminWar.ViewTick != 1 || len(adminWar.Teams) != 3 || len(adminWar.Services) != 1 || len(adminWar.Grid) != 3 {
		t.Fatalf("admin war room shape = %#v", adminWar)
	}
	if len(adminWar.Events) != 1 || adminWar.Events[0].Tick != 1 || adminWar.Events[0].Text != "delayed-one" {
		t.Fatalf("delayed war room events = %#v", adminWar.Events)
	}
	for _, cell := range adminWar.Grid {
		if cell.IsOwn {
			t.Errorf("admin cell = %#v, staff own nothing", cell)
		}
		if cell.TeamID == teamA.String() && cell.Status != "up" {
			t.Errorf("alpha cell = %#v, want up", cell)
		}
		if cell.TeamID == teamB.String() && cell.Status != "mumble" {
			t.Errorf("bravo cell = %#v, want mumble", cell)
		}
		if cell.TeamID == teamC.String() && cell.Status != "down" {
			t.Errorf("nop cell = %#v, want down", cell)
		}
	}
	if code, headers, _ = warCall(admin); code != http.StatusOK || headers.Get("X-Anvil-Cache") != "HIT" {
		t.Fatalf("admin war room repeat = %d cache=%q", code, headers.Get("X-Anvil-Cache"))
	}

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

	idleService := uuid.New()
	exec(`INSERT INTO game_services (id, name, slug, category, port, checker_ref, flag_stores)
		VALUES ($1, 'Waiting Room', 'waiting-room', 'web', 9400, 'http://waiting-checker:9000', 2)`, idleService)
	exec(`INSERT INTO game_deploys (team_id, service_id, kind, org_initiated, tick_number, deployed_at)
		VALUES ($1, $2, 'deploy', false, 3, '2026-10-24T13:45:00Z')`, teamA, idleService)
	exec(`INSERT INTO game_deployment_events (team_id, service_id, source_sequence, state, recorded_at)
		VALUES ($1, $2, 70, 'waiting_quiet_window', '2026-10-24T13:46:00Z')`, teamA, idleService)
	exec(`INSERT INTO game_deployment_events (team_id, service_id, source_sequence, state, reason_code, recorded_at)
		VALUES ($1, $2, 71, 'failed', 'unknown', '2026-10-24T13:47:00Z')`, teamB, idleService)
	code, raw = call("GET", "/api/v1/arena/me/status", &p1, "")
	if code != http.StatusOK {
		t.Fatalf("team status = %d %s", code, raw)
	}
	var status struct {
		Services []struct {
			Slug   string `json:"slug"`
			Stores []struct {
				Ticks []any `json:"ticks"`
			} `json:"stores"`
			LastDeployment *struct {
				Kind string `json:"kind"`
				Tick int    `json:"tick"`
			} `json:"last_deployment"`
			DeploymentHistory []struct {
				State   string `json:"state"`
				Message string `json:"message"`
			} `json:"deployment_history"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("decode team status: %v\n%s", err, raw)
	}
	foundIdle := false
	for _, service := range status.Services {
		if service.Slug != "waiting-room" {
			continue
		}
		foundIdle = true
		if len(service.Stores) != 2 || len(service.Stores[0].Ticks) != 0 || len(service.Stores[1].Ticks) != 0 {
			t.Errorf("pre-tick service stores = %#v", service.Stores)
		}
		if service.LastDeployment == nil ||
			service.LastDeployment.Kind != "deploy" || service.LastDeployment.Tick != 3 {
			t.Errorf("pre-tick deployment state = %#v", service)
		}
		if len(service.DeploymentHistory) != 1 || service.DeploymentHistory[0].State != "waiting_quiet_window" ||
			service.DeploymentHistory[0].Message != "Gate passed; waiting for a safe promotion window." {
			t.Errorf("redacted deployment history = %#v", service.DeploymentHistory)
		}
	}
	if !foundIdle {
		t.Error("enabled service without SLA ticks was omitted from team status")
	}
}

// the pipeline's server-to-server calls and the organizer re-credit
func TestFinalsInternalEndpointsAndCredit(t *testing.T) {
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
	team, svc, admin := uuid.New(), uuid.New(), uuid.New()
	sfx := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	exec(`TRUNCATE game_teams, game_services, game_ticks CASCADE`)
	defer func() {
		exec(`TRUNCATE game_teams, game_services, game_ticks CASCADE`)
		exec(`DELETE FROM users WHERE id = $1`, admin)
		exec(`DELETE FROM platform_settings WHERE key = 'game.vulnbox_mode'`)
	}()
	exec(`INSERT INTO users (id, username, email, role, status) VALUES ($1, 'ad'||$2, 'ad'||$2||'@x', 'admin', 'active')`, admin, sfx)
	exec(`INSERT INTO game_teams (id, name, slug, vulnbox_ip, team_number) VALUES ($1, 'alpha', 'alpha', '10.60.1.2', 1)`, team)
	exec(`INSERT INTO game_services (id, name, slug, category, port, checker_ref) VALUES ($1, 'Berth', 'berth', 'web', 7004, 'http://c:9000')`, svc)
	exec(`INSERT INTO game_ticks (tick_number, status) VALUES (1, 'closed'), (2, 'running')`)
	exec(`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status, step) VALUES (1, $1, $2, 'DOWN', 'putflag')`, team, svc)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "integration-secret-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	cfg.Game.Enabled = true
	cfg.Game.InternalToken = "pipeline-" + sfx
	containerSvc, err := container.NewService(cfg.Container, zap.NewNop())
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, containerSvc, nil, nil, nil, nil, nil, zap.NewNop()).Router()
	adminTok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
		UserID: admin, TokenType: "user",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), ID: uuid.NewString()},
	}).SignedString([]byte(cfg.JWT.Secret))
	call := func(method, path, bearer, body string) (int, map[string]any, []byte) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Body.Bytes()
	}
	internal := cfg.Game.InternalToken

	if code, _, _ := call("GET", "/api/v1/internal/arena/mode", "", ""); code != http.StatusNotFound {
		t.Fatalf("internal endpoint without token = %d, want 404", code)
	}
	if code, _, _ := call("GET", "/api/v1/internal/arena/mode", "wrong", ""); code != http.StatusNotFound {
		t.Fatalf("internal endpoint with a wrong token = %d, want 404", code)
	}
	if _, body, _ := call("GET", "/api/v1/internal/arena/mode", internal, ""); body["mode"] != "pipeline" {
		t.Fatalf("default mode = %v", body)
	}
	exec(`INSERT INTO platform_settings (key, value) VALUES ('game.vulnbox_mode', '"root"'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if _, body, _ := call("GET", "/api/v1/internal/arena/mode", internal, ""); body["mode"] != "root" {
		t.Fatalf("fallback mode = %v", body)
	}

	deploy := `{"team_id":"` + team.String() + `","service":"berth","kind":"deploy","image_digest":"sha256:abc","diff_lines":3}`
	for i := 0; i < 4; i++ {
		code, body, _ := call("POST", "/api/v1/internal/arena/deploys", internal, deploy)
		if code != http.StatusCreated || body["id"] == nil || body["free_left"] != nil {
			t.Fatalf("deploy %d = %d %v, want an id without deployment pricing", i+1, code, body)
		}
	}
	if code, _, _ := call("POST", "/api/v1/internal/arena/deploys", internal,
		`{"team_id":"`+team.String()+`","service":"nope","kind":"deploy"}`); code != http.StatusNotFound {
		t.Fatalf("deploy for an unknown service = %d, want 404", code)
	}
	var digest string
	if err := db.Pool.QueryRow(ctx, `SELECT image_digest FROM game_deploys WHERE team_id = $1 LIMIT 1`, team).Scan(&digest); err != nil || digest != "sha256:abc" {
		t.Fatalf("deploy row digest = %q err %v", digest, err)
	}

	event := `{"team_id":"` + team.String() + `","service":"berth","source_sequence":91,"state":"failed","reason_code":"gate"}`
	if code, body, _ := call("POST", "/api/v1/internal/arena/deployment-events", internal, event); code != http.StatusCreated || body["id"] == nil {
		t.Fatalf("deployment event create = %d %v", code, body)
	}
	if code, _, _ := call("POST", "/api/v1/internal/arena/deployment-events", internal, event); code != http.StatusOK {
		t.Fatalf("deployment event retry = %d, want 200", code)
	}
	conflict := `{"team_id":"` + team.String() + `","service":"berth","source_sequence":91,"state":"promoted"}`
	if code, _, _ := call("POST", "/api/v1/internal/arena/deployment-events", internal, conflict); code != http.StatusConflict {
		t.Fatalf("deployment event conflict = %d, want 409", code)
	}
	badReason := `{"team_id":"` + team.String() + `","service":"berth","source_sequence":92,"state":"failed","reason_code":"runner_output"}`
	if code, _, _ := call("POST", "/api/v1/internal/arena/deployment-events", internal, badReason); code != http.StatusBadRequest {
		t.Fatalf("deployment event unsafe reason = %d, want 400", code)
	}
	oversized := `{"team_id":"` + team.String() + `","service":"` + strings.Repeat("x", 5000) + `","source_sequence":93,"state":"queued"}`
	if code, _, _ := call("POST", "/api/v1/internal/arena/deployment-events", internal, oversized); code != http.StatusBadRequest {
		t.Fatalf("oversized deployment event = %d, want 400", code)
	}

	code, _, raw := call("GET", "/api/v1/internal/arena/sla?team_id="+team.String()+"&service=berth&since=1", internal, "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"status":"DOWN"`) {
		t.Fatalf("sla before credit = %d %s", code, raw)
	}
	if code, _, _ := call("POST", "/api/v1/admin/arena/credits", adminTok,
		`{"service":"berth","team_id":"`+team.String()+`","tick_from":1,"tick_to":1,"reason":"router reboot"}`); code != http.StatusCreated {
		t.Fatalf("admin credit = %d", code)
	}
	_, _, raw = call("GET", "/api/v1/internal/arena/sla?team_id="+team.String()+"&service=berth&since=1", internal, "")
	if !strings.Contains(string(raw), `"status":"ERROR"`) || !strings.Contains(string(raw), `"raw_status":"DOWN"`) {
		t.Fatalf("sla after credit = %s, want effective ERROR over raw DOWN", raw)
	}
}
