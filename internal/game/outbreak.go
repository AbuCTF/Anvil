package game

// outbreak.go — the Outbreak wave bot (§4 of INTERNAL-API.md).
//
// Schedule:
//   - Per (service, flag_store): advisory publishes at first blood (3+ distinct victims),
//     OR at game_services.advisory_at (IST) for stores that never go orange naturally.
//   - Wave starts orangeDelay (15) ticks after the advisory.
//   - Each wave tick: call exploit for every green team, write to game_bot_reads.
//   - Quiet window 00:00–06:00 IST: new advisories and first wave ticks defer; ongoing waves continue.
//
// Gated behind game.outbreak.enabled (default false).
// Sets outbreakFactory in init() so it's automatically wired by NewController.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	MethodExploit = "exploit"

	// firstBloodThreshold is the minimum distinct victim count to declare first blood.
	firstBloodThreshold = 3
	// orangeDelay is the number of ticks after first blood before the wave starts.
	orangeDelay = 15

	// quietStart/quietEnd are the IST hours (UTC+5:30) for the no-new-wave window.
	quietStartHour = 0
	quietEndHour   = 6
)

var istLocation = time.FixedZone("IST", 5*60*60+30*60)

func init() {
	outbreakFactory = func(c *Controller) func(ctx context.Context, tick int, started time.Time) {
		if !c.cfg.Outbreak.Enabled {
			return nil
		}
		bot := &outbreakBot{
			cfg:    c.cfg,
			db:     c.db,
			logger: c.logger.With(zap.String("component", "outbreak")),
			client: &http.Client{Transport: &http.Transport{
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     30 * time.Second,
			}},
		}
		return bot.run
	}
}

type outbreakBot struct {
	cfg    config.GameConfig
	db     *database.DB
	logger *zap.Logger
	client *http.Client
}

// orangeStore describes a (service, store) pair in wave mode this tick.
type orangeStore struct {
	serviceID   uuid.UUID
	serviceSlug string
	checkerRef  string
	storeIndex  int
	waveVariant int // wave_exploits[storeIndex], fallback = storeIndex
	port        int
}

// run is called by runFinalsTick (via Controller.outbreak) alongside dispatch.
// It waits until the third quarter of the tick, then calls the exploit checker for
// every orange (service, store) × green team.
func (b *outbreakBot) run(ctx context.Context, tick int, started time.Time) {
	tickInterval := b.cfg.TickInterval
	if tickInterval <= 0 {
		tickInterval = 2 * time.Minute
	}
	quarter := tickInterval / 4

	// wait for the third quarter to begin (putflag is done by then)
	thirdQStart := started.Add(2 * quarter)
	select {
	case <-time.After(time.Until(thirdQStart)):
	case <-ctx.Done():
		return
	}

	stores, err := b.orangeStores(ctx, tick, started)
	if err != nil {
		b.logger.Error("orange stores query", zap.Int("tick", tick), zap.Error(err))
		return
	}
	if len(stores) == 0 {
		return
	}

	teams, err := b.greenTeams(ctx)
	if err != nil {
		b.logger.Error("green teams query", zap.Int("tick", tick), zap.Error(err))
		return
	}
	if len(teams) == 0 {
		return
	}

	taskTimeout := tickInterval / 4
	if b.cfg.Checker.TaskTimeout > 0 && b.cfg.Checker.TaskTimeout < taskTimeout {
		taskTimeout = b.cfg.Checker.TaskTimeout
	}

	var wg sync.WaitGroup
	for _, store := range stores {
		checker := checkerFor(store.checkerRef, b.client)
		for _, team := range teams {
			flag, attackInfo, err := b.currentFlag(ctx, tick, team.id, store.serviceID, store.storeIndex)
			if err != nil || flag == "" {
				continue // no flag for this team this tick (maybe NOP with no flag)
			}

			wg.Add(1)
			go func(store orangeStore, team greenTeam, flag, attackInfo string) {
				defer wg.Done()
				b.callExploit(ctx, tick, started, store, team, flag, attackInfo, checker, taskTimeout)
			}(store, team, flag, attackInfo)
		}
	}
	wg.Wait()
}

type greenTeam struct {
	id         uuid.UUID
	number     int
	name       string
	vulnboxIP  string
}

