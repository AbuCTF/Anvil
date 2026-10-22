//go:build integration

package game

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func testDB(t *testing.T) *database.DB {
	t.Helper()
	databaseName := os.Getenv("ANVIL_TEST_DB_NAME")
	if err := validateDestructiveTestDatabase(databaseName, os.Getenv("ANVIL_INTEGRATION_ALLOW_DESTRUCTIVE")); err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(envOr("ANVIL_TEST_DB_PORT", "55432"))
	dbCfg := config.DatabaseConfig{
		Host:         envOr("ANVIL_TEST_DB_HOST", "127.0.0.1"),
		Port:         port,
		User:         envOr("ANVIL_TEST_DB_USER", "anvil"),
		Password:     envOr("ANVIL_TEST_DB_PASSWORD", "test"),
		Database:     databaseName,
		SSLMode:      "disable",
		MaxOpenConns: 10,
		MaxIdleConns: 2,
	}

	var db *database.DB
	var err error
	for i := 0; i < 40; i++ {
		if db, err = database.New(dbCfg); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect after retries (is the test postgres up?): %v", err)
	}
	var connectedDatabase string
	if err := db.Pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&connectedDatabase); err != nil {
		db.Close()
		t.Fatalf("verify integration database: %v", err)
	}
	if connectedDatabase != databaseName || !strings.HasSuffix(connectedDatabase, "_test") {
		db.Close()
		t.Fatalf("refusing to truncate connected database %q", connectedDatabase)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, err = db.Pool.Exec(context.Background(),
		`TRUNCATE game_teams, game_services, game_ticks, game_koth_hills, game_koth_rounds, users, teams CASCADE`)
	if err == nil {
		// back to the migration defaults rather than deleting rows other code expects
		_, err = db.Pool.Exec(context.Background(),
			`DELETE FROM platform_settings WHERE key = 'game.state';
			 UPDATE platform_settings SET value = '""'::jsonb WHERE key IN ('event.start_at', 'event.end_at');
			 UPDATE platform_settings SET value = 'false'::jsonb WHERE key = 'koth_native_enabled'`)
	}
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

func validateDestructiveTestDatabase(databaseName, allow string) error {
	if allow != "1" {
		return fmt.Errorf("refusing destructive integration test without ANVIL_INTEGRATION_ALLOW_DESTRUCTIVE=1")
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return fmt.Errorf("refusing destructive integration test against %q; ANVIL_TEST_DB_NAME must end in _test", databaseName)
	}
	return nil
}

func TestValidateDestructiveTestDatabase(t *testing.T) {
	for _, tt := range []struct {
		name, database, allow string
		wantErr               bool
	}{
		{name: "explicit test database", database: "anvil_integration_test", allow: "1"},
		{name: "missing opt in", database: "anvil_integration_test", wantErr: true},
		{name: "production-shaped name", database: "anvil", allow: "1", wantErr: true},
		{name: "missing name", allow: "1", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDestructiveTestDatabase(tt.database, tt.allow)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIntegrationCheckerDiagnosticsSanitizedBeforePersistence(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	team := seedTeam(t, db, "redaction")
	service := seedService(t, db, "/unused")
	if _, err := db.Pool.Exec(ctx, `INSERT INTO game_ticks (tick_number, status) VALUES (1, 'running')`); err != nil {
		t.Fatal(err)
	}
	secret := "eyJhbGciOiJIUzI1NiJ9.PRIVATE_HIDDEN_SENTINEL.signature"
	result := Result{Status: models.SLADown, Code: "bad code", Message: "BERTH-HAVOC-04: " + secret}
	taskID, err := openCheckerTask(db, team, service, Task{Tick: 1, RelatedTick: 1, Method: MethodGetFlag})
	if err != nil {
		t.Fatal(err)
	}
	if err := closeCheckerTask(db, taskID, result); err != nil {
		t.Fatal(err)
	}
	dispatcher := &Dispatcher{db: db}
	if err := dispatcher.recordSLA(1, team, service, result); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"game_checker_tasks", "game_sla_checks"} {
		var code, message string
		if err := db.Pool.QueryRow(ctx, `SELECT COALESCE(code, ''), COALESCE(message, '') FROM `+table+` LIMIT 1`).Scan(&code, &message); err != nil {
			t.Fatal(err)
		}
		if code != "" || message != "Service unavailable" || strings.Contains(message, secret) {
			t.Fatalf("%s persisted code %q, message %q", table, code, message)
		}
	}
}

func okChecker(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ok.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\nprintf '{\"status\":\"OK\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func checkingChecker(t *testing.T) (string, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "actions.log")
	p := filepath.Join(t.TempDir(), "checking.sh")
	body := fmt.Sprintf(`#!/bin/sh
task=$(cat)
printf '%%s\n' "$task" >> %s
case "$task" in
  *'"action":"check"'*) printf '{"status":"FLAG_NOT_FOUND","message":"missing old flag"}' ;;
  *) printf '{"status":"OK"}' ;;
esac
`, logPath)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p, logPath
}

func recordingChecker(t *testing.T, delay time.Duration) (string, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "actions.log")
	p := filepath.Join(t.TempDir(), "recording.sh")
	body := fmt.Sprintf(`#!/bin/sh
task=$(cat)
printf '%%s\n' "$task" >> %q
sleep %.3f
printf '{"status":"OK"}'
`, logPath, delay.Seconds())
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p, logPath
}

func seedTeam(t *testing.T, db *database.DB, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_teams (id, name, slug, token, status, is_nop, vulnbox_ip)
		 VALUES ($1, $2, $2, $2, 'active', false, '127.0.0.1')`, id, name)
	if err != nil {
		t.Fatalf("seed team %s: %v", name, err)
	}
	return id
}

func seedService(t *testing.T, db *database.DB, checker string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_services (id, name, slug, category, tier, port, checker_ref, flag_stores, enabled)
		 VALUES ($1, 'Notes', 'notes', 'misc', 'core', 1, $2, 1, true)`, id, checker)
	if err != nil {
		t.Fatalf("seed service: %v", err)
	}
	return id
}

type integrationCheckerFunc func(context.Context, Target, Task) Result

func (f integrationCheckerFunc) Run(ctx context.Context, target Target, task Task) Result {
	return f(ctx, target, task)
}

func TestIntegrationReferenceAttackBatchSafety(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	serviceID := seedService(t, db, "/unused")
	greenID := seedTeam(t, db, "green")
	staleID := seedTeam(t, db, "stale")
	nopID := seedTeam(t, db, "nop")
	if _, err := db.Pool.Exec(ctx,
		`UPDATE game_teams SET team_number = CASE id WHEN $1 THEN 1 WHEN $2 THEN 2 ELSE 99 END,
		                         is_nop = (id = $3)
		 WHERE id IN ($1, $2, $3)`, greenID, staleID, nopID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_ticks (tick_number, status) VALUES (1, 'closed'), (2, 'closed'), (3, 'running')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status) VALUES
		 (1, $1, $4, 'OK'), (1, $2, $4, 'OK'), (1, $3, $4, 'OK'),
		 (2, $1, $4, 'OK'), (2, $2, $4, 'DOWN'), (2, $3, $4, 'OK')`,
		greenID, staleID, nopID, serviceID); err != nil {
		t.Fatal(err)
	}

	bot := &outbreakBot{
		cfg:    config.GameConfig{FlagPrefix: "H7CTF", TickInterval: 2 * time.Minute},
		db:     db,
		logger: zap.NewNop(),
	}
	eligible, err := bot.greenTeams(ctx, 3, serviceID)
	if err != nil {
		t.Fatalf("green teams: %v", err)
	}
	if len(eligible) != 2 {
		t.Fatalf("eligible teams = %+v, want current-green team and NOP only", eligible)
	}
	for _, team := range eligible {
		if team.id == staleID {
			t.Fatal("team with stale green SLA and latest closed-tick DOWN was eligible")
		}
	}

	flags := make(map[uuid.UUID]outbreakFlag)
	insertFlag := func(teamID uuid.UUID, value string) outbreakFlag {
		t.Helper()
		var flag outbreakFlag
		if err := db.Pool.QueryRow(ctx,
			`INSERT INTO game_flags
			 (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at, attack_info)
			 VALUES (3, $1, $2, 0, $3, 3, 6, NOW(), $4)
			 RETURNING id, flag, attack_info`, teamID, serviceID, value, `{"account":"reference"}`,
		).Scan(&flag.id, &flag.value, &flag.attackInfo); err != nil {
			t.Fatalf("insert flag: %v", err)
		}
		flags[teamID] = flag
		return flag
	}
	insertFlag(nopID, "H7CTF{00000000000000000000000000000001}")

	var calls int
	checker := integrationCheckerFunc(func(_ context.Context, _ Target, task Task) Result {
		calls++
		return Result{Status: models.SLAOk, Message: task.Flag, Flag: task.Flag, Latency: time.Millisecond}
	})
	store := orangeStore{serviceID: serviceID, storeIndex: 0, waveVariant: 0, port: 7001}
	bot.runStore(ctx, 3, store, eligible, checker, time.Second)
	if calls != 0 || count(t, db, `SELECT COUNT(*) FROM game_checker_tasks`) != 0 || count(t, db, `SELECT COUNT(*) FROM game_bot_reads`) != 0 {
		t.Fatal("incomplete flag lookup batch called the checker or persisted an outcome")
	}

	insertFlag(greenID, "H7CTF{00000000000000000000000000000002}")
	calls = 0
	var taskMu sync.Mutex
	var taskIDs []int64
	checker = integrationCheckerFunc(func(_ context.Context, _ Target, task Task) Result {
		taskMu.Lock()
		taskIDs = append(taskIDs, task.ID)
		taskMu.Unlock()
		return Result{Status: models.SLAOk, Message: task.Flag, Flag: task.Flag, Latency: time.Millisecond}
	})
	bot.runStore(ctx, 3, store, eligible, checker, time.Second)
	if len(taskIDs) != 2 || taskIDs[0] <= 0 || taskIDs[1] <= 0 || taskIDs[0] == taskIDs[1] {
		t.Fatalf("reference checker task IDs = %v, want two unique nonzero IDs", taskIDs)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM game_checker_tasks WHERE finished_at IS NOT NULL AND message IS NULL`); got != 2 {
		t.Fatalf("sanitized completed checker tasks = %d, want 2", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM game_bot_reads`); got != 2 {
		t.Fatalf("NOP-approved bot read batch = %d, want 2", got)
	}
	// A MUMBLE exploit result is persisted as FAULTY for audit, but Reference
	// Attack is not an SLA task and must not affect service-health aggregates.
	if _, err := db.Pool.Exec(ctx,
		`UPDATE game_checker_tasks SET status = 'FAULTY', latency_ms = 60000
		 WHERE id = (SELECT MIN(id) FROM game_checker_tasks WHERE method = 'exploit')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_checker_tasks
		   (tick_number, related_tick, team_id, service_id, method, variant, status, latency_ms, finished_at)
		 VALUES
		   (3, 3, $1, $2, 'putflag', 0, 'OK', 10, NOW()),
		   (3, 3, $1, $2, 'getflag', 0, 'FAULTY', 20, NOW())`, greenID, serviceID); err != nil {
		t.Fatal(err)
	}
	metrics, err := QueryMetrics(ctx, db, 2*time.Minute, 5)
	if err != nil {
		t.Fatalf("query metrics: %v", err)
	}
	if len(metrics.Services) != 1 {
		t.Fatalf("service metrics = %+v, want one service", metrics.Services)
	}
	health := metrics.Services[0]
	if health.TasksTotal != 2 || health.TasksOK != 1 || health.ErrorRate != 0.5 || health.LatencyP95Ms > 20 {
		t.Fatalf("service health included Reference Attack tasks: %+v", health)
	}

	if _, err := db.Pool.Exec(ctx, `DELETE FROM game_bot_reads`); err != nil {
		t.Fatal(err)
	}
	valid := flags[greenID]
	err = bot.recordBotReads(3, []botRead{
		{flagID: valid.id, teamID: greenID, outcome: "stolen"},
		{flagID: uuid.New(), teamID: nopID, isNOP: true, outcome: "stolen"},
	})
	if err == nil {
		t.Fatal("atomic bot-read batch with an invalid flag unexpectedly succeeded")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM game_bot_reads`); got != 0 {
		t.Fatalf("failed bot-read transaction persisted %d rows, want 0", got)
	}
}

func kothChecker(t *testing.T, controllerToken string) string {
	t.Helper()
	body := fmt.Sprintf("#!/bin/sh\nline=$(cat)\ncase \"$line\" in\n  *'\"action\":\"control\"'*) printf '{\"controller\":\"%s\"}' ;;\n  *) printf '{}' ;;\nesac\n", controllerToken)
	p := filepath.Join(t.TempDir(), "koth.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func seedHill(t *testing.T, db *database.DB, checker string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_koth_hills (id, name, slug, host, port, checker_ref, reset_seconds, enabled)
		 VALUES ($1, 'Hill', 'hill', '127.0.0.1', 1, $2, 900, true)`, id, checker)
	if err != nil {
		t.Fatalf("seed hill: %v", err)
	}
	return id
}

func TestIntegrationKoth(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	a := seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	seedHill(t, db, kothChecker(t, "alpha"))

	ctrl := testController(db) // round = 2 ticks
	runTickOK(t, ctrl, ctx, 1)
	runTickOK(t, ctrl, ctx, 2)
	runTickOK(t, ctrl, ctx, 3) // enters round 2 -> closes round 1

	if n := count(t, db, `SELECT COUNT(*) FROM game_koth_control WHERE controller_team_id IS NOT NULL`); n != 3 {
		t.Fatalf("expected 3 control records for alpha, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_koth_rounds WHERE round_number = 1 AND status = 'closed'`); n != 1 {
		t.Fatalf("round 1 should be closed exactly once")
	}

	var bonus float64
	if err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(points), 0) FROM game_score_events WHERE team_id = $1 AND stream = 'KOTH'`, a).
		Scan(&bonus); err != nil {
		t.Fatal(err)
	}
	if bonus != 12 {
		t.Errorf("alpha round-1 rank bonus: got %v want 12", bonus)
	}

	// koth = per-tick hold (5 x 3 ticks) + round-1 rank bonus (12) = 27
	if s := getStanding(t, db, a); s.koth != 27 {
		t.Errorf("alpha koth standing: got %v want 27", s.koth)
	}
}

func TestIntegrationKothClosesRoundAfterLastHillIsDisabled(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	a := seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	hill := seedHill(t, db, kothChecker(t, "alpha"))
	ctrl := testController(db)
	runTickOK(t, ctrl, ctx, 1)
	runTickOK(t, ctrl, ctx, 2)
	if _, err := db.Pool.Exec(ctx, `UPDATE game_koth_hills SET enabled = FALSE WHERE id = $1`, hill); err != nil {
		t.Fatalf("disable hill: %v", err)
	}
	runTickOK(t, ctrl, ctx, 3)

	if n := count(t, db, `SELECT COUNT(*) FROM game_koth_rounds WHERE round_number = 1 AND status = 'closed'`); n != 1 {
		t.Fatalf("round 1 remained open after its final hill was disabled")
	}
	var bonus float64
	if err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(points), 0) FROM game_score_events WHERE team_id = $1 AND stream = 'KOTH'`, a).
		Scan(&bonus); err != nil {
		t.Fatal(err)
	}
	if bonus != 12 {
		t.Fatalf("alpha round-1 rank bonus: got %v want 12", bonus)
	}
}

