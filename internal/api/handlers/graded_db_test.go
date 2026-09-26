package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"go.uber.org/zap/zaptest"
)

// these run the graded endpoints against a real postgres (a throwaway database
// per run). same switches as the migration integration test:
// ANVIL_DATABASE_INTEGRATION=1 ANVIL_TEST_DB_{HOST,PORT,USER,PASSWORD,NAME}.
func gradedTestDB(t *testing.T) *database.DB {
	t.Helper()
	if os.Getenv("ANVIL_DATABASE_INTEGRATION") != "1" {
		t.Skip("set ANVIL_DATABASE_INTEGRATION=1 to run graded database tests")
	}
	port, err := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	if err != nil {
		t.Fatalf("invalid ANVIL_TEST_DB_PORT: %v", err)
	}
	base := config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port,
		User: os.Getenv("ANVIL_TEST_DB_USER"), Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"),
		Database: os.Getenv("ANVIL_TEST_DB_NAME"), SSLMode: "disable", MaxOpenConns: 4, MaxIdleConns: 1,
	}
	admin, err := database.New(base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	name := fmt.Sprintf("anvil_graded_%d_test", time.Now().UnixNano())
	if _, err := admin.Pool.Exec(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	cfg := base
	cfg.Database, cfg.MaxOpenConns = name, 16
	db, err := database.New(cfg)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Pool.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type gradedFixture struct {
	t      *testing.T
	db     *database.DB
	h      *GradedHandler
	router *gin.Engine
	now    time.Time
	slug   string
	chalID uuid.UUID
	secret string
	inst   map[uuid.UUID]string // team -> its instance's cr name (${INSTANCE_ID})
}

func newGradedFixture(t *testing.T) *gradedFixture {
	gin.SetMode(gin.TestMode)
	db := gradedTestDB(t)
	cfg := &config.Config{
		Graded:   config.GradedConfig{ReportURL: "https://ctf.example/api/v1/graded/report", ReportsPerMinute: 1000},
		Economy:  shippingEconomy(),
		Platform: config.PlatformConfig{ScoreboardEnabled: true},
	}
	cfg.Economy.CleanRefundFrac = 0.5
	cfg.Economy.TimerSteps = []float64{12, 27, 66, 120}
	cfg.Economy.StepMinutes = 10
	cfg.Economy.ConcurrencyCap = 3
	f := &gradedFixture{t: t, db: db, h: NewGradedHandler(cfg, db, zaptest.NewLogger(t)), now: time.Now(), slug: "deep-dive",
		inst: map[uuid.UUID]string{}}
	f.h.now = func() time.Time { return f.now }

	f.chalID = f.challenge(f.slug, "graded", "insane", 500)
	f.router = gin.New()
	f.router.POST("/api/v1/graded/evaluate", f.h.Evaluate)
	f.router.POST("/api/v1/graded/report", f.h.Report)
	auth := func(c *gin.Context) {
		if id, err := uuid.Parse(c.GetHeader("X-Test-User")); err == nil {
			c.Set("user_id", id)
			c.Set("role", c.GetHeader("X-Test-Role"))
		}
	}
	challengeHandler := NewChallengeHandler(cfg, db, nil, nil, nil, zap.NewNop())
	adminChallenges := NewAdminChallengeHandler(cfg, db, nil, zap.NewNop())
	f.router.GET("/api/v1/challenges", auth, challengeHandler.List)
	f.router.GET("/api/v1/challenges/:slug", auth, challengeHandler.Get)
	f.router.GET("/api/v1/challenges/:slug/graded", auth, f.h.Race)
	f.router.GET("/api/v1/admin/challenges", auth, adminChallenges.List)
	f.router.GET("/api/v1/admin/challenges/:id", auth, adminChallenges.Get)
	f.router.GET("/api/v1/admin/challenges/:id/graded", auth, f.h.AdminInfo)
	f.router.POST("/api/v1/admin/challenges/:id/graded/rotate", auth, f.h.AdminRotate)
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT graded_secret FROM challenges WHERE id = $1`, f.chalID).Scan(&f.secret); err != nil {
		t.Fatalf("load secret: %v", err)
	}
	return f
}

func (f *gradedFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *gradedFixture) challenge(slug, mode, difficulty string, points int) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := f.db.Pool.QueryRow(context.Background(),
		`INSERT INTO challenges (name, slug, difficulty, container_image, base_points, status, scoring_mode, release_date)
		 VALUES ($1, $1, $2, '', $3, 'published', $4, NOW() - INTERVAL '1 hour') RETURNING id`,
		slug, difficulty, points, mode).Scan(&id); err != nil {
		f.t.Fatalf("create challenge: %v", err)
	}
	return id
}

func (f *gradedFixture) team(name string) (team, user uuid.UUID) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.db.Pool.QueryRow(ctx,
		`INSERT INTO teams (name, join_code) VALUES ($1, $1) RETURNING id`, name).Scan(&team); err != nil {
		f.t.Fatalf("create team: %v", err)
	}
	if err := f.db.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, team_id) VALUES ($1, $2, $3) RETURNING id`,
		name+"-player", name+"@example.test", team).Scan(&user); err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	// the team's running instance of the graded challenge, as provisionInstance leaves it
	f.inst[team] = "inst" + strings.ReplaceAll(team.String(), "-", "")[:12]
	f.exec(`INSERT INTO instances (challenge_id, user_id, team_id, container_id, status, expires_at)
		VALUES ($1, $2, $3, $4, 'running', NOW() + INTERVAL '1 hour')`, f.chalID, user, team, f.inst[team])
	return team, user
}

func gradedNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type gradedCall struct {
	path, slug, secret, instance, nonce, body string
	ts                                        time.Time
}

func (f *gradedFixture) send(c gradedCall) (int, map[string]any) {
	f.t.Helper()
	if c.slug == "" {
		c.slug = f.slug
	}
	if c.secret == "" {
		c.secret = f.secret
	}
	if c.nonce == "" {
		c.nonce = gradedNonce()
	}
	if c.ts.IsZero() {
		c.ts = f.now
	}
	ts := strconv.FormatInt(c.ts.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Anvil-Challenge", c.slug)
	req.Header.Set("X-Anvil-Instance", c.instance)
	req.Header.Set("X-Anvil-Timestamp", ts)
	req.Header.Set("X-Anvil-Nonce", c.nonce)
	// the grader holds ${GRADER_SECRET}: the challenge secret narrowed to its instance
	req.Header.Set("X-Anvil-Signature", gradedSignature(graderKey(c.secret, c.instance), ts, c.nonce, []byte(c.body)))
	return f.serve(req)
}

func (f *gradedFixture) serve(req *http.Request) (int, map[string]any) {
	f.t.Helper()
	res := httptest.NewRecorder()
	f.router.ServeHTTP(res, req)
	if strings.Contains(res.Body.String(), f.secret) && !strings.Contains(req.URL.Path, "/admin/") {
		f.t.Fatalf("%s %s leaked the grader secret: %s", req.Method, req.URL.Path, res.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body
}

func (f *gradedFixture) get(path string, user uuid.UUID, role string) (int, map[string]any, string) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-User", user.String())
	req.Header.Set("X-Test-Role", role)
	res := httptest.NewRecorder()
	f.router.ServeHTTP(res, req)
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body, res.Body.String()
}

func (f *gradedFixture) evaluate(team uuid.UUID, evalID string) (int, map[string]any) {
	return f.send(gradedCall{path: "/api/v1/graded/evaluate", instance: f.inst[team],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":%q}`, team, evalID)})
}

func (f *gradedFixture) report(team uuid.UUID, evalID, key, status string, score float64) (int, map[string]any) {
	return f.send(gradedCall{path: "/api/v1/graded/report", instance: f.inst[team],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":%q,"status":%q,"score":%v,"idempotency_key":%q,"raw":{"depth":%v,"of":10}}`,
			team, evalID, status, score, key, score*10)})
}

// cooldown pretends the team's evaluations (and its launch) happened long enough ago.
func (f *gradedFixture) cooldown(team uuid.UUID) {
	f.exec(`UPDATE graded_evaluations SET created_at = created_at - INTERVAL '11 minutes' WHERE team_id = $1`, team)
	f.exec(`UPDATE economy_challenge_state SET opened_at = opened_at - INTERVAL '11 minutes' WHERE team_id = $1`, team)
}

func (f *gradedFixture) teamScore(team uuid.UUID) int {
	f.t.Helper()
	var score int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT total_score FROM teams WHERE id = $1`, team).Scan(&score); err != nil {
		f.t.Fatalf("team score: %v", err)
	}
	return score
}

func (f *gradedFixture) credits(team uuid.UUID) float64 {
	f.t.Helper()
	var credits float64
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT credits FROM economy_team_score WHERE team_id = $1`, team).Scan(&credits); err != nil {
		f.t.Fatalf("credits: %v", err)
	}
	return credits
}

func expectStatus(t *testing.T, label string, got int, body map[string]any, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d (%v), want %d", label, got, body, want)
	}
}

func TestGradedFlowAgainstPostgres(t *testing.T) {
	f := newGradedFixture(t)
	alpha, alphaUser := f.team("alpha")

	// evaluate -> report, the happy path; non-economy scoring moves teams.total_score
	code, body := f.evaluate(alpha, "a-1")
	expectStatus(t, "evaluate 1", code, body, http.StatusOK)
	if body["eval_id"] != "a-1" || body["charged"] != 0.0 || body["evaluations_used"] != 1.0 {
		t.Fatalf("evaluate 1 = %v", body)
	}
	code, body = f.evaluate(alpha, "a-2")
	expectStatus(t, "evaluate while one is running", code, body, http.StatusTooManyRequests)
	if body["retry_after"] == nil {
		t.Fatalf("in-flight 429 without retry_after: %v", body)
	}
	code, body = f.evaluate(alpha, "a-1")
	expectStatus(t, "evaluate retry", code, body, http.StatusOK)
	if body["evaluations_used"] != 1.0 {
		t.Fatalf("evaluate retry = %v", body)
	}

	code, body = f.report(alpha, "a-1", "r-1", "ok", 0.4)
	expectStatus(t, "report 0.4", code, body, http.StatusOK)
	if body["best"] != 0.4 || body["improved"] != true {
		t.Fatalf("report 0.4 = %v", body)
	}
	if got := f.teamScore(alpha); got != 200 {
		t.Fatalf("team score = %d, want 200", got)
	}

	// idempotency: same key is a no-op; a new key for a closed evaluation is refused
	code, body = f.report(alpha, "a-1", "r-1", "ok", 0.4)
	expectStatus(t, "report retry", code, body, http.StatusOK)
	if body["best"] != 0.4 || body["improved"] != false {
		t.Fatalf("report retry = %v", body)
	}
	code, body = f.report(alpha, "a-1", "r-1b", "ok", 0.9)
	expectStatus(t, "second verdict", code, body, http.StatusConflict)
	code, body = f.report(alpha, "nope", "r-x", "ok", 0.9)
	expectStatus(t, "unknown evaluation", code, body, http.StatusNotFound)

	// nonce replay (evaluate and report share one nonce space)
	nonce := gradedNonce()
	f.cooldown(alpha)
	code, body = f.send(gradedCall{path: "/api/v1/graded/evaluate", nonce: nonce, instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"a-2"}`, alpha)})
	expectStatus(t, "evaluate 2", code, body, http.StatusOK)
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", nonce: nonce, instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"a-2","score":1,"idempotency_key":"r-2"}`, alpha)})
	expectStatus(t, "replayed nonce", code, body, http.StatusConflict)

	// monotonic: a worse run changes nothing
	code, body = f.report(alpha, "a-2", "r-2", "ok", 0.3)
	expectStatus(t, "report 0.3", code, body, http.StatusOK)
	if body["best"] != 0.4 || body["improved"] != false || f.teamScore(alpha) != 200 {
		t.Fatalf("worse report moved the best: %v score %d", body, f.teamScore(alpha))
	}
	code, body = f.evaluate(alpha, "a-3")
	expectStatus(t, "cooldown", code, body, http.StatusTooManyRequests)
	if ra, _ := body["retry_after"].(float64); ra < 500 || ra > 600 {
		t.Fatalf("cooldown retry_after = %v", body["retry_after"])
	}
	f.cooldown(alpha)
	code, body = f.evaluate(alpha, "a-3")
	expectStatus(t, "evaluate 3", code, body, http.StatusOK)
	code, body = f.report(alpha, "a-3", "r-3", "ok", 0.9)
	expectStatus(t, "report 0.9", code, body, http.StatusOK)
	if body["best"] != 0.9 || body["improved"] != true || f.teamScore(alpha) != 450 {
		t.Fatalf("report 0.9 = %v, score %d (want 450)", body, f.teamScore(alpha))
	}

	// infra_error closes the evaluation without scoring; it neither counts nor cools down
	f.cooldown(alpha)
	code, body = f.evaluate(alpha, "a-4")
	expectStatus(t, "evaluate 4", code, body, http.StatusOK)
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"a-4","status":"infra_error","idempotency_key":"r-4"}`, alpha)})
	expectStatus(t, "infra error", code, body, http.StatusOK)
	if body["best"] != 0.9 || body["improved"] != false {
		t.Fatalf("infra error = %v", body)
	}
	code, body = f.evaluate(alpha, "a-5")
	expectStatus(t, "evaluate after infra error", code, body, http.StatusOK)
	if body["evaluations_used"] != 4.0 {
		t.Fatalf("infra error counted: %v", body)
	}

	// an unreported evaluation expires; its late verdict is refused
	f.exec(`UPDATE graded_evaluations SET created_at = NOW() - INTERVAL '16 minutes' WHERE eval_id = 'a-5'`)
	code, body = f.report(alpha, "a-5", "r-5", "ok", 1)
	expectStatus(t, "late verdict", code, body, http.StatusConflict)
	if err := SweepGraded(context.Background(), f.db); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var status string
	_ = f.db.Pool.QueryRow(context.Background(), `SELECT status FROM graded_evaluations WHERE eval_id = 'a-5'`).Scan(&status)
	if status != "expired" {
		t.Fatalf("stale evaluation status = %q, want expired", status)
	}

	// rejects: bounds, signature, clock, team, mode
	f.cooldown(alpha)
	code, body = f.evaluate(alpha, "a-6")
	expectStatus(t, "evaluate 6", code, body, http.StatusOK)
	code, body = f.report(alpha, "a-6", "r-6", "ok", 1.5)
	expectStatus(t, "score above 1", code, body, http.StatusBadRequest)
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", secret: strings.Repeat("0", 64), instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"a-6","score":1,"idempotency_key":"r-6"}`, alpha)})
	expectStatus(t, "forged signature", code, body, http.StatusUnauthorized)
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", ts: f.now.Add(-3 * time.Minute), instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"a-6","score":1,"idempotency_key":"r-6"}`, alpha)})
	expectStatus(t, "stale timestamp", code, body, http.StatusUnauthorized)
	code, body = f.send(gradedCall{path: "/api/v1/graded/evaluate", instance: "inst-no-such-launch",
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"ghost-1"}`, alpha)})
	expectStatus(t, "unknown instance", code, body, http.StatusForbidden)
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", slug: "no-such-challenge", instance: f.inst[alpha],
		body: `{"team_id":"7d9f3a52-5c1e-4f0e-9a3b-2b6c8d1e4f70","eval_id":"x","score":1,"idempotency_key":"k"}`})
	expectStatus(t, "unknown challenge", code, body, http.StatusNotFound)
	f.challenge("plain-flag", "flag", "easy", 100)
	var plainSecret string
	_ = f.db.Pool.QueryRow(context.Background(), `SELECT graded_secret FROM challenges WHERE slug = 'plain-flag'`).Scan(&plainSecret)
	code, body = f.send(gradedCall{path: "/api/v1/graded/evaluate", slug: "plain-flag", secret: plainSecret, instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"p-1"}`, alpha)})
	expectStatus(t, "flag-mode challenge", code, body, http.StatusForbidden)

	// staff test with admin accounts: their launches carry no team_id, but the
	// instance is keyed on (and speaks for) the admin's team
	staff, staffUser := f.team("staff")
	f.exec(`UPDATE instances SET team_id = NULL WHERE team_id = $1`, staff)
	f.exec(`UPDATE users SET role = 'admin' WHERE id = $1`, staffUser)
	code, body = f.evaluate(staff, "s-1")
	expectStatus(t, "admin-launched instance", code, body, http.StatusOK)

	// a key already spent by another team is a grader bug, not a retry
	bravo, _ := f.team("bravo")

	// an instance speaks only for its owner, and only under its own key
	code, body = f.send(gradedCall{path: "/api/v1/graded/evaluate", instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"steal-1"}`, bravo)})
	expectStatus(t, "team_id of another team", code, body, http.StatusForbidden)
	ts := strconv.FormatInt(f.now.Unix(), 10)
	stolen := httptest.NewRequest(http.MethodPost, "/api/v1/graded/evaluate",
		strings.NewReader(fmt.Sprintf(`{"team_id":%q,"eval_id":"steal-2"}`, bravo)))
	stolenNonce := gradedNonce()
	stolen.Header.Set("X-Anvil-Challenge", f.slug)
	stolen.Header.Set("X-Anvil-Instance", f.inst[bravo])
	stolen.Header.Set("X-Anvil-Timestamp", ts)
	stolen.Header.Set("X-Anvil-Nonce", stolenNonce)
	stolen.Header.Set("X-Anvil-Signature", gradedSignature(graderKey(f.secret, f.inst[alpha]), ts, stolenNonce,
		[]byte(fmt.Sprintf(`{"team_id":%q,"eval_id":"steal-2"}`, bravo))))
	code, body = f.serve(stolen)
	expectStatus(t, "alpha's key on bravo's instance", code, body, http.StatusUnauthorized)

	code, body = f.evaluate(bravo, "b-1")
	expectStatus(t, "bravo evaluate", code, body, http.StatusOK)
	code, body = f.report(bravo, "b-1", "r-1", "ok", 0.5)
	expectStatus(t, "foreign idempotency key", code, body, http.StatusConflict)
	code, body = f.evaluate(bravo, "a-6")
	expectStatus(t, "foreign eval_id", code, body, http.StatusConflict)
	code, body = f.report(bravo, "b-1", "rb-1", "ok", 0.5)
	expectStatus(t, "bravo report", code, body, http.StatusOK)

	// the depth race: own best + raw + evaluations, field bars, no secret anywhere
	code, body, raw := f.get("/api/v1/challenges/deep-dive/graded", alphaUser, "user")
	expectStatus(t, "race", code, body, http.StatusOK)
	own, _ := body["own"].(map[string]any)
	if own == nil || own["best"] != 0.9 || own["rank"] != 1.0 || own["points"] != 450.0 {
		t.Fatalf("race own = %v", body["own"])
	}
	if metric, _ := own["raw"].(map[string]any); metric["depth"] != 9.0 || metric["of"] != 10.0 {
		t.Fatalf("race is missing the own raw metric: %s", raw)
	}
	// the admin-only staff team evaluated too, but test teams stay off public boards
	if top, _ := body["top"].([]any); len(top) != 2 || body["teams"] != 2.0 || body["attempted"] != 2.0 {
		t.Fatalf("race field = %s", raw)
	}
	ev, _ := body["evaluations"].(map[string]any)
	if ev == nil || ev["in_flight"] != true || ev["used"] != 4.0 {
		t.Fatalf("race evaluations = %v", body["evaluations"])
	}
	if strings.Contains(raw, "rb-1") || strings.Contains(raw, `"depth":5`) {
		t.Fatalf("race leaked another team's report detail: %s", raw)
	}
	f.exec(`UPDATE platform_settings SET value = 'true'::jsonb WHERE key = 'scoreboard_frozen'`)
	_, body, _ = f.get("/api/v1/challenges/deep-dive/graded", alphaUser, "user")
	if top, _ := body["top"].([]any); len(top) != 0 || body["hidden"] != true || body["own"] == nil {
		t.Fatalf("frozen race = %v", body)
	}
	f.exec(`UPDATE platform_settings SET value = 'false'::jsonb WHERE key = 'scoreboard_frozen'`)

	// list/detail carry the own best; admin reads show the secret only on the grading panel
	_, list, listRaw := f.get("/api/v1/challenges", alphaUser, "user")
	if !strings.Contains(listRaw, `"graded_best":0.9`) || !strings.Contains(listRaw, `"scoring_mode":"graded"`) {
		t.Fatalf("challenge list = %v", list)
	}
	_, _, detailRaw := f.get("/api/v1/challenges/deep-dive", alphaUser, "user")
	if !strings.Contains(detailRaw, `"graded_best":0.9`) || strings.Contains(detailRaw, f.secret) {
		t.Fatalf("challenge detail = %s", detailRaw)
	}
	for _, path := range []string{"/api/v1/admin/challenges", "/api/v1/admin/challenges/" + f.chalID.String()} {
		if _, _, raw := f.get(path, alphaUser, "admin"); strings.Contains(raw, f.secret) || strings.Contains(raw, "graded_secret") {
			t.Fatalf("%s exposes the secret: %s", path, raw)
		}
	}
	_, info, infoRaw := f.get("/api/v1/admin/challenges/"+f.chalID.String()+"/graded", alphaUser, "admin")
	if info["secret"] != f.secret || info["scored_teams"] != 2.0 || !strings.Contains(infoRaw, `"eval_id":"a-3"`) {
		t.Fatalf("admin grading panel = %s", infoRaw)
	}

	// rotation: the old secret stops working at once
	old := f.secret
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/challenges/"+f.chalID.String()+"/graded/rotate", nil)
	req.Header.Set("X-Test-User", alphaUser.String())
	f.secret = "rotating"
	code, body = f.serve(req)
	expectStatus(t, "rotate", code, body, http.StatusOK)
	if f.secret, _ = body["secret"].(string); f.secret == old || len(f.secret) != 64 {
		t.Fatalf("rotate = %v", body)
	}
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", secret: old, instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"a-6","score":1,"idempotency_key":"r-6"}`, alpha)})
	expectStatus(t, "old secret", code, body, http.StatusUnauthorized)
	code, body = f.report(alpha, "a-6", "r-6", "ok", 1)
	expectStatus(t, "new secret", code, body, http.StatusOK)
	if f.teamScore(alpha) != 500 {
		t.Fatalf("full depth score = %d, want 500", f.teamScore(alpha))
	}
}

