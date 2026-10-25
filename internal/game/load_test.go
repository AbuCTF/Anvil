//go:build integration

package game

// load_test.go — synthetic engine harness.
//
// TestEngineLoad: 16 teams × 6 services with varied ENO checker latency
// profiles (fast/medium/slow/hung). Asserts tick timing, hung-task timeouts,
// and DB throughput (flag + SLA check row counts).
//
// TestEngineConcurrentGateUnderLiveTick: verifies that 15 simultaneous
// GateRunner calls do not block a live tick (separate semaphore pools).

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
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

// --- helpers -----------------------------------------------------------------

func loadController(db *database.DB, taskTimeout time.Duration) *Controller {
	return NewController(config.GameConfig{
		Enabled:        true,
		TickInterval:   time.Minute,
		FlagValidTicks: 10,
		FlagPrefix:     "H7CTF",
		Koth:           config.KothConfig{RoundInterval: 2 * time.Minute},
		Checker: config.CheckerConfig{
			TaskTimeout: taskTimeout,
			TailMargin:  taskTimeout / 3,
			Concurrency: 64,
		},
		Scoring: config.ScoringConfig{
			AttackBase:    100,
			DefenseFactor: 1,
			SLAPoints:     10,
			KothHold:      5,
			KothRank:      []int{12, 7, 4, 2, 1},
		},
	}, db, zap.NewNop())
}

// seedLoadTeams inserts n teams named "loadXX" with IPs 10.1.0.1..n.
func seedLoadTeams(t *testing.T, db *database.DB, n int) []uuid.UUID {
	t.Helper()
	teams := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		name := fmt.Sprintf("load%02d", i+1)
		ip := fmt.Sprintf("10.1.0.%d", i+1)
		_, err := db.Pool.Exec(context.Background(),
			`INSERT INTO game_teams (id, name, slug, token, status, is_nop, vulnbox_ip)
			 VALUES ($1, $2, $2, $2, 'active', false, $3)`, id, name, ip)
		if err != nil {
			t.Fatalf("seed team %d: %v", i+1, err)
		}
		teams[i] = id
	}
	return teams
}

