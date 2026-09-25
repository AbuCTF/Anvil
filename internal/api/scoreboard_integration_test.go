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
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// the public team board lists a team only once it has scored, in standings, matrix
// and history alike. destructive: truncates users/teams/challenges in a *_test db.
func TestScoreboardListsOnlyScoringTeams(t *testing.T) {
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

	alpha, bravo, charlie, delta, testTeam := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	a1, a2, b1, c1, d1, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	web, pwn := uuid.New(), uuid.New()
	webEasy, webHard, pwnMed, draft, future, hill := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fEasy1, fEasy2, fHard, fPwn := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	exec(`TRUNCATE users, teams, challenges, categories CASCADE`)
	exec(`INSERT INTO platform_settings (key, value) VALUES ('teams_mode', 'true'), ('economy_mode', 'false')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO teams (id, name, join_code) VALUES
		($1, 'alpha', 'ja'), ($2, 'bravo', 'jb'), ($3, 'charlie', 'jc'), ($4, 'delta', 'jd'), ($5, 'h7 test', 'jt')`,
		alpha, bravo, charlie, delta, testTeam)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'a1', 'a1@x', 'user', 'active', $7), ($2, 'a2', 'a2@x', 'user', 'active', $7),
		($3, 'b1', 'b1@x', 'user', 'active', $8), ($4, 'c1', 'c1@x', 'user', 'active', $9),
		($5, 'd1', 'd1@x', 'user', 'active', $10), ($6, 'ad', 'ad@x', 'admin', 'active', $11)`,
		a1, a2, b1, c1, d1, admin, alpha, bravo, charlie, delta, testTeam)
	exec(`INSERT INTO categories (id, name, slug, sort_order) VALUES ($1, 'web', 'web', 1), ($2, 'pwn', 'pwn', 2)`, web, pwn)
	exec(`INSERT INTO challenges (id, name, slug, difficulty, category_id, status, container_image, base_points, release_date, arena_mode) VALUES
		($1, 'Web Hard', 'web-hard', 'hard', $7, 'published', '', 300, NULL, 'per_team'),
		($2, 'Web Easy', 'web-easy', 'easy', $7, 'published', '', 100, NULL, 'per_team'),
		($3, 'Pwn Med', 'pwn-med', 'medium', $8, 'published', '', 200, NULL, 'per_team'),
		($4, 'Draft', 'draft', 'easy', $7, 'draft', '', 100, NULL, 'per_team'),
		($5, 'Future', 'future', 'easy', $8, 'published', '', 100, NOW() + INTERVAL '1 day', 'per_team'),
		($6, 'Hill', 'hill', 'hard', $8, 'published', '', 0, NULL, 'shared')`,
		webHard, webEasy, pwnMed, draft, future, hill, web, pwn)
	exec(`INSERT INTO flags (id, challenge_id, name, flag_hash, points, sort_order) VALUES
		($1, $5, 'one', 'x1', 50, 0), ($2, $5, 'two', 'x2', 50, 1), ($3, $6, 'root', 'x3', 300, 0),
		($4, $7, 'shell', 'x4', 200, 0), (uuid_generate_v4(), $8, 'd', 'x5', 100, 0), (uuid_generate_v4(), $9, 'f', 'x6', 100, 0)`,
		fEasy1, fEasy2, fHard, fPwn, webEasy, webHard, pwnMed, draft, future)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.JWT.Secret = "integration-secret-integration-secret!"
	cfg.JWT.Issuer = ""
	cfg.RateLimit.Enabled = false
	cfg.VPN.Enabled = false
	gin.SetMode(gin.TestMode)
	var router http.Handler
	fresh := func() { router = NewServer(cfg, db, nil, nil, nil, nil, nil, nil, zap.NewNop()).Router() } // new response cache
	token := func(uid uuid.UUID) string {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
			UserID: uid, TokenType: "user",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(cfg.JWT.Secret))
		return s
	}
	get := func(path string, who *uuid.UUID) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		if who != nil {
			req.Header.Set("Authorization", "Bearer "+token(*who))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, w.Code, w.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	list := func(body map[string]any, key string, fields ...string) string {
		var out []string
		for _, raw := range body[key].([]any) {
			m := raw.(map[string]any)
			var parts []string
			for _, f := range fields {
				switch v := m[f].(type) {
				case float64:
					parts = append(parts, strconv.FormatFloat(v, 'f', -1, 64))
				default:
					parts = append(parts, toString(v))
				}
			}
			out = append(out, strings.Join(parts, ":"))
		}
		return strings.Join(out, ",")
	}
	cells := func(body map[string]any) string {
		var out []string
		for _, raw := range body["rows"].([]any) {
			row := raw.(map[string]any)
			var cs []string
			for _, c := range row["cells"].([]any) {
				cell := c.(map[string]any)
				s := strconv.Itoa(int(cell["s"].(float64))) + strconv.Itoa(int(cell["b"].(float64)))
				if p, ok := cell["p"]; ok {
					s += "p" + strconv.Itoa(int(p.(float64)))
				}
				cs = append(cs, s)
			}
			out = append(out, row["name"].(string)+"="+strings.Join(cs, " "))
		}
		return strings.Join(out, ",")
	}
	rank := func(who uuid.UUID) float64 {
		t.Helper()
		return get("/api/v1/user/me/rank", &who)["rank"].(float64)
	}

	// before any solve nothing is listed anywhere
	fresh()
	sb := get("/api/v1/scoreboard", nil)
	if len(sb["leaderboard"].([]any)) != 0 || sb["total_users"] != 0.0 || sb["teams"] != true {
		t.Errorf("empty board = %v", sb)
	}
	mx := get("/api/v1/scoreboard/matrix", nil)
	if len(mx["rows"].([]any)) != 0 || mx["total_users"] != 0.0 {
		t.Errorf("empty matrix rows = %v", mx["rows"])
	}
	if got := list(mx, "challenges", "slug", "difficulty"); got != "web-easy:easy,web-hard:hard,pwn-med:medium" {
		t.Errorf("matrix columns = %s, want released solvable only, category then difficulty", got)
	}
	if h := get("/api/v1/scoreboard/history", nil); len(h["series"].([]any)) != 0 {
		t.Errorf("empty history = %v", h)
	}
	if r := rank(a1); r != 0 {
		t.Errorf("unscored team rank = %v, want 0", r)
	}

	// alpha completes web-easy across two members (after bravo) and takes pwn first;
	// the hidden test team's earlier pwn solve takes no blood; delta only has koth
	t0 := time.Now().Add(-time.Hour)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	exec(`INSERT INTO solves (user_id, challenge_id, flag_id, points_awarded, solved_at) VALUES
		($1, $5, $7, 50, $10), ($2, $5, $8, 50, $11), ($1, $6, $9, 200, $12),
		($3, $5, $7, 50, $13), ($3, $5, $8, 50, $14), ($4, $6, $9, 200, $15)`,
		a1, a2, b1, admin, webEasy, pwnMed, fEasy1, fEasy2, fPwn,
		at(1), at(5), at(10), at(3), at(4), at(0))
	exec(`UPDATE teams SET total_score = CASE id WHEN $1 THEN 300 WHEN $2 THEN 100 WHEN $3 THEN 200 ELSE 0 END`,
		alpha, bravo, testTeam)
	exec(`UPDATE teams SET koth_score = 30 WHERE id = $1`, delta)

	fresh()
	sb = get("/api/v1/scoreboard", nil)
	if got := list(sb, "leaderboard", "username", "total_score", "rank"); got != "alpha:300:1,bravo:100:2,delta:30:3" || sb["total_users"] != 3.0 {
		t.Errorf("board = %s total=%v", got, sb["total_users"])
	}
	if sb = get("/api/v1/scoreboard?q=alp", nil); sb["matching_users"] != 1.0 || sb["total_users"] != 3.0 {
		t.Errorf("search counts = %v/%v", sb["matching_users"], sb["total_users"])
	}
	if sb = get("/api/v1/scoreboard?sort=name", nil); list(sb, "leaderboard", "username", "rank") != "alpha:1,bravo:2,delta:3" {
		t.Errorf("name sort = %s", list(sb, "leaderboard", "username", "rank"))
	}
	if r := rank(a2); r != 1 {
		t.Errorf("alpha rank = %v", r)
	}
	if r := rank(c1); r != 0 {
		t.Errorf("charlie rank = %v, want 0 (unscored)", r)
	}

	mx = get("/api/v1/scoreboard/matrix", nil)
	if got := list(mx, "rows", "name", "rank", "total"); got != "alpha:1:300,bravo:2:100,delta:3:30" || mx["teams"] != true {
		t.Errorf("matrix rows = %s", got)
	}
	if got := cells(mx); got != "alpha=12 00 11,bravo=11 00 00,delta=00 00 00" {
		t.Errorf("matrix cells = %s", got)
	}
	mx = get("/api/v1/scoreboard/matrix?limit=2&page=2", nil)
	if got := list(mx, "rows", "name", "rank"); got != "delta:3" || mx["matching_users"] != 3.0 {
		t.Errorf("matrix page 2 = %s (%v)", got, mx["matching_users"])
	}
	if mx = get("/api/v1/scoreboard/matrix?q=brav", nil); list(mx, "rows", "name", "rank") != "bravo:2" {
		t.Errorf("matrix search = %s", list(mx, "rows", "name", "rank"))
	}

	h := get("/api/v1/scoreboard/history", nil)
	var series []string
	for _, raw := range h["series"].([]any) {
		s := raw.(map[string]any)
		var ys []string
		for _, p := range s["points"].([]any) {
			ys = append(ys, strconv.Itoa(int(p.(map[string]any)["y"].(float64))))
		}
		series = append(series, s["label"].(string)+"="+strings.Join(ys, "/"))
	}
	if got := strings.Join(series, ","); got != "alpha=50/100/300,bravo=50/100" {
		t.Errorf("team history = %s", got)
	}

	// before the start the matrix gives nothing away, except to staff
	exec(`INSERT INTO platform_settings (key, value) VALUES
		('event.start_at', to_jsonb($1::text)), ('event.end_at', to_jsonb($2::text))
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339), time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339))
	fresh()
	mx = get("/api/v1/scoreboard/matrix", &b1)
	if len(mx["challenges"].([]any)) != 0 || len(mx["rows"].([]any)) != 0 {
		t.Errorf("pre-event matrix = %v", mx)
	}
	if mx = get("/api/v1/scoreboard/matrix", &admin); len(mx["challenges"].([]any)) != 3 {
		t.Errorf("pre-event staff matrix = %v", mx["challenges"])
	}
	exec(`DELETE FROM platform_settings WHERE key IN ('event.start_at', 'event.end_at')`)

	// economy: points (fractional, rounded), a held share, a member solve or koth list a
	// team; a granted-but-idle team does not. a held share shows as a partial cell.
	exec(`UPDATE platform_settings SET value = 'true' WHERE key = 'economy_mode'`)
	exec(`INSERT INTO economy_team_score (team_id, points, credits, grant_issued) VALUES
		($1, 150.5, 4000, true), ($2, 0, 4000, true), ($3, 0, 4000, true)`, alpha, bravo, charlie)
	exec(`INSERT INTO economy_challenge_state (team_id, challenge_id, status, holds_solve, frac) VALUES
		($1, $2, 'solved', true, 1), ($1, $3, 'open', true, 0.5), ($4, $2, 'open', false, 1)`,
		alpha, pwnMed, webHard, charlie)
	exec(`INSERT INTO economy_point_events (team_id, challenge_id, kind, value_after, created_at) VALUES
		($1, $2, 'crowd_recompute', 100, $5), ($1, $3, 'crowd_recompute', 80, $6),
		($1, $2, 'crowd_recompute', 70.4, $7), ($4, $2, 'crowd_recompute', 60, $6)`,
		alpha, webEasy, pwnMed, bravo, at(1), at(10), at(30))
	fresh()
	sb = get("/api/v1/scoreboard", nil)
	if got := list(sb, "leaderboard", "username", "total_score", "rank"); got != "alpha:151:1,delta:30:2,bravo:0:3" || sb["total_users"] != 3.0 || sb["economy"] != true {
		t.Errorf("economy board = %s total=%v", got, sb["total_users"])
	}
	mx = get("/api/v1/scoreboard/matrix", nil)
	if got := cells(mx); got != "alpha=12 00p1 11,delta=00 00 00,bravo=11 00 00" {
		t.Errorf("economy matrix cells = %s", got)
	}
	h = get("/api/v1/scoreboard/history", nil)
	series = nil
	for _, raw := range h["series"].([]any) {
		s := raw.(map[string]any)
		var ys []string
		for _, p := range s["points"].([]any) {
			ys = append(ys, strconv.Itoa(int(p.(map[string]any)["y"].(float64))))
		}
		series = append(series, s["label"].(string)+"="+strings.Join(ys, "/"))
	}
	if got := strings.Join(series, ","); got != "alpha=100/180/150,bravo=60" {
		t.Errorf("economy history = %s", got)
	}
	if r := rank(c1); r != 0 {
		t.Errorf("idle economy team rank = %v, want 0", r)
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