func TestGradedPhasesAgainstPostgres(t *testing.T) {
	f := newGradedFixture(t)
	alpha, _ := f.team("alpha")
	setWindow := func(start, end time.Time) {
		f.exec(`UPDATE platform_settings SET value = to_jsonb($1::text) WHERE key = 'event.start_at'`, start.UTC().Format(time.RFC3339))
		f.exec(`UPDATE platform_settings SET value = to_jsonb($1::text) WHERE key = 'event.end_at'`, end.UTC().Format(time.RFC3339))
	}

	setWindow(f.now.Add(time.Hour), f.now.Add(2*time.Hour))
	code, body := f.evaluate(alpha, "s-1")
	expectStatus(t, "before the start", code, body, http.StatusForbidden)

	setWindow(f.now.Add(-2*time.Hour), f.now.Add(time.Hour))
	code, body = f.evaluate(alpha, "l-1")
	expectStatus(t, "live evaluate", code, body, http.StatusOK)
	code, body = f.report(alpha, "l-1", "l-1", "ok", 0.5)
	expectStatus(t, "live report", code, body, http.StatusOK)

	setWindow(f.now.Add(-3*time.Hour), f.now.Add(-time.Hour))
	f.cooldown(alpha)
	code, body = f.evaluate(alpha, "e-1")
	expectStatus(t, "practice evaluate", code, body, http.StatusOK)
	code, body = f.report(alpha, "e-1", "e-1", "ok", 1)
	expectStatus(t, "practice report", code, body, http.StatusOK)
	if body["practice"] != true || body["best"] != 0.5 || body["improved"] != false || f.teamScore(alpha) != 250 {
		t.Fatalf("practice report = %v, score %d", body, f.teamScore(alpha))
	}

	// the per-(challenge, team) brake
	f.h.limiter = newGradedLimiter(1, time.Minute)
	code, body = f.evaluate(alpha, "e-2")
	expectStatus(t, "first call in window", code, body, http.StatusTooManyRequests) // cooldown
	code, body = f.evaluate(alpha, "e-3")
	expectStatus(t, "rate limited", code, body, http.StatusTooManyRequests)
	if body["error"] != "too many grader calls for this team" {
		t.Fatalf("rate limit = %v", body)
	}
}