type standingRow struct {
	attack, defense, sla, koth, total float64
	rank                              int
}

func getStanding(t *testing.T, db *database.DB, team uuid.UUID) standingRow {
	t.Helper()
	var s standingRow
	err := db.Pool.QueryRow(context.Background(),
		`SELECT attack, defense, sla, koth, total, COALESCE(rank, 0) FROM game_standings WHERE team_id = $1`, team).
		Scan(&s.attack, &s.defense, &s.sla, &s.koth, &s.total, &s.rank)
	if err != nil {
		t.Fatalf("get standing: %v", err)
	}
	return s
}

func testController(db *database.DB) *Controller {
	return NewController(config.GameConfig{
		Enabled:        true,
		TickInterval:   time.Minute,
		FlagValidTicks: 10,
		FlagPrefix:     "H7CTF",
		Koth:           config.KothConfig{RoundInterval: 2 * time.Minute, ResetEnabled: true},
		Checker:        config.CheckerConfig{TaskTimeout: 30 * time.Second, TailMargin: 10 * time.Second, Concurrency: 8},
		Scoring: config.ScoringConfig{
			AttackBase:    100,
			DefenseFactor: 1,
			SLAPoints:     10,
			KothHold:      5,
			KothRank:      []int{12, 7, 4, 2, 1},
		},
	}, db, zap.NewNop())
}

