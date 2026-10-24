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
// (zero teams too), and it blinds at the freeze like history.
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
	exec(`INSERT INTO game_koth_rounds (round_number, status) VALUES ($1, 'closed'), ($2, 'closed'), ($3, 'running')`, r1, r2, r3)
	// r1: a wins hill 1. r2: a wins hill 1 with a bounty and places on hill 2.
	// r3 is still running, so its row must not count.
	exec(`INSERT INTO game_koth_results (round_number, hill_id, team_id, held_ticks, rank, points, bounty) VALUES
	      ($1, $4, $5, 3, 1, 12, 0),
	      ($2, $4, $5, 2, 1, 7, 2),
	      ($2, $6, $5, 1, 3, 4, 0),
	      ($2, $4, $7, 5, 1, 12, 0),
	      ($3, $4, $5, 4, 1, 12, 0)`, r1, r2, r3, h1, a, h2, nop)
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_results WHERE round_number IN ($1, $2, $3)`, r1, r2, r3)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_rounds WHERE round_number IN ($1, $2, $3)`, r1, r2, r3)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_koth_hills WHERE id IN ($1, $2)`, h1, h2)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM game_teams WHERE id IN ($1, $2, $3)`, a, b, nop)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM platform_settings WHERE key = 'event.freeze_at'`)
	}()

	cfg := &config.Config{}
	cfg.Game.Enabled = true
	cfg.Game.Scoring.KothHold = 0.5 // hold term rides on held_ticks, as in accumulateKoth
	h := NewGameHandler(cfg, db, zap.NewNop())

	load := func() (map[string][]historyPoint, bool) {
		t.Helper()
		body, _, err := h.loadLiveState(ctx, false)
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
		a.String(): {{Tick: r1, Total: 12 + 1.5}, {Tick: r2, Total: 13.5 + 9 + 1 + 4 + 0.5}},
		b.String(): {{Tick: r1, Total: 0}, {Tick: r2, Total: 0}},
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

	exec(`INSERT INTO platform_settings (key, value) VALUES ('event.freeze_at', to_jsonb((NOW() - interval '1 minute')::text))
	      ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if got, _ := load(); len(got) != 0 {
		t.Fatalf("koth_history leaked during freeze: %+v", got)
	}
}