// greenTeams returns all active game teams (green = UP or no SLA check yet).
// NOP teams are included per spec.
func (b *outbreakBot) greenTeams(ctx context.Context) ([]greenTeam, error) {
	rows, err := b.db.Pool.Query(ctx,
		`SELECT id, COALESCE(team_number, 0), name, COALESCE(vulnbox_ip, '127.0.0.1')
		 FROM game_teams WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var teams []greenTeam
	for rows.Next() {
		var t greenTeam
		if err := rows.Scan(&t.id, &t.number, &t.name, &t.vulnboxIP); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// inQuietWindow returns true when the given time falls in the 00:00–06:00 IST window.
func inQuietWindow(t time.Time) bool {
	h := t.In(istLocation).Hour()
	return h >= quietStartHour && h < quietEndHour
}

// orangeStores returns all (service, store) pairs in wave mode for this tick.
// A pair enters wave mode when: firstBlood+orangeDelay <= tick, OR advisory_at <= tick start.
func (b *outbreakBot) orangeStores(ctx context.Context, tick int, started time.Time) ([]orangeStore, error) {
	// advisory_tick (from game_events) is the canonical source of truth for when
	// the advisory was published. if absent, the advisory hasn't been published yet.
	rows, err := b.db.Pool.Query(ctx, `
		SELECT
		    gs.id,
		    gs.slug,
		    gs.checker_ref,
		    COALESCE(gs.port, 0),
		    gs.wave_exploits,
		    s.store_index,
		    gs.advisory_at,
		    fb.first_blood_tick,
		    adv.advisory_tick
		FROM game_services gs
		CROSS JOIN LATERAL (
		    SELECT generate_series(0, GREATEST(gs.flag_stores, 1) - 1) AS store_index
		) s
		LEFT JOIN LATERAL (
		    SELECT MIN(gc.tick_number) AS first_blood_tick
		    FROM game_captures gc
		    JOIN game_flags gf ON gf.id = gc.flag_id
		    WHERE gf.service_id = gs.id AND gf.store_index = s.store_index
		    HAVING COUNT(DISTINCT gc.victim_team_id) >= $2
		) fb ON TRUE
		LEFT JOIN LATERAL (
		    SELECT MIN(tick_number) AS advisory_tick
		    FROM game_events
		    WHERE kind = 'advisory' AND service_id = gs.id AND store_index = s.store_index
		) adv ON TRUE
		WHERE gs.enabled = true AND gs.checker_ref IS NOT NULL AND gs.checker_ref <> ''
	`, tick, firstBloodThreshold)
	if err != nil {
		return nil, fmt.Errorf("orange stores: %w", err)
	}
	defer rows.Close()

	var result []orangeStore
	for rows.Next() {
		var (
			id           uuid.UUID
			slug         string
			checkerRef   string
			port         int
			waveExplJSON []byte
			storeIndex   int
			advisoryAt   *time.Time
			firstBlood   *int
			advisoryTick *int
		)
		if err := rows.Scan(&id, &slug, &checkerRef, &port, &waveExplJSON, &storeIndex,
			&advisoryAt, &firstBlood, &advisoryTick); err != nil {
			return nil, err
		}

		// determine wave variant: wave_exploits[storeIndex], fallback to storeIndex
		waveVariant := storeIndex
		if len(waveExplJSON) > 0 {
			var wm map[string]int
			if json.Unmarshal(waveExplJSON, &wm) == nil {
				key := fmt.Sprintf("%d", storeIndex)
				if v, ok := wm[key]; ok {
					waveVariant = v
				}
			}
		}

		// advisory publishes at first blood (or advisory_at), outside quiet window.
		// the advisory tick (game_events row) is the clock for when the wave may start.
		// new advisories defer during 00:00-06:00 IST; running waves continue.
		if advisoryTick == nil {
			triggerMet := (firstBlood != nil && tick >= *firstBlood) ||
				(advisoryAt != nil && !advisoryAt.After(started))
			if triggerMet && !inQuietWindow(started) {
				b.publishAdvisory(ctx, tick, id, storeIndex)
				advisoryTick = &tick
			}
		}

		// wave starts orangeDelay ticks after the advisory
		if advisoryTick == nil || tick < *advisoryTick+orangeDelay {
			continue
		}

		// first wave tick defers if quiet window is active; ongoing waves continue
		if inQuietWindow(started) && tick == *advisoryTick+orangeDelay {
			continue
		}

		result = append(result, orangeStore{
			serviceID:   id,
			serviceSlug: slug,
			checkerRef:  checkerRef,
			storeIndex:  storeIndex,
			waveVariant: waveVariant,
			port:        port,
		})
	}
	return result, rows.Err()
}

// currentFlag returns the flag and attack_info for (team, service, store) planted this tick.
func (b *outbreakBot) currentFlag(ctx context.Context, tick int, teamID, serviceID uuid.UUID, storeIndex int) (string, string, error) {
	var flag, attackInfo string
	err := b.db.Pool.QueryRow(ctx,
		`SELECT flag, COALESCE(attack_info, '') FROM game_flags
		 WHERE tick_number = $1 AND team_id = $2 AND service_id = $3 AND store_index = $4
		   AND planted_at IS NOT NULL`,
		tick, teamID, serviceID, storeIndex,
	).Scan(&flag, &attackInfo)
	if err != nil {
		return "", "", err
	}
	return flag, attackInfo, nil
}

// callExploit calls the exploit checker for one (team, service, store) and records the outcome.
func (b *outbreakBot) callExploit(
	ctx context.Context,
	tick int,
	started time.Time,
	store orangeStore,
	team greenTeam,
	flag, attackInfo string,
	checker Checker,
	taskTimeout time.Duration,
) {
	chainID := fmt.Sprintf("outbreak_s%s_t%s_i%d_tick%d",
		store.serviceID, team.id, store.storeIndex, tick)
	task := Task{
		Tick:        tick,
		RelatedTick: tick,
		TeamNumber:  team.number,
		TeamName:    team.name,
		Method:      MethodExploit,
		Variant:     store.waveVariant,
		Flag:        flag,
		ChainID:     chainID,
		Timeout:     taskTimeout,
		RoundLength: b.cfg.TickInterval,
	}

	tctx, cancel := context.WithTimeout(ctx, taskTimeout)
	res := checker.Run(tctx, Target{Host: team.vulnboxIP, Port: store.port}, task)
	cancel()

	outcome := botOutcome(res, flag)

	// find the flag_id for this (team, service, store, tick) row
	var flagID uuid.UUID
	if err := b.db.Pool.QueryRow(ctx,
		`SELECT id FROM game_flags
		 WHERE tick_number = $1 AND team_id = $2 AND service_id = $3 AND store_index = $4`,
		tick, team.id, store.serviceID, store.storeIndex,
	).Scan(&flagID); err != nil {
		b.logger.Warn("outbreak: flag_id lookup",
			zap.Int("tick", tick), zap.Stringer("team", team.id), zap.Error(err))
		return
	}

	if _, err := b.db.Pool.Exec(ctx,
		`INSERT INTO game_bot_reads (flag_id, tick_number, outcome, latency_ms)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (flag_id, tick_number) DO NOTHING`,
		flagID, tick, outcome, res.Latency.Milliseconds(),
	); err != nil {
		b.logger.Warn("outbreak: record bot read",
			zap.Int("tick", tick), zap.Stringer("team", team.id), zap.Error(err))
	}
}

// botOutcome maps a checker result to the game_bot_reads outcome vocabulary.
func botOutcome(res Result, expectedFlag string) string {
	switch res.Status {
	case models.SLAOk:
		// checker returned OK; stolen if it echoed the exact flag in message
		if res.Message == expectedFlag {
			return "stolen"
		}
		return "blocked"
	case models.SLADown:
		// service stopped answering during or after the exploit
		return "crashed"
	default:
		return "error"
	}
}

// publishAdvisory writes a game_events row when the wave starts for a store.
func (b *outbreakBot) publishAdvisory(ctx context.Context, tick int, serviceID uuid.UUID, storeIndex int) {
	payload := fmt.Sprintf(`{"store_index":%d}`, storeIndex)
	if _, err := b.db.Pool.Exec(ctx,
		`INSERT INTO game_events (kind, tick_number, service_id, store_index, payload)
		 VALUES ('advisory', $1, $2, $3, $4::jsonb)`,
		tick, serviceID, storeIndex, payload,
	); err != nil {
		b.logger.Warn("outbreak: publish advisory",
			zap.Int("tick", tick), zap.Stringer("service", serviceID), zap.Error(err))
	}
}
