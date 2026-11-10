//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// koth_history is the running koth total per closed round, every non-nop team
// (zero teams too), and freezes at the last round closed by the cutoff.
func TestArenaStateKothHistory(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 2,
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
			t.Fatalf("%s: %v", q, err)
		}
	}
	sfx := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	a, b, nop, h1, h2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO game_teams (id, name, slug) VALUES ($1, $2, $3), ($4, $5, $6)`,
		a, "kh-a-"+sfx, "kh-a-"+sfx, b, "kh-b-"+sfx, "kh-b-"+sfx)
	exec(`INSERT INTO game_teams (id, name, slug, is_nop) VALUES ($1, $2, $3, TRUE)`, nop, "kh-nop-"+sfx, "kh-nop-"+sfx)
	exec(`INSERT INTO game_koth_hills (id, name, slug, port) VALUES ($1, $2, $3, 7191), ($4, $5, $6, 7192)`,
		h1, "kh1 "+sfx, "kh1-"+sfx, h2, "kh2 "+sfx, "kh2-"+sfx)
	const r1, r2, r3 = 9101, 9102, 9103
	exec(`INSERT INTO game_koth_rounds (round_number, status, ends_at)
	      VALUES ($1, 'closed', NOW() - interval '3 minutes'),
	             ($2, 'closed', NOW() - interval '1 minute'),
	             ($3, 'running', NULL)`, r1, r2, r3)
	// the history must chart the same sources standings sum: hold per controlled tick
	// (game_koth_control) plus KOTH score events per round.
	// r1: a holds hill 1 for 3 ticks and earns 12.
	// r2: a holds hill 1 for 2 ticks and hill 2 for 1 tick, earning 7+2 and 4;
	//     b holds hill 2 for 2 ticks with no round event (tick-hold only, the case
	//     that charted flat at zero when history read game_koth_results);
	//     the nop team holds and earns too but must never be charted.
	// r3 is still running, so none of its ticks or events count.
	const t0 = 9101000
	exec(`INSERT INTO game_ticks (tick_number, status) SELECT g, 'closed' FROM generate_series($1::int, $1::int + 11) g`, t0)
	exec(`INSERT INTO game_koth_control (tick_number, round_number, hill_id, controller_team_id) VALUES
	      ($1, $2, $5, $7), ($1 + 1, $2, $5, $7), ($1 + 2, $2, $5, $7),
	      ($1 + 3, $3, $5, $7), ($1 + 4, $3, $5, $7), ($1 + 3, $3, $6, $7),
	      ($1 + 4, $3, $6, $8), ($1 + 5, $3, $6, $8),
	      ($1 + 5, $3, $5, $9), ($1 + 6, $3, $5, $9),
	      ($1 + 7, $4, $5, $7), ($1 + 8, $4, $5, $7), ($1 + 9, $4, $5, $7), ($1 + 10, $4, $5, $7)`,
		t0, r1, r2, r3, h1, h2, a, b, nop)
	exec(`INSERT INTO game_score_events (team_id, round_number, stream, points, source) VALUES
	      ($4, $1, 'KOTH', 12, 'koth:test'),
	      ($4, $2, 'KOTH', 9, 'koth:test'), ($4, $2, 'KOTH', 4, 'koth:test'),
	      ($5, $2, 'KOTH', 12, 'koth:test'),
	      ($4, $3, 'KOTH', 12, 'koth:test')`, r1, r2, r3, a, nop)
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_score_events WHERE round_number IN ($1, $2, $3)`, r1, r2, r3)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_control WHERE round_number IN ($1, $2, $3)`, r1, r2, r3)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_rounds WHERE round_number IN ($1, $2, $3)`, r1, r2, r3)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_hills WHERE id IN ($1, $2)`, h1, h2)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_teams WHERE id IN ($1, $2, $3)`, a, b, nop)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_ticks WHERE tick_number BETWEEN $1 AND $1 + 11`, t0)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM platform_settings WHERE key = 'event.freeze_at'`)
	}()

	cfg := &config.Config{}
	cfg.Game.Enabled = true
	cfg.Game.Scoring.KothHold = 0.5 // hold per controlled tick, as in accumulateKoth
	h := NewGameHandler(cfg, db, zap.NewNop())

	load := func() (map[string][]historyPoint, bool) {
		t.Helper()
		body, _, err := h.loadLiveState(ctx, gameStatePublic)
		if err != nil {
			t.Fatalf("load state: %v", err)
		}
		var payload struct {
			KothHistory []historySeries `json:"koth_history"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got := map[string][]historyPoint{}
		for _, s := range payload.KothHistory {
			var mine []historyPoint
			for _, p := range s.Points {
				if p.Tick >= r1 {
					mine = append(mine, p)
				}
			}
			got[s.TeamID] = mine
		}
		return got, payload.KothHistory != nil
	}

	got, present := load()
	if !present {
		t.Fatal("koth_history missing from payload")
	}
	want := map[string][]historyPoint{
		a.String(): {{Tick: r1, Total: 12 + 1.5}, {Tick: r2, Total: 13.5 + 9 + 4 + 1.5}},
		b.String(): {{Tick: r1, Total: 0}, {Tick: r2, Total: 1}},
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok {
			t.Fatalf("team %s missing", id)
		}
		if len(g) != len(w) {
			t.Fatalf("team %s points %+v, want %+v", id, g, w)
		}
		for i := range w {
			if g[i] != w[i] {
				t.Fatalf("team %s points %+v, want %+v", id, g, w)
			}
		}
	}
	if _, ok := got[nop.String()]; ok {
		t.Fatal("nop team charted")
	}

	exec(`INSERT INTO platform_settings (key, value) VALUES ('event.freeze_at',
	      to_jsonb(to_char((NOW() - interval '2 minutes') AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
	      ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	got, _ = load()
	for _, id := range []string{a.String(), b.String()} {
		points := got[id]
		if len(points) != 1 || points[0].Tick != r1 {
			t.Fatalf("team %s frozen points %+v, want only round %d", id, points, r1)
		}
	}
}
