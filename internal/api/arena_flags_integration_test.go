//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TestBulkFlagSubmit covers PUT /api/v1/arena/flags (X-Team-Token auth, bulk results)
// and the admin token-rotation, vulnbox-IP, checker-log and sla-suspicious endpoints.
func TestBulkFlagSubmit(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port,
		User:     os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"),
		Database: name, SSLMode: "disable",
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

	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	makeTok := func() string {
		return strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	teamA, teamB, teamNOP := uuid.New(), uuid.New(), uuid.New()
	platformTeamA, platformTeamB := uuid.New(), uuid.New()
	adminUser := uuid.New()
	svc := uuid.New()
	tokA := makeTok()
	tokNOP := makeTok()

	clearGame := func() {
		exec(`DELETE FROM platform_settings WHERE key = 'game.state'`)
		setting("event.start_at", `""`)
		setting("event.end_at", `""`)
		setting("arena_enabled", "false")
	}

	exec(`TRUNCATE game_teams, game_services, game_ticks CASCADE`)
	clearGame()
	defer func() {
		exec(`TRUNCATE game_teams, game_services, game_ticks CASCADE`)
		exec(`DELETE FROM users WHERE id = $1`, adminUser)
		exec(`DELETE FROM teams WHERE id IN ($1, $2)`, platformTeamA, platformTeamB)
		clearGame()
	}()

	setting("arena_enabled", "true")
	exec(`INSERT INTO teams (id, name, join_code) VALUES ($1, 'alpha'||$3, 'ja'||$3), ($2, 'bravo'||$3, 'jb'||$3)`,
		platformTeamA, platformTeamB, sfx)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES ($1, 'adm'||$2, 'adm'||$2||'@x', 'admin', 'active', NULL)`,
		adminUser, sfx)

	exec(`INSERT INTO game_teams (id, name, slug, vulnbox_ip, team_number, token) VALUES
		($1, 'alpha', 'alpha', '10.60.1.2', 1, $4),
		($2, 'bravo', 'bravo', '10.60.2.2', 2, NULL),
		($3, 'nopteam', 'nopteam', '10.60.9.2', 9, $5)`,
		teamA, teamB, teamNOP, tokA, tokNOP)
	exec(`UPDATE game_teams SET is_nop = TRUE WHERE id = $1`, teamNOP)
	exec(`INSERT INTO game_services (id, name, slug, category, port, checker_ref, flag_stores) VALUES ($1, 'Svc', 'svc', 'web', 8080, 'http://checker:9000', 1)`, svc)
	exec(`INSERT INTO game_ticks (tick_number, status) VALUES (1, 'closed'), (2, 'closed')`)
	exec(`INSERT INTO game_flags (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at) VALUES
		(2, $1, $3, 0, 'H7CTF{BRAVO_FLAG}', 2, 9, NOW()),
		(2, $2, $3, 0, 'H7CTF{ALPHA_FLAG}', 2, 9, NOW())`,
		teamB, teamA, svc)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "integration-secret-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	cfg.Game.Enabled = true
	cfg.Container.NetworkName = "bridge" // no container is provisioned by this API test
	containerSvc, err := container.NewService(cfg.Container, zap.NewNop())
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := NewServer(cfg, db, containerSvc, nil, nil, nil, nil, nil, zap.NewNop()).Router()

	adminToken := func() string {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: adminUser, Role: "admin", TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				ID:        uuid.NewString(),
			},
		}).SignedString([]byte(cfg.JWT.Secret))
		return s
	}

	callToken := func(method, path, tok string, body any) (int, []byte) {
		t.Helper()
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if tok != "" {
			req.Header.Set("X-Team-Token", tok)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code, w.Body.Bytes()
	}
	callAdmin := func(method, path string, body any) (int, []byte) {
		t.Helper()
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+adminToken())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code, w.Body.Bytes()
	}

	t.Run("no token returns 401", func(t *testing.T) {
		code, _ := callToken("PUT", "/api/v1/arena/flags", "", []string{"H7CTF{BRAVO_FLAG}"})
		if code != http.StatusUnauthorized {
			t.Errorf("want 401, got %d", code)
		}
	})

	t.Run("invalid token returns 401", func(t *testing.T) {
		code, _ := callToken("PUT", "/api/v1/arena/flags", "notavalidtoken", []string{"H7CTF{BRAVO_FLAG}"})
		if code != http.StatusUnauthorized {
			t.Errorf("want 401, got %d", code)
		}
	})

	t.Run("valid token but game not live returns not_live", func(t *testing.T) {
		// game.state unset = legacy mode, which FinalsPhase treats as !ok => not_live
		code, raw := callToken("PUT", "/api/v1/arena/flags", tokA, []string{"H7CTF{BRAVO_FLAG}"})
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d %s", code, raw)
		}
		var results []struct {
			Flag   string `json:"flag"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &results); err != nil {
			t.Fatalf("decode: %v %s", err, raw)
		}
		if len(results) != 1 || results[0].Status != string(game.SubmitNotLive) {
			t.Errorf("want [{not_live}], got %+v", results)
		}
	})

	// bring the game live
	setting("game.state", `"running"`)
	now := time.Now().UTC()
	setting("event.start_at", fmt.Sprintf(`"%s"`, now.Add(-time.Minute).Format(time.RFC3339)))
	setting("event.end_at", fmt.Sprintf(`"%s"`, now.Add(2*time.Hour).Format(time.RFC3339)))

	t.Run("NOP team gets nop for all flags", func(t *testing.T) {
		code, raw := callToken("PUT", "/api/v1/arena/flags", tokNOP, []string{"H7CTF{BRAVO_FLAG}", "H7CTF{JUNK}"})
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d %s", code, raw)
		}
		var results []struct {
			Status string `json:"status"`
		}
		json.Unmarshal(raw, &results)
		for _, r := range results {
			if r.Status != string(game.SubmitNop) {
				t.Errorf("want nop, got %s", r.Status)
			}
		}
	})

	t.Run("accepted, own, invalid, duplicate", func(t *testing.T) {
		flags := []string{
			"H7CTF{BRAVO_FLAG}", // teamA attacks teamB: accepted
			"H7CTF{ALPHA_FLAG}", // teamA attacks self: own
			"H7CTF{NOTREAL}",    // invalid
			"H7CTF{BRAVO_FLAG}", // duplicate
		}
		code, raw := callToken("PUT", "/api/v1/arena/flags", tokA, flags)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d %s", code, raw)
		}
		var results []struct {
			Flag   string `json:"flag"`
			Status string `json:"status"`
		}
		json.Unmarshal(raw, &results)
		want := []string{
			string(game.SubmitAccepted),
			string(game.SubmitOwnFlag),
			string(game.SubmitInvalid),
			string(game.SubmitDuplicate),
		}
		for i, r := range results {
			if r.Status != want[i] {
				t.Errorf("[%d] flag=%s want=%s got=%s", i, r.Flag, want[i], r.Status)
			}
		}
	})

	t.Run("over 500 flags rejected", func(t *testing.T) {
		many := make([]string, 501)
		for i := range many {
			many[i] = "H7CTF{X}"
		}
		code, _ := callToken("PUT", "/api/v1/arena/flags", tokA, many)
		if code != http.StatusBadRequest {
			t.Errorf("want 400, got %d", code)
		}
	})

	t.Run("admin rotate token", func(t *testing.T) {
		code, raw := callAdmin("POST", fmt.Sprintf("/api/v1/admin/arena/teams/%s/token", teamB), nil)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d %s", code, raw)
		}
		var resp struct {
			Token string `json:"token"`
		}
		json.Unmarshal(raw, &resp)
		if len(resp.Token) != 64 {
			t.Errorf("want 64-char token, got %q", resp.Token)
		}
		// teamB can now submit with the new token
		code2, raw2 := callToken("PUT", "/api/v1/arena/flags", resp.Token, []string{"H7CTF{JUNK}"})
		if code2 != http.StatusOK {
			t.Errorf("new token: want 200, got %d %s", code2, raw2)
		}
	})

	t.Run("admin update vulnbox ip", func(t *testing.T) {
		code, raw := callAdmin("PATCH",
			fmt.Sprintf("/api/v1/admin/arena/teams/%s/vulnbox-ip", teamA),
			map[string]string{"vulnbox_ip": "10.60.1.99"})
		if code != http.StatusOK {
			t.Errorf("want 200, got %d %s", code, raw)
		}
		var row struct{ VulnboxIP string }
		if err := db.Pool.QueryRow(ctx, `SELECT host(vulnbox_ip) FROM game_teams WHERE id = $1`, teamA).Scan(&row.VulnboxIP); err != nil {
			t.Fatal(err)
		}
		if row.VulnboxIP != "10.60.1.99" {
			t.Errorf("want 10.60.1.99, got %s", row.VulnboxIP)
		}
	})

	t.Run("admin checker log returns rows", func(t *testing.T) {
		exec(`INSERT INTO game_checker_tasks (tick_number, related_tick, team_id, service_id, method, variant, status, message, code)
			VALUES (2, 2, $1, $2, 'putflag', 0, 'OK', 'ok', NULL)`, teamA, svc)
		code, raw := callAdmin("GET",
			fmt.Sprintf("/api/v1/admin/arena/checker-log?team_id=%s&service_id=%s&tick=2", teamA, svc),
			nil)
		if code != http.StatusOK {
			t.Errorf("want 200, got %d %s", code, raw)
		}
		var resp struct {
			Tasks []struct {
				Method string `json:"method"`
			} `json:"tasks"`
		}
		json.Unmarshal(raw, &resp)
		if len(resp.Tasks) == 0 {
			t.Error("want at least one checker task row")
		}
	})

	t.Run("reference attack failures remain audit-only", func(t *testing.T) {
		// ENO MUMBLE maps to FAULTY. These rows remain in the checker log, but
		// must not contribute to SLA suspicion or service-health calculations.
		for i := 0; i < 4; i++ {
			exec(`INSERT INTO game_checker_tasks (tick_number, related_tick, team_id, service_id, method, variant, status)
				VALUES (2, 2, $1, $2, 'exploit', $3, 'FAULTY')`, teamA, svc, i)
		}
		code, raw := callAdmin("GET", "/api/v1/admin/arena/sla-suspicious?since_tick=1", nil)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d %s", code, raw)
		}
		var suspicious struct {
			Rows []struct {
				TeamID string `json:"team_id"`
			} `json:"rows"`
		}
		if err := json.Unmarshal(raw, &suspicious); err != nil {
			t.Fatal(err)
		}
		if len(suspicious.Rows) != 0 {
			t.Fatalf("exploit failures triggered suspicious SLA: %+v", suspicious.Rows)
		}

		code, raw = callAdmin("GET",
			fmt.Sprintf("/api/v1/admin/arena/checker-log?team_id=%s&service_id=%s&tick=2", teamA, svc), nil)
		if code != http.StatusOK {
			t.Fatalf("checker log: want 200, got %d %s", code, raw)
		}
		var audit struct {
			Tasks []struct {
				Method string `json:"method"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &audit); err != nil {
			t.Fatal(err)
		}
		var exploits int
		for _, task := range audit.Tasks {
			if task.Method == game.MethodExploit {
				exploits++
			}
		}
		if exploits != 4 {
			t.Fatalf("checker audit contains %d exploit rows, want 4", exploits)
		}
	})

	t.Run("admin sla suspicious returns rows on high failure rate", func(t *testing.T) {
		// insert 4 DOWN tasks for teamB+svc in tick 2 (100% failure)
		for i := 0; i < 4; i++ {
			exec(`INSERT INTO game_checker_tasks (tick_number, related_tick, team_id, service_id, method, variant, status)
				VALUES (2, 2, $1, $2, 'getflag', $3, 'DOWN')`, teamB, svc, i)
		}
		code, raw := callAdmin("GET", "/api/v1/admin/arena/sla-suspicious?since_tick=1", nil)
		if code != http.StatusOK {
			t.Errorf("want 200, got %d %s", code, raw)
		}
		var resp struct {
			Rows []struct {
				TeamID   string  `json:"team_id"`
				DownRate float64 `json:"down_rate"`
			} `json:"rows"`
		}
		json.Unmarshal(raw, &resp)
		if len(resp.Rows) != 1 || resp.Rows[0].TeamID != teamB.String() || resp.Rows[0].DownRate != 1 {
			t.Errorf("suspicious rows = %+v, want only normal getflag failures for teamB", resp.Rows)
		}
	})
}
