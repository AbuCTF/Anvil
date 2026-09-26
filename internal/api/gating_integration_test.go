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
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// economy gate + staff test-team hiding, end to end through the real router.
// destructive: truncates users/teams/challenges in a *_test database.
func TestEconomyGatingAndStaffTeams(t *testing.T) {
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

	teamA, teamB, teamT, teamC, teamD := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	p1, p2, p3, author, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	p4, p5 := uuid.New(), uuid.New()
	c1, c2, c3, cat := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	flag1Hash := sha256.Sum256([]byte("flag{one}"))

	exec(`TRUNCATE users, teams, challenges, categories CASCADE`) // cascades into platform_settings too
	exec(`INSERT INTO platform_settings (key, value) VALUES
		('economy_mode', 'true'), ('teams_mode', 'true'), ('registration_mode', '"token"')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO teams (id, name, join_code, total_score, koth_score) VALUES
		($1, 'alpha', 'ja', 0, 0), ($2, 'bravo', 'jb', 0, 50), ($3, 'h7 test', 'jt', 0, 0),
		($4, 'charlie', 'jc', 0, 0), ($5, 'delta', 'jd', 0, 0)`, teamA, teamB, teamT, teamC, teamD)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'p4', 'p4@x', 'user', 'active', $3), ($2, 'p5', 'p5@x', 'user', 'active', $4)`, p4, p5, teamC, teamD)
	exec(`INSERT INTO users (id, username, email, role, status, team_id) VALUES
		($1, 'p1', 'p1@x', 'user', 'active', $6), ($2, 'p2', 'p2@x', 'user', 'active', NULL),
		($3, 'p3', 'p3@x', 'user', 'active', $7), ($4, 'au', 'au@x', 'author', 'active', $8),
		($5, 'ad', 'ad@x', 'admin', 'active', NULL)`, p1, p2, p3, author, admin, teamA, teamB, teamT)
	exec(`INSERT INTO categories (id, name, slug) VALUES ($1, 'web', 'web')`, cat)
	exec(`INSERT INTO challenges (id, name, slug, description, sub_description, difficulty, category_id, status, container_image, total_flags)
		VALUES ($1, 'Static One', 'static-one', 'SECRET BRIEF', 'teaser line', 'easy', $3, 'published', '', 1),
		       ($2, 'Box Two', 'box-two', 'BOX BRIEF', NULL, 'medium', $3, 'published', 'anvil-gating-test/none', 1)`, c1, c2, cat)
	exec(`INSERT INTO challenges (id, name, slug, difficulty, category_id, status, container_image, arena_mode)
		VALUES ($1, 'Throne', 'throne', 'hard', $2, 'published', '', 'shared')`, c3, cat)
	exec(`INSERT INTO flags (challenge_id, name, flag_hash, points, flag_type) VALUES
		($1, 'rce via upload', $3, 100, 'static'), ($2, 'box flag', 'x', 250, 'static')`, c1, c2, hex.EncodeToString(flag1Hash[:]))
	hint := uuid.New()
	exec(`INSERT INTO hints (id, challenge_id, content, cost) VALUES ($1, $2, 'HINT TEXT', 0)`, hint, c1)
	attachment := uuid.New()
	exec(`INSERT INTO challenge_attachments (id, challenge_id, uploaded_by, filename, file_size, url)
		VALUES ($1, $2, $3, 'handout.zip', 10, 'https://files.invalid/handout.zip')`, attachment, c1, admin)
	exec(`INSERT INTO economy_team_score (team_id, points, credits, grant_issued) VALUES
		($1, 100, 4000, true), ($2, 70, 4000, true), ($3, 1000, 4000, true), ($4, 0, 4000, true)`, teamA, teamB, teamT, teamC)
	// charlie: c1's timer ran out but isn't swept yet, c2 already swept to expired
	exec(`INSERT INTO economy_challenge_state (team_id, challenge_id, status, opened_at, expires_at) VALUES
		($1, $4, 'open', NOW(), NOW() + INTERVAL '1 hour'), ($1, $5, 'open', NOW(), NOW() + INTERVAL '1 hour'),
		($2, $4, 'abandoned', NOW(), NULL),
		($3, $4, 'open', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 minute'),
		($3, $5, 'expired', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 hour')`, teamA, teamB, teamC, c1, c2)
	// running instances, so an admitted start stops at "already exists" instead of provisioning
	exec(`INSERT INTO instances (id, user_id, team_id, challenge_id, resource_type, status, expires_at) VALUES
		($1, $3, $4, $6, 'docker', 'running', NOW() + INTERVAL '1 hour'),
		($2, $5, $7, $6, 'docker', 'running', NOW() + INTERVAL '1 hour')`,
		uuid.New(), uuid.New(), p1, teamA, author, c2, teamT)

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
		t.Skipf("docker unavailable for the instance gate: %v", err)
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
	callBearer := func(method, path, bearer, body string) (int, map[string]any, http.Header) {
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
		return w.Code, out, w.Header()
	}
	call := func(method, path string, who *uuid.UUID, body string) (int, map[string]any, http.Header) {
		bearer := ""
		if who != nil {
			bearer = token(*who)
		}
		return callBearer(method, path, bearer, body)
	}
	expect := func(what string, got, want int, body map[string]any) {
		t.Helper()
		if got != want {
			t.Errorf("%s: status %d, want %d (%v)", what, got, want, body)
		}
	}
	listDesc := func(who *uuid.UUID) map[string]any {
		t.Helper()
		_, body, _ := call("GET", "/api/v1/challenges", who, "")
		out := map[string]any{}
		for _, raw := range body["challenges"].([]any) {
			ch := raw.(map[string]any)
			out[ch["slug"].(string)] = ch["description"]
		}
		return out
	}

	// Infrastructure artifact ingestion is an admin surface. A participant is
	// rejected before an upload service/session can be reached or created.
	{
		code, body, _ := call("GET", "/api/v1/uploads/types", &p1, "")
		expect("participant upload types", code, http.StatusForbidden, body)
		code, body, _ = call("GET", "/api/v1/uploads/types", &admin, "")
		expect("admin upload types", code, http.StatusOK, body)
	}

	// Public discovery and registration enforcement read the same runtime mode.
	if code, body, _ := call("GET", "/api/v1/info", nil, ""); code != http.StatusOK || body["registration_mode"] != "token" {
		t.Errorf("platform registration mode = %d %v, want token", code, body)
	}

	// Irreversible economy actions expose their current server-side price before
	// the UI enables them; quote calls themselves do not mutate either ledger.
	if code, body, _ := call("POST", "/api/v1/economy/convert/quote", &p1, `{"points":75}`); code != http.StatusOK || body["credits"] != 67.5 {
		t.Errorf("conversion quote = %d %v, want 67.5 credits", code, body)
	}
	if code, body, _ := call("GET", "/api/v1/economy/challenges/static-one/extension-quote", &p1, ""); code != http.StatusOK || body["cost"] != 25.0 || body["added_seconds"] != 3600.0 {
		t.Errorf("extension quote = %d %v, want 25 credits and 3600 seconds", code, body)
	}

	// list: description only for the opener's team (and staff)
	if d := listDesc(&p1); d["static-one"] != "SECRET BRIEF" || d["box-two"] != "BOX BRIEF" {
		t.Errorf("opener list = %v", d)
	}
	for _, who := range []*uuid.UUID{nil, &p2, &p3} {
		if d := listDesc(who); d["static-one"] != nil || d["box-two"] != nil {
			t.Errorf("locked list leaked descriptions: %v", d)
		}
	}
	if d := listDesc(&author); d["static-one"] != "SECRET BRIEF" {
		t.Errorf("author list = %v", d)
	}

	// detail: locked resource-backed challenges retain the brief, but redact every
	// act-gated resource; an opened team receives the resources and file ticket.
	for _, who := range []*uuid.UUID{nil, &p2, &p3} {
		code, body, _ := call("GET", "/api/v1/challenges/static-one", who, "")
		expect("locked detail", code, 200, body)
		if body["description"] != "SECRET BRIEF" || len(body["flags"].([]any)) != 0 || len(body["hints"].([]any)) != 0 ||
			len(body["attachments"].([]any)) != 0 || body["sub_description"] != "teaser line" || body["total_flags"] != 1.0 {
			t.Errorf("locked detail = %v", body)
		}
	}
	code, body, _ := call("GET", "/api/v1/challenges/static-one", &p1, "")
	expect("open detail", code, 200, body)
	files, _ := body["attachments"].([]any)
	if body["description"] != "SECRET BRIEF" || len(body["hints"].([]any)) != 1 || len(files) != 1 {
		t.Fatalf("open detail = %v", body)
	}
	ticket := files[0].(map[string]any)["ticket"].(string)
	if code, body, _ := call("GET", "/api/v1/challenges/static-one", &author, ""); code != 200 || body["description"] != "SECRET BRIEF" {
		t.Errorf("author detail = %d %v", code, body)
	}

	// downloads: plain link needs the ticket (or an opened team's auth)
	dl := "/api/v1/challenges/static-one/attachments/" + attachment.String() + "/download"
	code, body, _ = call("GET", dl, nil, "")
	expect("anonymous download", code, 403, body)
	code, body, _ = call("GET", dl+"?t=1.deadbeef", nil, "")
	expect("forged ticket", code, 403, body)
	code, body, _ = call("GET", dl, &p3, "")
	expect("abandoned team download", code, 403, body)
	code, _, hdr := call("GET", dl+"?t="+ticket, nil, "")
	if code != http.StatusFound || hdr.Get("Location") != "https://files.invalid/handout.zip" {
		t.Errorf("ticketed download = %d %q", code, hdr.Get("Location"))
	}
	code, body, _ = call("GET", dl, &p1, "")
	expect("opened team download", code, 302, body)

	// hints, flags, submit, instance: 403 before anything counts
	code, body, _ = call("GET", "/api/v1/challenges/static-one/hints", &p3, "")
	expect("locked hints", code, 403, body)
	code, body, _ = call("GET", "/api/v1/challenges/static-one/hints", &p1, "")
	expect("open hints", code, 200, body)
	code, body, _ = call("GET", "/api/v1/challenges/static-one/flags", &p3, "")
	expect("locked flags", code, 403, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/hints/"+uuid.NewString()+"/unlock", &p3, "")
	expect("locked hint unlock", code, 403, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/submit", &p3, `{"flag":"flag{nope}"}`)
	expect("locked submit", code, 403, body)
	if body["error"] != "open this challenge first" {
		t.Errorf("locked submit message = %v", body["error"])
	}
	code, body, _ = call("POST", "/api/v1/challenges/static-one/submit", &p2, `{"flag":"flag{nope}"}`)
	expect("teamless submit", code, 403, body)
	var attempts int
	_ = db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM flag_attempts WHERE user_id = ANY($1))
		+ (SELECT COUNT(*) FROM flag_attempt_lockouts WHERE user_id = ANY($1))`, []uuid.UUID{p2, p3}).Scan(&attempts)
	if attempts != 0 {
		t.Errorf("locked submits were counted: %d attempt/lockout rows", attempts)
	}
	code, body, _ = call("POST", "/api/v1/instances", &p3, `{"challenge_slug":"box-two"}`)
	expect("locked instance start", code, 403, body)
	code, body, _ = call("POST", "/api/v1/instances", &p1, `{"challenge_slug":"box-two"}`)
	expect("opened instance start passes the gate", code, 400, body)
	code, body, _ = call("POST", "/api/v1/instances", &author, `{"challenge_slug":"box-two"}`)
	expect("author instance start bypasses the gate", code, 400, body)

	// Author on the hidden test team: open it so the economy write path has a
	// locked state row, then exercise the full scoring path.
	code, body, _ = call("POST", "/api/v1/challenges/static-one/open", &author, "")
	if code != http.StatusOK || body["status"] != "open" {
		t.Fatalf("author open = %d %v", code, body)
	}
	code, body, _ = call("POST", "/api/v1/challenges/static-one/submit", &author, `{"flag":"flag{one}"}`)
	if code != 200 || body["correct"] != true {
		t.Fatalf("author submit = %d %v", code, body)
	}
	var st string
	_ = db.Pool.QueryRow(ctx, `SELECT status FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`, teamT, c1).Scan(&st)
	if st != "solved" {
		t.Errorf("author's team economy status = %q, want solved", st)
	}
	code, body, _ = call("POST", "/api/v1/challenges/static-one/open", &author, "")
	if code != http.StatusOK || body["status"] != "solved" || body["already_solved"] != true {
		t.Errorf("open solved challenge = %d %v", code, body)
	}

	// timer ran out: still readable (paid once), but nothing actionable until re-opened
	code, body, _ = call("GET", "/api/v1/challenges/static-one", &p4, "")
	if code != 200 || body["description"] != "SECRET BRIEF" || body["economy"].(map[string]any)["launched"] != false {
		t.Errorf("expired detail = %d %v", code, body)
	}
	code, body, _ = call("GET", dl, &p4, "")
	expect("expired team download", code, 302, body)
	code, body, _ = call("GET", "/api/v1/challenges/static-one/hints", &p4, "")
	expect("expired team hint list", code, 200, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/hints/"+hint.String()+"/unlock", &p4, "")
	expect("expired team hint unlock", code, 403, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/submit", &p4, `{"flag":"flag{nope}"}`)
	expect("expired team submit", code, 403, body)
	if body["error"] != "your timer on this challenge ran out; open it again first" {
		t.Errorf("expired submit message = %v", body["error"])
	}
	code, body, _ = call("POST", "/api/v1/instances", &p4, `{"challenge_slug":"box-two"}`)
	expect("expired team instance start", code, 403, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/extend", &p4, "")
	expect("expired team extend", code, 400, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/abandon", &p4, "")
	expect("expired team abandon", code, 400, body)
	code, body, _ = call("POST", "/api/v1/challenges/static-one/open", &p4, "")
	if code != 200 || body["credits"] != 3950.0 {
		t.Errorf("re-open after expiry = %d %v, want charged to 3950", code, body)
	}
	code, body, _ = call("POST", "/api/v1/challenges/static-one/submit", &p4, `{"flag":"flag{nope}"}`)
	if code != 200 || body["correct"] != false {
		t.Errorf("submit after re-open = %d %v", code, body)
	}
	code, body, _ = call("POST", "/api/v1/challenges/static-one/open", &p1, "")
	if code != 200 || body["credits"] != 4000.0 {
		t.Errorf("open on a live timer must not charge: %d %v", code, body)
	}

	// koth buy-in for a team that never touched the economy: lazy grant, no 500
	code, body, _ = call("POST", "/api/v1/challenges/throne/koth-enter", &p5, "")
	if code != 200 || body["credits"] != 3500.0 {
		t.Errorf("koth enter without an economy row = %d %v", code, body)
	}

	// boards: test team and author never show; koth counts on the economy board
	code, body, _ = call("GET", "/api/v1/scoreboard", &p1, "")
	expect("scoreboard", code, 200, body)
	var names []string
	for _, raw := range body["leaderboard"].([]any) {
		e := raw.(map[string]any)
		names = append(names, e["username"].(string)+":"+strconv.Itoa(int(e["total_score"].(float64))))
	}
	// charlie and delta never scored, so they don't list yet
	if strings.Join(names, ",") != "bravo:120,alpha:100" || body["total_users"] != 2.0 {
		t.Errorf("economy board = %v total=%v", names, body["total_users"])
	}
	code, body, _ = call("GET", "/api/v1/user/me/rank", &p1, "")
	if code != 200 || body["rank"] != 2.0 {
		t.Errorf("p1 team rank = %d %v, want 2", code, body)
	}
	if _, body, _ = call("GET", "/api/v1/user/me", &p3, ""); body["rank"] != 1.0 || body["total_score"] != 120.0 || body["score_scope"] != "team" {
		t.Errorf("p3 team-scoped profile = %v, want rank 1 and score 120", body)
	}
	if _, body, _ = call("GET", "/api/v1/user/me/rank", &author, ""); body["rank"] != 0.0 {
		t.Errorf("author rank = %v, want 0 (hidden)", body["rank"])
	}
	if _, body, _ = call("GET", "/api/v1/teams/me", &p3, ""); body["team"].(map[string]any)["total_score"] != 120.0 {
		t.Errorf("bravo team page total = %v, want 120", body["team"])
	}
	_, body, _ = call("GET", "/api/v1/scoreboard/matrix", &p1, "")
	for _, raw := range body["rows"].([]any) {
		if u := raw.(map[string]any)["username"]; u == "au" || u == "ad" {
			t.Errorf("staff user %v on the matrix", u)
		}
	}
	if code, _, _ = call("GET", "/api/v1/profile/au", nil, ""); code != 404 {
		t.Errorf("author public profile = %d, want 404", code)
	}
	_, body, _ = call("GET", "/api/v1/stats", nil, "")
	if body["total_users"] != 5.0 || body["total_solves"] != 0.0 {
		t.Errorf("public stats = %v", body)
	}

	// economy off: everything back to the old behavior
	exec(`UPDATE platform_settings SET value = 'false' WHERE key = 'economy_mode'`)
	if d := listDesc(nil); d["static-one"] != "SECRET BRIEF" {
		t.Errorf("economy off list = %v", d)
	}
	code, body, _ = call("GET", dl, nil, "")
	expect("economy off anonymous download", code, 302, body)

	// Logout revokes the exact bearer immediately, rather than only clearing
	// browser storage. A separately minted token for the same user stays valid.
	code, body, _ = callBearer("POST", "/api/v1/auth/logout", "not-a-valid-jwt", `{}`)
	expect("invalid bearer logout", code, http.StatusOK, body)
	var revocations int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM revoked_access_tokens`).Scan(&revocations); err != nil || revocations != 0 {
		t.Errorf("invalid bearer created %d revocation rows (err=%v)", revocations, err)
	}
	access := token(p1)
	code, body, _ = callBearer("POST", "/api/v1/auth/logout", access, `{}`)
	expect("logout", code, http.StatusOK, body)
	code, body, _ = callBearer("GET", "/api/v1/user/me", access, "")
	expect("revoked access token", code, http.StatusUnauthorized, body)
	code, body, _ = call("GET", "/api/v1/user/me", &p1, "")
	expect("independent access token", code, http.StatusOK, body)
}