func runTickOK(t *testing.T, ctrl *Controller, ctx context.Context, tick int) {
	t.Helper()
	ageEarlierTicks(t, ctrl.db, tick)
	if err := ctrl.runTick(ctx, tick); err != nil {
		t.Fatalf("run tick %d: %v", tick, err)
	}
}

// ageEarlierTicks backdates earlier ticks past the cadence gate, which otherwise
// refuses a new tick within one interval of the last and turns back-to-back test
// ticks into silent no-ops.
func ageEarlierTicks(t *testing.T, db *database.DB, tick int) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE game_ticks SET started_at = started_at - interval '1 hour' WHERE tick_number < $1`, tick); err != nil {
		t.Fatalf("age ticks: %v", err)
	}
}

func setSetting(t *testing.T, db *database.DB, key, jsonValue string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(),
		`INSERT INTO platform_settings (key, value) VALUES ($1, $2::jsonb)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, jsonValue); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

// setFinals switches the engine into finals mode, where attack-defense runs.
func setFinals(t *testing.T, db *database.DB) {
	t.Helper()
	setSetting(t, db, "game.state", `"running"`)
}

func count(t *testing.T, db *database.DB, q string) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestIntegrationADLoop(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	a := seedTeam(t, db, "alpha")
	b := seedTeam(t, db, "bravo")
	c := seedTeam(t, db, "charlie")
	seedService(t, db, okChecker(t))

	ctrl := testController(db)
	runTickOK(t, ctrl, ctx, 1)

	if n := count(t, db, `SELECT COUNT(*) FROM game_flags WHERE tick_number = 1`); n != 3 {
		t.Fatalf("expected 3 flags planted, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE status = 'OK'`); n != 3 {
		t.Fatalf("expected 3 OK sla checks, got %d", n)
	}
	if s := getStanding(t, db, a); s.sla <= 0 {
		t.Fatalf("expected positive SLA after an OK tick, got %v", s.sla)
	}

	var flagB string
	if err := db.Pool.QueryRow(ctx, `SELECT flag FROM game_flags WHERE team_id = $1`, b).Scan(&flagB); err != nil {
		t.Fatalf("read bravo's flag: %v", err)
	}

	assertOutcome(t, db, a, flagB, SubmitAccepted)
	assertOutcome(t, db, a, flagB, SubmitDuplicate)
	assertOutcome(t, db, b, flagB, SubmitOwnFlag)
	assertOutcome(t, db, a, "H7CTF{NOPE}", SubmitInvalid)

	// A stale flag (past its window) must be rejected as expired.
	var stale string = "H7CTF{STALE0000}"
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO game_flags (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick)
		 SELECT 1, $1, id, 1, $2, 1, 0 FROM game_services LIMIT 1`, c, stale)
	if err != nil {
		t.Fatalf("insert stale flag: %v", err)
	}
	assertOutcome(t, db, a, stale, SubmitExpired)

	if err := ctrl.recomputeStandings(ctx); err != nil {
		t.Fatalf("recompute standings: %v", err)
	}

	sa := getStanding(t, db, a)
	sb := getStanding(t, db, b)
	if sa.attack != 100 {
		t.Errorf("alpha attack: got %v want 100 (sole captor of one flag)", sa.attack)
	}
	if sb.defense != -1 {
		t.Errorf("bravo defense: got %v want -1 (one flag lost to one team)", sb.defense)
	}
	if sa.total <= sb.total {
		t.Errorf("alpha (%.2f) should outrank bravo (%.2f)", sa.total, sb.total)
	}
	if sa.rank != 1 {
		t.Errorf("alpha should be rank 1, got %d", sa.rank)
	}
}

func TestIntegrationOldFlagLossRecoversAndClosedTickIsNotRepeated(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	// the service keeps only the current tick's flag: old getflags come back MUMBLE
	srv, calls := enoFake(t, func(m enoTaskMessage) enoResultMessage {
		if m.Method == MethodGetFlag && m.RelatedRoundID < m.CurrentRoundID {
			return enoResult("MUMBLE", "flag gone")
		}
		return enoResult("OK", "")
	})
	seedENOService(t, db, srv.URL, 1, 0, 0)

	ctrl := testController(db)
	runTickOK(t, ctrl, ctx, 1)
	runTickOK(t, ctrl, ctx, 2)

	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE tick_number = 1 AND status = 'OK'`); n != 2 {
		t.Fatalf("tick 1 should be OK for both teams, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE tick_number = 2 AND status = 'RECOVERING'`); n != 2 {
		t.Fatalf("lost old flags should mark both teams RECOVERING on tick 2, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_flag_checks fc JOIN game_flags f ON f.id = fc.flag_id
		WHERE fc.tick_number = 2 AND f.tick_number = 1 AND NOT fc.readable`); n != 2 {
		t.Fatalf("old flags read on tick 2 should be recorded unreadable, got %d", n)
	}
	before := calls.Load()
	// put+get per team on tick 1, then put+get+old get per team on tick 2
	if before != 10 {
		t.Fatalf("checker saw %d tasks over two ticks, want 10", before)
	}
	if err := ctrl.runTick(ctx, 2); err != nil {
		t.Fatalf("repeat closed tick: %v", err)
	}
	if after := calls.Load(); after != before {
		t.Fatalf("closed tick dispatched again: %d -> %d tasks", before, after)
	}
}

func TestIntegrationReservedFlagIsNotSubmittable(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	attacker := seedTeam(t, db, "alpha")
	victim := seedTeam(t, db, "bravo")
	service := seedService(t, db, okChecker(t))
	if _, err := db.Pool.Exec(ctx, `INSERT INTO game_ticks (tick_number, status) VALUES (1, 'running')`); err != nil {
		t.Fatalf("seed tick: %v", err)
	}
	reserved := "H7CTF{RESERVED}"
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_flags
		   (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at)
		 VALUES (1, $1, $2, 0, $3, 1, 10, NULL)`, victim, service, reserved); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}
	if outcome, err := SubmitFlag(ctx, db, attacker, reserved); err != nil || outcome != SubmitInvalid {
		t.Fatalf("reserved flag outcome = %q, err = %v; want invalid", outcome, err)
	}
}

func TestIntegrationRunningTickReusesReservedFlag(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	team := seedTeam(t, db, "alpha")
	checker, logPath := recordingChecker(t, 0)
	service := seedService(t, db, checker)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO game_ticks (tick_number, status) VALUES (1, 'running')`); err != nil {
		t.Fatalf("seed tick: %v", err)
	}
	reserved := "H7CTF{STABLE_RESERVATION}"
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_flags
		   (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at)
		 VALUES (1, $1, $2, 0, $3, 1, 10, NULL)`, team, service, reserved); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}

	runTickOK(t, testController(db), ctx, 1)
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags`); n != 1 {
		t.Fatalf("running tick minted a replacement flag; got %d rows", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags WHERE planted_at IS NOT NULL`); n != 1 {
		t.Fatalf("reserved flag was not marked planted")
	}
	actions, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read checker log: %v", err)
	}
	if !strings.Contains(string(actions), reserved) {
		t.Fatalf("checker did not receive reserved flag: %s", actions)
	}
}

