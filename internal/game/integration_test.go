//go:build integration

package game

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
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
		`TRUNCATE game_teams, game_services, game_ticks, game_koth_hills, game_koth_rounds, users CASCADE`)
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
	if err := ctrl.runTick(ctx, tick); err != nil {
		t.Fatalf("run tick %d: %v", tick, err)
	}
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

func TestIntegrationDispatchChecksPreviousFlagAndDoesNotRepeatClosedTick(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	seedTeam(t, db, "alpha")
	seedTeam(t, db, "bravo")
	checker, logPath := checkingChecker(t)
	seedService(t, db, checker)

	ctrl := testController(db)
	if err := ctrl.runTick(ctx, 1); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if err := ctrl.runTick(ctx, 2); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE tick_number = 2 AND status = 'FLAG_NOT_FOUND'`); n != 2 {
		t.Fatalf("expected retrieval failures for both teams on tick 2, got %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM game_flags WHERE planted_at IS NOT NULL`); n != 4 {
		t.Fatalf("expected two planted generations, got %d flags", n)
	}

	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read checker log: %v", err)
	}
	if checks := strings.Count(string(before), `"action":"check"`); checks != 2 {
		t.Fatalf("expected two retrieval checks, got %d", checks)
	}
	if err := ctrl.runTick(ctx, 2); err != nil {
		t.Fatalf("repeat closed tick: %v", err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read checker log after repeat: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("closed tick dispatched again: checker log grew from %d to %d bytes", len(before), len(after))
	}
}

func TestIntegrationReservedFlagIsNotSubmittable(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

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
	if got := strings.Count(strings.TrimSpace(string(actions)), "\n") + 1; got != 2 {
		t.Fatalf("two controllers dispatched %d checker jobs; want exactly 2", got)
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