func TestGradedEconomyAgainstPostgres(t *testing.T) {
	f := newGradedFixture(t)
	f.exec(`UPDATE platform_settings SET value = 'true'::jsonb WHERE key = 'economy_mode'`)
	alpha, alphaUser := f.team("alpha")
	charlie, charlieUser := f.team("charlie")

	code, body := f.evaluate(alpha, "x-0")
	expectStatus(t, "evaluate before launch", code, body, http.StatusForbidden)

	open := func(team uuid.UUID) {
		ctx := context.Background()
		tx, err := f.db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if opErr := openChallengeEconomy(ctx, tx, team, f.chalID, "insane", f.h.config.Economy); opErr != nil {
			t.Fatalf("open: %v", opErr.Message)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	open(alpha)

	// three free evaluations, then 0.2 x 250 x 1.5^(k-4)
	for k := 1; k <= 3; k++ {
		code, body = f.evaluate(alpha, fmt.Sprintf("x-%d", k))
		expectStatus(t, fmt.Sprintf("free evaluation %d", k), code, body, http.StatusOK)
		if body["charged"] != 0.0 {
			t.Fatalf("free evaluation %d charged %v", k, body["charged"])
		}
		code, body = f.report(alpha, fmt.Sprintf("x-%d", k), fmt.Sprintf("x-%d", k), "ok", 0.1*float64(k))
		expectStatus(t, fmt.Sprintf("report %d", k), code, body, http.StatusOK)
		f.cooldown(alpha)
	}
	start := f.credits(alpha)
	code, body = f.evaluate(alpha, "x-4")
	expectStatus(t, "paid evaluation", code, body, http.StatusOK)
	if body["charged"] != 50.0 || body["evaluations_used"] != 4.0 || f.credits(alpha) != start-50 {
		t.Fatalf("paid evaluation = %v, credits %v (from %v)", body, f.credits(alpha), start)
	}
	// infra_error refunds the charge, and the retry is priced the same
	code, body = f.send(gradedCall{path: "/api/v1/graded/report", instance: f.inst[alpha],
		body: fmt.Sprintf(`{"team_id":%q,"eval_id":"x-4","status":"infra_error","idempotency_key":"x-4"}`, alpha)})
	expectStatus(t, "infra error", code, body, http.StatusOK)
	if body["refunded"] != 50.0 || f.credits(alpha) != start {
		t.Fatalf("infra error = %v, credits %v", body, f.credits(alpha))
	}
	code, body = f.evaluate(alpha, "x-5")
	expectStatus(t, "re-priced evaluation", code, body, http.StatusOK)
	if body["charged"] != 50.0 {
		t.Fatalf("retry after infra error charged %v", body["charged"])
	}
	code, body = f.report(alpha, "x-5", "x-5", "ok", 0.6)
	expectStatus(t, "economy report", code, body, http.StatusOK)
	var frac, points float64
	_ = f.db.Pool.QueryRow(context.Background(),
		`SELECT frac FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`, alpha, f.chalID).Scan(&frac)
	_ = f.db.Pool.QueryRow(context.Background(), `SELECT points FROM economy_team_score WHERE team_id = $1`, alpha).Scan(&points)
	if frac != 0.6 || points <= 0 || f.teamScore(alpha) != 0 {
		t.Fatalf("economy scoring: frac %v points %v team score %d", frac, points, f.teamScore(alpha))
	}

	// an evaluation the grader never answers is refunded by the janitor
	f.cooldown(alpha)
	code, body = f.evaluate(alpha, "x-6")
	expectStatus(t, "evaluation 6", code, body, http.StatusOK)
	charged, _ := body["charged"].(float64)
	before := f.credits(alpha)
	f.exec(`UPDATE graded_evaluations SET created_at = NOW() - INTERVAL '16 minutes' WHERE eval_id = 'x-6'`)
	if err := SweepGraded(context.Background(), f.db); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if charged != 75 || f.credits(alpha) != before+charged {
		t.Fatalf("expired evaluation charged %v, credits %v -> %v", charged, before, f.credits(alpha))
	}

	// can't pay, can't evaluate
	f.cooldown(alpha)
	f.exec(`UPDATE economy_team_score SET credits = 10 WHERE team_id = $1`, alpha)
	code, body = f.evaluate(alpha, "x-7")
	expectStatus(t, "insufficient credits", code, body, http.StatusPaymentRequired)

	// a team that hasn't launched sees only counts; its own view is still there
	_, race, raw := f.get("/api/v1/challenges/deep-dive/graded", charlieUser, "user")
	if race["gated"] != true || race["teams"] != 1.0 || len(race["top"].([]any)) != 0 {
		t.Fatalf("gated race = %s", raw)
	}
	open(charlie)
	_, race, raw = f.get("/api/v1/challenges/deep-dive/graded", charlieUser, "user")
	if race["gated"] != false || len(race["top"].([]any)) != 1 {
		t.Fatalf("launched race = %s", raw)
	}
	_, race, _ = f.get("/api/v1/challenges/deep-dive/graded", alphaUser, "user")
	ev, _ := race["evaluations"].(map[string]any)
	if ev == nil || ev["free_left"] != 0.0 || ev["next_cost"] != 75.0 {
		t.Fatalf("own evaluation state = %v", race["evaluations"])
	}
	if own, _ := race["own"].(map[string]any); own == nil || own["points"] != nil {
		t.Fatalf("economy own = %v (points only exist outside the economy)", race["own"])
	}
	_ = charlie
}

func TestGradedConcurrencyAgainstPostgres(t *testing.T) {
	f := newGradedFixture(t)
	f.exec(`UPDATE platform_settings SET value = 'true'::jsonb WHERE key = 'economy_mode'`)

	// a burst of evaluate calls for one team admits exactly one
	alpha, _ := f.team("alpha")
	ctx := context.Background()
	openTeam := func(team uuid.UUID) {
		tx, err := f.db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if opErr := openChallengeEconomy(ctx, tx, team, f.chalID, "insane", f.h.config.Economy); opErr != nil {
			t.Fatalf("open: %v", opErr.Message)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	openTeam(alpha)
	codes := make(chan int, 12)
	for i := 0; i < cap(codes); i++ {
		go func(i int) {
			code, _ := f.evaluate(alpha, fmt.Sprintf("burst-%d", i))
			codes <- code
		}(i)
	}
	admitted := 0
	for i := 0; i < cap(codes); i++ {
		switch code := <-codes; code {
		case http.StatusOK:
			admitted++
		case http.StatusTooManyRequests:
		default:
			t.Fatalf("burst evaluate returned %d", code)
		}
	}
	if admitted != 1 {
		t.Fatalf("admitted %d evaluations from one burst, want 1", admitted)
	}

	// many teams improving at once must not deadlock the economy repricing
	const teams = 8
	ids := make([]uuid.UUID, teams)
	for i := range ids {
		ids[i], _ = f.team(fmt.Sprintf("team-%d", i))
		openTeam(ids[i])
		if code, body := f.evaluate(ids[i], fmt.Sprintf("t%d", i)); code != http.StatusOK {
			t.Fatalf("team %d evaluate: %d %v", i, code, body)
		}
	}
	done := make(chan int, teams)
	for i := range ids {
		go func(i int) {
			code, _ := f.report(ids[i], fmt.Sprintf("t%d", i), fmt.Sprintf("t%d", i), "ok", 0.1+0.1*float64(i))
			done <- code
		}(i)
	}
	for range ids {
		if code := <-done; code != http.StatusOK {
			t.Fatalf("concurrent report returned %d", code)
		}
	}
	var holders int
	var crowd float64
	if err := f.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(frac), 0) FROM economy_challenge_state WHERE challenge_id = $1 AND holds_solve`,
		f.chalID).Scan(&holders, &crowd); err != nil {
		t.Fatal(err)
	}
	if holders != teams || crowd < 3.59 || crowd > 3.61 {
		t.Fatalf("holders %d crowd %.3f, want %d / 3.6", holders, crowd, teams)
	}
}