func TestIntegrationRunningTickSkipsCompletedJobs(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	completedTeam := seedTeam(t, db, "alpha")
	unfinishedTeam := seedTeam(t, db, "bravo")
	checker, logPath := recordingChecker(t, 0)
	service := seedService(t, db, checker)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO game_ticks (tick_number, status) VALUES (1, 'running')`); err != nil {
		t.Fatalf("seed tick: %v", err)
	}
	completedFlag := "H7CTF{ALREADY_FINISHED}"
	unfinishedFlag := "H7CTF{STILL_PENDING}"
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_flags
		   (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at)
		 VALUES
		   (1, $1, $3, 0, $4, 1, 10, NOW()),
		   (1, $2, $3, 0, $5, 1, 10, NULL)`,
		completedTeam, unfinishedTeam, service, completedFlag, unfinishedFlag); err != nil {
		t.Fatalf("seed flags: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status)
		 VALUES (1, $1, $2, 'OK')`, completedTeam, service); err != nil {
		t.Fatalf("seed completed SLA: %v", err)
	}

	runTickOK(t, testController(db), ctx, 1)
	actions, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read checker log: %v", err)
	}
	if strings.Contains(string(actions), completedFlag) {
		t.Fatalf("completed job was dispatched again: %s", actions)
	}
	if !strings.Contains(string(actions), unfinishedFlag) {
		t.Fatalf("unfinished job was not resumed: %s", actions)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE tick_number = 1`); n != 2 {
		t.Fatalf("resumed tick has %d SLA rows; want 2", n)
	}
}