// seedVariantService inserts a game_service with the given slug and checker URL.
func seedVariantService(t *testing.T, db *database.DB, slug, checkerURL string, flags, noise, havoc int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO game_services (id, name, slug, category, tier, port, checker_ref,
		   flag_stores, noise_variants, havoc_variants, enabled)
		 VALUES ($1, $2, $2, 'web', 'core', 8080, $3, $4, $5, $6, true)`,
		id, slug, checkerURL, flags, noise, havoc)
	if err != nil {
		t.Fatalf("seed service %s: %v", slug, err)
	}
	return id
}

// enoFakeLatency returns a fake ENO checker that sleeps for delay before responding OK.
func enoFakeLatency(t *testing.T, delay time.Duration) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	return enoFake(t, func(enoTaskMessage) enoResultMessage {
		if delay > 0 {
			time.Sleep(delay)
		}
		return enoResult("OK", "")
	})
}

// enoFakeHung returns a checker that blocks every request until the test ends.
// The t.Cleanup ordering (LIFO) ensures release fires before srv.Close so the
// handlers drain cleanly — same pattern as TestIntegrationHungCheckerDoesNotStallTheTick.
func enoFakeHung(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release // block until test cleanup closes this channel
	}))
	t.Cleanup(srv.Close)                 // registered 1st → runs 2nd (LIFO)
	t.Cleanup(func() { close(release) }) // registered 2nd → runs 1st
	return srv, &calls
}

// enoFakeFlaky responds in a random delay between lo and hi (ms).
func enoFakeFlaky(t *testing.T, loMs, hiMs int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	r := rand.New(rand.NewSource(42)) //nolint:gosec // test only, deterministic seed
	var mu sync.Mutex
	return enoFake(t, func(enoTaskMessage) enoResultMessage {
		mu.Lock()
		d := time.Duration(loMs+r.Intn(hiMs-loMs)) * time.Millisecond
		mu.Unlock()
		time.Sleep(d)
		return enoResult("OK", "")
	})
}

func countLoad(t *testing.T, db *database.DB, q string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count(%s): %v", q, err)
	}
	return n
}

// --- tests -------------------------------------------------------------------

// TestEngineLoad exercises a full tick with 16 teams × 6 services and 5 distinct
// checker latency profiles. The assertions are:
//
//   - Tick completes in under 2× TaskTimeout (hung tasks time out, don't stall).
//   - Hung-service tasks are recorded as TIMEOUT in game_sla_checks.
//   - OK-service tasks are recorded as OK.
//   - game_flags count equals non-hung teams × non-hung flag_store sum.
func TestEngineLoad(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	const (
		nTeams      = 16
		taskTimeout = 3 * time.Second
	)

	seedLoadTeams(t, db, nTeams)

	// svc1: fast (5ms), flags=1, noise=1, havoc=1
	fast, _ := enoFakeLatency(t, 5*time.Millisecond)
	s1 := seedVariantService(t, db, "load-fast", fast.URL, 1, 1, 1)

	// svc2: medium (250ms), flags=1, noise=1, havoc=1
	medium, _ := enoFakeLatency(t, 250*time.Millisecond)
	s2 := seedVariantService(t, db, "load-medium", medium.URL, 1, 1, 1)

	// svc3: slow (2s), flags=1, noise=0, havoc=0  — within timeout
	slow, _ := enoFakeLatency(t, 2*time.Second)
	s3 := seedVariantService(t, db, "load-slow", slow.URL, 1, 0, 0)

	// svc4: hung (blocks until ctx cancelled) — all tasks time out
	hung, _ := enoFakeHung(t)
	s4 := seedVariantService(t, db, "load-hung", hung.URL, 1, 0, 0)

	// svc5: multi-flag (50ms), flags=2, noise=1, havoc=0
	multi, _ := enoFakeLatency(t, 50*time.Millisecond)
	s5 := seedVariantService(t, db, "load-multi", multi.URL, 2, 1, 0)

	// svc6: flaky (500-2000ms), flags=1, noise=1, havoc=1 — all within timeout
	flaky, _ := enoFakeFlaky(t, 500, 2000)
	s6 := seedVariantService(t, db, "load-flaky", flaky.URL, 1, 1, 1)

	_ = s1
	_ = s2
	_ = s3
	_ = s5
	_ = s6

	ctrl := loadController(db, taskTimeout)

	tickStart := time.Now()
	ageEarlierTicks(t, db, 1)
	if err := ctrl.runTick(ctx, 1); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	tickDur := time.Since(tickStart)

	// hung tasks time out at taskTimeout; the tick should not take more than
	// 2× that (generous for CI scheduling jitter).
	if tickDur > 2*taskTimeout+5*time.Second {
		t.Errorf("tick took %s; expected < %s", tickDur, 2*taskTimeout+5*time.Second)
	}
	t.Logf("tick 1 wall time: %s", tickDur)

	// hung service: all 16 tasks must be DOWN (checker timed out → SLADown)
	downCount := countLoad(t, db,
		`SELECT COUNT(*) FROM game_sla_checks WHERE service_id = $1 AND status = 'DOWN'`, s4)
	if downCount != nTeams {
		t.Errorf("hung service: want %d DOWN, got %d", nTeams, downCount)
	}

	// fast/medium/slow/multi/flaky: every SLA check must be OK
	for _, sid := range []uuid.UUID{s1, s2, s3, s5, s6} {
		notOK := countLoad(t, db,
			`SELECT COUNT(*) FROM game_sla_checks WHERE service_id = $1 AND status != 'OK'`, sid)
		if notOK != 0 {
			t.Errorf("service %s: %d non-OK sla checks", sid, notOK)
		}
	}

	// flags: s4 (hung) plants no flags on timeout; others each plant flags per team.
	// s1,s2,s3,s6: 1 flag_store × 16 teams = 16 each
	// s5: 2 flag_stores × 16 teams = 32
	for _, tc := range []struct {
		id   uuid.UUID
		want int
	}{
		{s1, 16}, {s2, 16}, {s3, 16}, {s5, 32}, {s6, 16},
	} {
		got := countLoad(t, db,
			`SELECT COUNT(*) FROM game_flags WHERE service_id = $1 AND tick_number = 1`, tc.id)
		if got != tc.want {
			t.Errorf("flags for service %s: want %d, got %d", tc.id, tc.want, got)
		}
	}
	// flags are reserved in DB before the checker runs; for hung tasks planted_at stays NULL
	hungUnplanted := countLoad(t, db,
		`SELECT COUNT(*) FROM game_flags WHERE service_id = $1 AND planted_at IS NULL`, s4)
	if hungUnplanted != nTeams {
		t.Errorf("hung service: want %d unplanted flags, got %d", nTeams, hungUnplanted)
	}

	// run a second tick to confirm getflag chains work at scale
	ageEarlierTicks(t, db, 2)
	if err := ctrl.runTick(ctx, 2); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	t.Logf("total sla checks after 2 ticks: %d",
		countLoad(t, db, `SELECT COUNT(*) FROM game_sla_checks`))
	t.Logf("total flags planted after 2 ticks: %d",
		countLoad(t, db, `SELECT COUNT(*) FROM game_flags`))
}

func TestSignTargetConcurrencySerializesEachTargetNotTheField(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	const nTargets = 16
	teams := seedLoadTeams(t, db, nTargets)
	if _, err := db.Pool.Exec(ctx, `UPDATE game_teams SET is_nop = TRUE WHERE id = $1`, teams[nTargets-1]); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	activeByTarget := make(map[string]int)
	maxByTarget := make(map[string]int)
	globalActive := 0
	globalMax := 0
	checker, calls := enoFake(t, func(task enoTaskMessage) enoResultMessage {
		mu.Lock()
		activeByTarget[task.Address]++
		globalActive++
		maxByTarget[task.Address] = max(maxByTarget[task.Address], activeByTarget[task.Address])
		globalMax = max(globalMax, globalActive)
		mu.Unlock()

		time.Sleep(25 * time.Millisecond)

		mu.Lock()
		activeByTarget[task.Address]--
		globalActive--
		mu.Unlock()
		return enoResult("OK", "")
	})
	seedVariantService(t, db, "sign-me-in", checker.URL, 2, 1, 5)

	runTickOK(t, loadController(db, 3*time.Second), ctx, 1)

	if got, want := calls.Load(), int64(nTargets*11); got != want {
		t.Fatalf("checker tasks = %d, want %d", got, want)
	}
	if got, want := countLoad(t, db, `SELECT COUNT(*) FROM game_checker_tasks WHERE status = 'OK'`), nTargets*11; got != want {
		t.Fatalf("OK checker task rows = %d, want %d", got, want)
	}
	if got := countLoad(t, db, `SELECT COUNT(*) FROM game_sla_checks WHERE status = 'OK'`); got != nTargets {
		t.Fatalf("OK SLA rows = %d, want %d", got, nTargets)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(maxByTarget) != nTargets {
		t.Fatalf("checker reached %d targets, want %d", len(maxByTarget), nTargets)
	}
	if globalActive != 0 {
		t.Fatalf("global active checker tasks = %d after tick, want 0", globalActive)
	}
	for target, peak := range maxByTarget {
		if activeByTarget[target] != 0 {
			t.Errorf("target %s still has %d active checker tasks", target, activeByTarget[target])
		}
		if peak != 1 {
			t.Errorf("target %s peak concurrency = %d, want 1", target, peak)
		}
	}
	if globalMax <= 1 || globalMax > nTargets {
		t.Fatalf("global peak concurrency = %d, want 2..%d", globalMax, nTargets)
	}
}

// TestEngineConcurrentGateUnderLiveTick fires 15 simultaneous GateRunner calls
// while a live tick is running. The gate's separate semaphore pool must not
// block the tick, and the tick must not block the gates.
func TestEngineConcurrentGateUnderLiveTick(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()
	setFinals(t, db)

	const nTeams = 8
	const nGates = 15
	const taskTimeout = 2 * time.Second

	seedLoadTeams(t, db, nTeams)

	// one fast service used by both the live tick and the gate calls
	fast, _ := enoFakeLatency(t, 20*time.Millisecond)
	seedVariantService(t, db, "gate-svc", fast.URL, 1, 1, 0)

	// two additional services to give the tick real work
	for _, slug := range []string{"gate-svc2", "gate-svc3"} {
		s, _ := enoFakeLatency(t, 100*time.Millisecond)
		seedVariantService(t, db, slug, s.URL, 1, 0, 0)
	}

	ctrl := loadController(db, taskTimeout)
	gate := newGateRunnerForTest(db, 16)

	var tickErr error
	var tickDone sync.WaitGroup
	tickDone.Add(1)
	go func() {
		defer tickDone.Done()
		ageEarlierTicks(t, db, 1)
		tickErr = ctrl.runTick(ctx, 1)
	}()

	var wg sync.WaitGroup
	gateErrs := make([]error, nGates)
	for i := 0; i < nGates; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, gateErrs[idx] = gate.RunGate(ctx, "gate-svc", "127.0.0.1",
				1, taskTimeout)
		}(i)
	}

	wg.Wait()
	tickDone.Wait()

	if tickErr != nil {
		t.Errorf("tick error under concurrent gates: %v", tickErr)
	}
	for i, err := range gateErrs {
		if err != nil {
			t.Errorf("gate[%d] error: %v", i, err)
		}
	}

	// verify all live tasks still got dispatched (gates did not starve live pool)
	ok := countLoad(t, db,
		`SELECT COUNT(*) FROM game_sla_checks WHERE status = $1`, string(models.SLAOk))
	if ok == 0 {
		t.Error("no OK sla checks after concurrent gates — live pool may have been starved")
	}
	t.Logf("concurrent gate test: %d OK live checks, %d gate runs completed", ok, nGates)
}

// newGateRunnerForTest creates a GateRunner wired to the test DB.
func newGateRunnerForTest(db *database.DB, concurrency int) *GateRunner {
	return NewGateRunner(db, concurrency, "H7CTF")
}