func TestIntegrationConcurrentControllersDispatchTickOnce(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	checker, logPath := recordingChecker(t, 400*time.Millisecond)
	seedService(t, db, checker)

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, ctrl := range []*Controller{testController(db), testController(db)} {
		go func(ctrl *Controller) {
			<-start
			errs <- ctrl.runTick(ctx, 1)
		}(ctrl)
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent tick: %v", err)
		}
	}

	actions, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read checker log: %v", err)
	}
	// one job per team, each a putflag then its getflag
	if got := strings.Count(strings.TrimSpace(string(actions)), "\n") + 1; got != 4 {
		t.Fatalf("two controllers ran %d checker tasks; want exactly 4", got)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags`); n != 2 {
		t.Fatalf("two controllers persisted %d flags; want exactly 2", n)
	}
}

func assertOutcome(t *testing.T, db *database.DB, team uuid.UUID, flag string, want SubmitOutcome) {
	t.Helper()
	got, err := SubmitFlag(context.Background(), db, team, flag)
	if err != nil {
		t.Fatalf("submit %q: %v", flag, err)
	}
	if got != want {
		t.Errorf("submit %q: got %q want %q", flag, got, want)
	}
}

func TestIntegrationTeamForUser(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	team := seedTeam(t, db, "alpha")
	user := uuid.New()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, password_hash, role, status)
		 VALUES ($1, 'p1', 'p1@example.com', 'x', 'user', 'active')`, user)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_team_members (team_id, user_id, role) VALUES ($1, $2, 'captain')`, team, user); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	got, ok, err := TeamForUser(ctx, db, user)
	if err != nil || !ok || got != team {
		t.Fatalf("TeamForUser: got %v ok=%v err=%v want %v", got, ok, err, team)
	}
	if _, ok, _ := TeamForUser(ctx, db, uuid.New()); ok {
		t.Errorf("unknown user should not resolve to a team")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE game_teams SET status = 'disabled' WHERE id = $1`, team); err != nil {
		t.Fatalf("disable team: %v", err)
	}
	if _, ok, err := TeamForUser(ctx, db, user); err != nil || ok {
		t.Errorf("disabled team resolved for submission: ok=%v err=%v", ok, err)
	}
}

func enoResult(result, message string) enoResultMessage {
	m := enoResultMessage{Result: result}
	if message != "" {
		m.Message = &message
	}
	return m
}

// enoFake is an enowars v2 checker that answers through decide and counts tasks.
func enoFake(t *testing.T, decide func(enoTaskMessage) enoResultMessage) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m enoTaskMessage
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Errorf("decode task: %v", err)
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(decide(m))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func seedENOService(t *testing.T, db *database.DB, url string, flags, noise, havoc int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_services (id, name, slug, category, tier, port, checker_ref, flag_stores,
		   noise_variants, havoc_variants, enabled)
		 VALUES ($1, 'Berth', 'berth', 'web', 'core', 8080, $2, $3, $4, $5, true)`, id, url, flags, noise, havoc)
	if err != nil {
		t.Fatalf("seed eno service: %v", err)
	}
	return id
}

func TestIntegrationLegacyTickNeverDispatchesAD(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	a := seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	seedHill(t, db, kothChecker(t, "alpha"))
	srv, calls := enoFake(t, func(enoTaskMessage) enoResultMessage { return enoResult("OK", "") })
	seedENOService(t, db, srv.URL, 1, 0, 0)

	// game.state unset: the quals path must behave exactly as it always did
	ctrl := testController(db)
	runTickOK(t, ctrl, ctx, 1)
	runTickOK(t, ctrl, ctx, 2)

	if n := calls.Load(); n != 0 {
		t.Fatalf("legacy tick ran %d checker tasks", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags`) + count(t, db, `SELECT COUNT(*) FROM game_sla_checks`); n != 0 {
		t.Fatalf("legacy tick wrote %d flag/sla rows", n)
	}
	if s := getStanding(t, db, a); s.koth != 10 {
		t.Fatalf("legacy koth hold = %v, want 10 (5 x 2 ticks)", s.koth)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_ticks WHERE status = 'closed'`); n != 2 {
		t.Fatalf("legacy ticks closed = %d, want 2", n)
	}
}

func TestIntegrationTickingAllowedFollowsGameState(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	ctrl := testController(db)

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	clear := func() {
		if _, err := db.Pool.Exec(ctx,
			`DELETE FROM platform_settings WHERE key = 'game.state';
			 UPDATE platform_settings SET value = '""'::jsonb WHERE key IN ('event.start_at', 'event.end_at');
			 UPDATE platform_settings SET value = 'false'::jsonb WHERE key = 'koth_native_enabled'`); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name     string
		settings map[string]string
		want     bool
	}{
		{"legacy, koth off", map[string]string{}, false},
		{"legacy, koth on", map[string]string{"koth_native_enabled": "true"}, true},
		{"legacy, koth on but ended", map[string]string{"koth_native_enabled": "true", "event.end_at": `"` + past + `"`}, false},
		{"running", map[string]string{"game.state": `"running"`}, true},
		{"running ignores koth flag", map[string]string{"game.state": `"running"`, "koth_native_enabled": "false"}, true},
		{"running before start", map[string]string{"game.state": `"running"`, "event.start_at": `"` + future + `"`}, false},
		{"running inside window", map[string]string{"game.state": `"running"`, "event.start_at": `"` + past + `"`, "event.end_at": `"` + future + `"`}, true},
		{"running after end", map[string]string{"game.state": `"running"`, "event.end_at": `"` + past + `"`}, false},
		{"paused", map[string]string{"game.state": `"paused"`, "koth_native_enabled": "true"}, false},
		{"stopped", map[string]string{"game.state": `"stopped"`}, false},
		{"typo pauses", map[string]string{"game.state": `"runing"`}, false},
	} {
		clear()
		for k, v := range tt.settings {
			setSetting(t, db, k, v)
		}
		if got := ctrl.tickingAllowed(ctx); got != tt.want {
			t.Errorf("%s: tickingAllowed = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIntegrationENOVariantsAttackInfoAndTaskLog(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	alpha := seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	nop := seedTeam(t, db, "nop")
	if _, err := db.Pool.Exec(ctx, `UPDATE game_teams SET is_nop = TRUE WHERE id = $1`, nop); err != nil {
		t.Fatal(err)
	}
	var chains sync.Map
	srv, calls := enoFake(t, func(m enoTaskMessage) enoResultMessage {
		switch m.Method {
		case MethodPutFlag:
			if m.Flag == nil {
				t.Errorf("putflag without a flag")
			}
			chains.Store(m.TaskChainID, *m.Flag)
			r := enoResult("OK", "")
			info := fmt.Sprintf(`{"user":"u%d"}`, m.VariantID)
			r.AttackInfo = &info
			return r
		case MethodGetFlag:
			if want, ok := chains.Load(m.TaskChainID); !ok || m.Flag == nil || want != *m.Flag {
				return enoResult("MUMBLE", "chain id does not match its putflag")
			}
			return enoResult("OK", "")
		default:
			return enoResult("OK", "")
		}
	})
	seedENOService(t, db, srv.URL, 2, 1, 1)

	runTickOK(t, testController(db), ctx, 1)

	// 3 teams (the nop team included) x 2 flag variants
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags WHERE planted_at IS NOT NULL`); n != 6 {
		t.Fatalf("planted flags = %d, want 6", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags WHERE attack_info = '{"user":"u1"}' AND store_index = 1`); n != 3 {
		t.Fatalf("variant 1 flags with attack info = %d, want 3", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE status = 'OK'`); n != 3 {
		t.Fatalf("OK sla rows = %d, want 3", n)
	}
	// per team: 2 putflag + 2 getflag + putnoise + getnoise + havoc = 7
	if got := calls.Load(); got != 21 {
		t.Fatalf("checker tasks = %d, want 21", got)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_checker_tasks WHERE status = 'OK' AND finished_at IS NOT NULL`); n != 21 {
		t.Fatalf("logged tasks = %d, want 21", n)
	}
	if n := count(t, db, `SELECT COUNT(DISTINCT id) FROM game_checker_tasks`); n != 21 {
		t.Fatalf("task ids are not unique: %d distinct", n)
	}

	// the nop team's flags are stealable so teams can test exploits on it
	var nopFlag string
	if err := db.Pool.QueryRow(ctx, `SELECT flag FROM game_flags WHERE team_id = $1 LIMIT 1`, nop).Scan(&nopFlag); err != nil {
		t.Fatal(err)
	}
	// the nop's flags prove an exploit works but never score
	assertOutcome(t, db, alpha, nopFlag, SubmitNop)
	// every getflag's read is kept per tick for the hold life rule: 3 teams x 2 stores
	if n := count(t, db, `SELECT COUNT(*) FROM game_flag_checks WHERE tick_number = 1 AND readable`); n != 6 {
		t.Fatalf("flag reads recorded = %d, want 6", n)
	}
}

func TestIntegrationHungCheckerDoesNotStallTheTick(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	a := seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	seedHill(t, db, kothChecker(t, "alpha"))
	release := make(chan struct{})
	srv, _ := enoFake(t, func(m enoTaskMessage) enoResultMessage {
		if m.Method == MethodHavoc {
			<-release // hangs well past its timeout
		}
		return enoResult("OK", "")
	})
	// registered after the server so it runs first: Close waits on the hung handler
	t.Cleanup(func() { close(release) })
	seedENOService(t, db, srv.URL, 1, 0, 1)

	ctrl := NewController(config.GameConfig{
		Enabled:        true,
		TickInterval:   8 * time.Second, // quarter = 2 s task timeout
		FlagValidTicks: 5,
		FlagPrefix:     "H7",
		Koth:           config.KothConfig{RoundInterval: time.Minute},
		Checker:        config.CheckerConfig{TaskTimeout: 30 * time.Second, TailMargin: time.Second, Concurrency: 8},
		Scoring:        config.ScoringConfig{AttackBase: 100, DefenseFactor: 1, SLAPoints: 10, KothHold: 5, KothRank: []int{12, 7, 4, 2, 1}},
	}, db, zap.NewNop())

	started := time.Now()
	runTickOK(t, ctrl, ctx, 1)
	if took := time.Since(started); took > 6*time.Second {
		t.Fatalf("tick took %s with one hung checker task", took)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_ticks WHERE tick_number = 1 AND status = 'closed'`); n != 1 {
		t.Fatal("tick 1 did not close")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE status = 'DOWN' AND step = 'havoc'`); n != 2 {
		t.Fatalf("a timed-out havoc should mark both teams DOWN on the havoc step, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_koth_control WHERE controller_team_id = '`+a.String()+`'`); n != 1 {
		t.Fatalf("koth did not score beside the hung checker")
	}
}

func TestIntegrationTeamForUserViaPlatformTeam(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	team := uuid.New()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO teams (id, name, join_code) VALUES ($1, 'Alpha', 'join-alpha')`, team); err != nil {
		t.Fatalf("seed platform team: %v", err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_teams (id, name, slug, status, is_nop, vulnbox_ip) VALUES ($1, 'Alpha', 'alpha', 'active', false, '10.60.1.2')`,
		team); err != nil {
		t.Fatalf("seed game team: %v", err)
	}
	user := uuid.New()
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, password_hash, role, status, team_id)
		 VALUES ($1, 'p2', 'p2@example.com', 'x', 'user', 'active', $2)`, user, team); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	got, ok, err := TeamForUser(ctx, db, user)
	if err != nil || !ok || got != team {
		t.Fatalf("TeamForUser via users.team_id: got %v ok=%v err=%v want %v", got, ok, err, team)
	}
}

func TestIntegrationMirrorScoresThroughTicks(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	alpha := seedTeam(t, db, "alpha")
	bravo := seedTeam(t, db, "bravo")
	charlie := seedTeam(t, db, "charlie")
	nop := seedTeam(t, db, "nop")
	if _, err := db.Pool.Exec(ctx, `UPDATE game_teams SET is_nop = TRUE WHERE id = $1`, nop); err != nil {
		t.Fatal(err)
	}
	srv, _ := enoFake(t, func(enoTaskMessage) enoResultMessage { return enoResult("OK", "") })
	svc := seedENOService(t, db, srv.URL, 1, 0, 0)

	cfg := testController(db).cfg
	cfg.FlagValidTicks = 1 // a flag lives ticks t and t+1
	cfg.Scoring.Formula = "mirror"
	cfg.Scoring.WeightAD, cfg.Scoring.WeightKoth = 0.7, 0.3
	cfg.Scoring.MassFaultTeams, cfg.Scoring.FreeDeploys = 3, 3
	ctrl := NewController(cfg, db, zap.NewNop())

	runTickOK(t, ctrl, ctx, 1)
	var bravoFlag, nopFlag string
	if err := db.Pool.QueryRow(ctx, `SELECT flag FROM game_flags WHERE team_id = $1 AND tick_number = 1`, bravo).Scan(&bravoFlag); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT flag FROM game_flags WHERE team_id = $1 AND tick_number = 1`, nop).Scan(&nopFlag); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, db, alpha, bravoFlag, SubmitAccepted)
	assertOutcome(t, db, alpha, nopFlag, SubmitNop)
	runTickOK(t, ctrl, ctx, 2)
	runTickOK(t, ctrl, ctx, 3) // tick-1 flags (valid through 2) settle while 3 closes

	up := 3 * math.Sqrt(3) // three green ticks, three scored teams, one store
	type row struct{ steal, hold, up, ad float64 }
	get := func(team uuid.UUID) row {
		var r row
		if err := db.Pool.QueryRow(ctx,
			`SELECT attack, defense, sla, ad FROM game_standings WHERE team_id = $1`, team).
			Scan(&r.steal, &r.hold, &r.up, &r.ad); err != nil {
			t.Fatalf("standing: %v", err)
		}
		return r
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-3 }
	if r := get(alpha); !near(r.steal, 2) || r.hold != 0 || !near(r.up, up) {
		t.Fatalf("alpha = %+v, want steal 2 (sole captor), hold 0 (nobody attacked it), up %.3f", r, up)
	}
	if r := get(bravo); r.steal != 0 || r.hold != 0 || !near(r.up, up) {
		t.Fatalf("bravo = %+v, want only up (its flag was stolen)", r)
	}
	if r := get(charlie); !near(r.hold, 2) || !near(r.ad, 2+up) {
		t.Fatalf("charlie = %+v, want hold 2 (held against one attacker, h = 1)", r)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_standings WHERE team_id = '`+nop.String()+`'`); n != 0 {
		t.Fatal("the nop team is on the board")
	}
	var held, settled int
	if err := db.Pool.QueryRow(ctx,
		`SELECT flags_held, flags_settled FROM game_score_breakdown WHERE team_id = $1 AND service_id = $2`,
		charlie, svc).Scan(&held, &settled); err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if held != 1 || settled != 1 {
		t.Fatalf("charlie breakdown held/settled = %d/%d, want 1/1", held, settled)
	}

	// an admin re-credit of a red tick is visible through EffectiveSLA with its reason
	if _, err := db.Pool.Exec(ctx,
		`UPDATE game_sla_checks SET status = 'DOWN' WHERE team_id = $1 AND tick_number = 3`, bravo); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_sla_credits (service_id, team_id, tick_from, tick_to, reason) VALUES ($1, $2, 3, 3, 'router reboot')`,
		svc, bravo); err != nil {
		t.Fatal(err)
	}
	ticks, err := EffectiveSLA(ctx, db, bravo, 3, 3)
	if err != nil || len(ticks) != 1 || ticks[0].Effective != "ERROR" || ticks[0].Credited != "router reboot" {
		t.Fatalf("effective sla = %+v err %v", ticks, err)
	}
}

func TestIntegrationKothFinalsRoundsBountyAndCrowns(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	alpha := seedTeam(t, db, "alpha")
	bravo := seedTeam(t, db, "bravo")
	charlie := seedTeam(t, db, "charlie")
	h1 := seedHill(t, db, kothChecker(t, "alpha"))
	h2 := uuid.New()
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_koth_hills (id, name, slug, host, port, checker_ref, reset_seconds, enabled)
		 VALUES ($1, 'Hill2', 'hill2', '127.0.0.1', 1, $2, 900, true)`, h2, kothChecker(t, "bravo")); err != nil {
		t.Fatal(err)
	}

	cfg := testController(db).cfg
	cfg.Scoring.KothHold = 0 // finals score rounds, not ticks
	cfg.Koth.ResetEnabled = false
	cfg.Koth.BountyPerRound, cfg.Koth.BountyMinStreak = 2, 2
	ctrl := NewController(cfg, db, zap.NewNop())
	endRound := func() {
		t.Helper()
		if _, err := db.Pool.Exec(ctx,
			`UPDATE game_koth_rounds SET planned_end = NOW() - interval '1 second' WHERE status = 'running'`); err != nil {
			t.Fatal(err)
		}
	}

	runTickOK(t, ctrl, ctx, 1) // round 1 opens; alpha holds h1, bravo h2
	endRound()
	runTickOK(t, ctrl, ctx, 2) // round 1 closes, round 2 opens
	endRound()
	if _, err := db.Pool.Exec(ctx, `UPDATE game_koth_hills SET checker_ref = $1 WHERE id = $2`,
		kothChecker(t, "charlie"), h1); err != nil {
		t.Fatal(err)
	}
	runTickOK(t, ctrl, ctx, 3) // round 2 closes (alpha has won h1 twice); charlie takes h1 in round 3
	runTickOK(t, ctrl, ctx, 4)
	endRound()
	runTickOK(t, ctrl, ctx, 5) // round 3 closes: charlie ends alpha's two-round reign

	pts := func(team uuid.UUID) float64 {
		var p float64
		if err := db.Pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(points), 0) FROM game_score_events WHERE team_id = $1 AND stream = 'KOTH'`, team).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// each hill ranks its own round: alpha 12 + 12 on h1, bravo 12 x 3 on h2
	if got := pts(alpha); got != 24 {
		t.Fatalf("alpha koth = %v, want 24 (two h1 rounds)", got)
	}
	if got := pts(bravo); got != 36 {
		t.Fatalf("bravo koth = %v, want 36 (three h2 rounds)", got)
	}
	// charlie held h1 for both ticks of round 3 and won it, plus the bounty for
	// ending a 2-round reign: 2 x 2
	if got := pts(charlie); got != 12+4 {
		t.Fatalf("charlie koth = %v, want 16 (round win + bounty)", got)
	}
	var bounty float64
	if err := db.Pool.QueryRow(ctx,
		`SELECT bounty FROM game_koth_results WHERE team_id = $1 AND hill_id = $2 AND round_number = 3`, charlie, h1).Scan(&bounty); err != nil {
		t.Fatalf("result row: %v", err)
	}
	if bounty != 4 {
		t.Fatalf("recorded bounty = %v, want 4", bounty)
	}
	// crown events: alpha and bravo take their hills on tick 1, charlie takes h1 on tick 3
	if n := count(t, db, `SELECT COUNT(*) FROM game_events WHERE kind = 'crown'`); n != 3 {
		t.Fatalf("crown events = %d, want 3", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_events WHERE kind = 'crown' AND tick_number = 3 AND team_id = '`+charlie.String()+`'`); n != 1 {
		t.Fatal("no crown event for charlie taking h1")
	}
}

func TestIntegrationHillWindowsAndExtend(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)
	seedTeam(t, db, "alpha")
	hill := seedHill(t, db, kothChecker(t, "alpha")) // starts enabled
	now := time.Now()
	if _, err := db.Pool.Exec(ctx,
		`INSERT INTO game_koth_windows (hill_id, opens_at, closes_at) VALUES ($1, $2, $3)`,
		hill, now.Add(20*time.Minute), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctrl := testController(db)
	runTickOK(t, ctrl, ctx, 1)
	var enabled bool
	if err := db.Pool.QueryRow(ctx, `SELECT enabled FROM game_koth_hills WHERE id = $1`, hill).Scan(&enabled); err != nil || enabled {
		t.Fatalf("a hill before its window must be closed (enabled=%v err=%v)", enabled, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_events WHERE kind = 'hill_close'`); n != 1 {
		t.Fatalf("hill_close events = %d, want 1", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_koth_control WHERE tick_number = 1`); n != 0 {
		t.Fatal("a closed hill was polled")
	}

	// extend: everything still ahead moves, everything past stays
	past := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	end := now.Add(time.Hour).UTC().Truncate(time.Second)
	setSetting(t, db, "event.end_at", `"`+end.Format(time.RFC3339)+`"`)
	setSetting(t, db, "event.freeze_at", `"`+past+`"`)
	svc := seedService(t, db, okChecker(t))
	if _, err := db.Pool.Exec(ctx, `UPDATE game_services SET release_at = $2, advisory_at = $3 WHERE id = $1`,
		svc, now.Add(30*time.Minute), now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := ExtendGame(ctx, db, 15*time.Minute); err != nil {
		t.Fatalf("extend: %v", err)
	}
	var endRaw, freezeRaw string
	if err := db.Pool.QueryRow(ctx,
		`SELECT (SELECT value #>> '{}' FROM platform_settings WHERE key = 'event.end_at'),
		        (SELECT value #>> '{}' FROM platform_settings WHERE key = 'event.freeze_at')`).Scan(&endRaw, &freezeRaw); err != nil {
		t.Fatal(err)
	}
	if endRaw != end.Add(15*time.Minute).Format(time.RFC3339) || freezeRaw != past {
		t.Fatalf("end %s freeze %s: the future end must move 15m, the past freeze must not", endRaw, freezeRaw)
	}
	var shifted, unshifted bool
	if err := db.Pool.QueryRow(ctx,
		`SELECT release_at > $2::timestamptz + interval '44 minutes', advisory_at < $2::timestamptz
		 FROM game_services WHERE id = $1`, svc, now).Scan(&shifted, &unshifted); err != nil {
		t.Fatal(err)
	}
	if !shifted || !unshifted {
		t.Fatalf("release moved=%v, past advisory kept=%v", shifted, unshifted)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_koth_windows WHERE opens_at > NOW() + interval '34 minutes'`); n != 1 {
		t.Fatal("the hill window did not move")
	}
}
