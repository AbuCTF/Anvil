package game

// outbreak.go — the Outbreak wave bot (§4 of INTERNAL-API.md).
//
// Schedule (wall-clock IST, not tick-count):
//   - Per (service, flag_store): advisory publishes at first blood (3+ distinct victims)
//     OR at game_services.advisory_at for stores that never go orange naturally.
//     Advisory defers if the first eligible tick is in [00:00, 06:00) IST → publishes at 06:00.
//   - Wave starts orangeDelay ticks (default 15 × tickInterval = 30 min) after the advisory.
//     wave_start_time = advisory_time + orangeDelay × tickInterval.
//     If wave_start_time ∈ [00:00, 06:00) IST → pushed to 06:30 IST that morning.
//   - Wave fires every tick while started >= wave_start_time.
//   - Waves that began before 00:00 continue through the quiet window naturally.
//
// Gated behind game.outbreak.enabled (default false).
// Sets outbreakFactory in init() so it's automatically wired by NewController.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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
	cfg       config.GameConfig
	db        *database.DB
	logger    *zap.Logger
	client    *http.Client
	taskOpen  func(uuid.UUID, uuid.UUID, Task) (int64, error)
	taskClose func(int64, Result) error
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

	taskTimeout := tickInterval / 4
	if b.cfg.Checker.TaskTimeout > 0 && b.cfg.Checker.TaskTimeout < taskTimeout {
		taskTimeout = b.cfg.Checker.TaskTimeout
	}

	var wg sync.WaitGroup
	for _, store := range stores {
		teams, err := b.greenTeams(ctx, tick, store.serviceID)
		if err != nil {
			b.logger.Error("green teams query", zap.Int("tick", tick), zap.Stringer("service", store.serviceID), zap.Error(err))
			continue
		}
		if len(teams) == 0 {
			continue
		}
		checker := checkerFor(store.checkerRef, b.client)
		wg.Add(1)
		go func(store orangeStore, teams []greenTeam, checker Checker) {
			defer wg.Done()
			b.runStore(ctx, tick, store, teams, checker, taskTimeout)
		}(store, teams, checker)
	}
	wg.Wait()
}

type greenTeam struct {
	id        uuid.UUID
	number    int
	name      string
	vulnboxIP string
	isNOP     bool
}

// greenTeams returns active teams whose service was green on the last completed
// tick. A missing SLA result and a missing service address both fail closed. NOP
// teams are included because their successful read validates the whole batch.
func (b *outbreakBot) greenTeams(ctx context.Context, tick int, serviceID uuid.UUID) ([]greenTeam, error) {
	rows, err := b.db.Pool.Query(ctx,
		`SELECT t.id, COALESCE(t.team_number, 0), t.name, host(t.vulnbox_ip), t.is_nop, sla.status
		 FROM game_teams t
		 JOIN LATERAL (
		   SELECT MAX(tick_number) AS tick_number
		   FROM game_ticks
		   WHERE status = 'closed' AND tick_number < $1
		 ) previous ON previous.tick_number IS NOT NULL
		 JOIN game_sla_checks sla ON sla.tick_number = previous.tick_number
		   AND sla.team_id = t.id AND sla.service_id = $2
		 WHERE t.status = 'active' AND t.vulnbox_ip IS NOT NULL`, tick, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var teams []greenTeam
	for rows.Next() {
		var t greenTeam
		var status models.SLAStatus
		if err := rows.Scan(&t.id, &t.number, &t.name, &t.vulnboxIP, &t.isNOP, &status); err != nil {
			return nil, err
		}
		if green(status) {
			teams = append(teams, t)
		}
	}
	return teams, rows.Err()
}

// inQuietWindow returns true when the given time falls in the 00:00–06:00 IST window.
func inQuietWindow(t time.Time) bool {
	h := t.In(istLocation).Hour()
	return h >= quietStartHour && h < quietEndHour
}

// orangeStores returns all (service, store) pairs in wave mode for this tick.
// Wave eligibility is wall-clock: started >= quietWaveStart(advisory_time + waveDelay).
func (b *outbreakBot) orangeStores(ctx context.Context, tick int, started time.Time) ([]orangeStore, error) {
	// advisory_time: started_at of the tick when the advisory was published (from game_ticks).
	// NULL means the advisory has not been published yet.
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
		    gt.started_at AS advisory_time
		FROM game_services gs
		CROSS JOIN LATERAL (
		    SELECT generate_series(0, GREATEST(gs.flag_stores, 1) - 1) AS store_index
		) s
		LEFT JOIN LATERAL (
		    SELECT MIN(gc.tick_number) AS first_blood_tick
		    FROM game_captures gc
		    JOIN game_flags gf ON gf.id = gc.flag_id
		    WHERE gf.service_id = gs.id AND gf.store_index = s.store_index
		    HAVING COUNT(DISTINCT gc.victim_team_id) >= $1
		) fb ON TRUE
		LEFT JOIN LATERAL (
		    SELECT ge.tick_number AS advisory_tick
		    FROM game_events ge
		    WHERE ge.kind = 'advisory' AND ge.service_id = gs.id AND ge.store_index = s.store_index
		    ORDER BY ge.tick_number ASC LIMIT 1
		) adv ON TRUE
		LEFT JOIN game_ticks gt ON gt.tick_number = adv.advisory_tick
		WHERE gs.enabled = true AND gs.checker_ref IS NOT NULL AND gs.checker_ref <> ''
		  AND gs.port BETWEEN 1 AND 65535
	`, firstBloodThreshold)
	if err != nil {
		return nil, fmt.Errorf("orange stores: %w", err)
	}
	defer rows.Close()

	waveDelay := time.Duration(orangeDelay) * b.cfg.TickInterval
	if waveDelay <= 0 {
		waveDelay = time.Duration(orangeDelay) * 2 * time.Minute
	}

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
			advisoryTime *time.Time
		)
		if err := rows.Scan(&id, &slug, &checkerRef, &port, &waveExplJSON, &storeIndex,
			&advisoryAt, &firstBlood, &advisoryTime); err != nil {
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

		// advisory publishes at first blood (or advisory_at) on the first tick that
		// is outside the quiet window (00:00–06:00 IST). advisory_time is used as the
		// wall-clock anchor for wave_start_time = quietWaveStart(advisory_time + waveDelay).
		if advisoryTime == nil {
			triggerMet := (firstBlood != nil && tick >= *firstBlood) ||
				(advisoryAt != nil && !advisoryAt.After(started))
			if triggerMet && !inQuietWindow(started) {
				b.publishAdvisory(ctx, tick, id, storeIndex)
				advisoryTime = &started
			}
		}
		if advisoryTime == nil {
			continue
		}

		// wave fires when the tick's wall-clock start reaches wave_start_time.
		// quietWaveStart pushes times inside [00:00, 06:00) IST to 06:30 that morning.
		waveStartTime := quietWaveStart(advisoryTime.Add(waveDelay))
		if started.Before(waveStartTime) {
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

// quietWaveStart adjusts a wave start time that falls inside [00:00, 06:00) IST
// to 06:30 IST that morning, ensuring waves never start during the quiet window.
func quietWaveStart(t time.Time) time.Time {
	local := t.In(istLocation)
	if h := local.Hour(); h >= quietStartHour && h < quietEndHour {
		return time.Date(local.Year(), local.Month(), local.Day(),
			6, 30, 0, 0, istLocation)
	}
	return t
}

type outbreakFlag struct {
	id         uuid.UUID
	value      string
	attackInfo string
}

// currentFlag returns the flag and attack_info for (team, service, store) planted this tick.
func (b *outbreakBot) currentFlag(ctx context.Context, tick int, teamID, serviceID uuid.UUID, storeIndex int) (outbreakFlag, error) {
	var flag outbreakFlag
	err := b.db.Pool.QueryRow(ctx,
		`SELECT id, flag, COALESCE(attack_info, '') FROM game_flags
		 WHERE tick_number = $1 AND team_id = $2 AND service_id = $3 AND store_index = $4
		   AND planted_at IS NOT NULL`,
		tick, teamID, serviceID, storeIndex,
	).Scan(&flag.id, &flag.value, &flag.attackInfo)
	return flag, err
}

type botRead struct {
	flagID  uuid.UUID
	teamID  uuid.UUID
	isNOP   bool
	outcome string
	latency time.Duration
}

func newExploitTask(cfg config.GameConfig, tick int, store orangeStore, team greenTeam, flag outbreakFlag, timeout time.Duration) Task {
	digest := sha256.Sum256([]byte(flag.value))
	return Task{
		Tick:        tick,
		RelatedTick: tick,
		TeamNumber:  team.number,
		TeamName:    team.name,
		Method:      MethodExploit,
		Variant:     store.waveVariant,
		Flag:        flag.value,
		AttackInfo:  flag.attackInfo,
		FlagRegex:   regexp.QuoteMeta(cfg.FlagPrefix) + `\{[0-9a-f]{32}\}`,
		FlagHash:    hex.EncodeToString(digest[:]),
		ChainID: fmt.Sprintf("outbreak_s%s_t%s_i%d_tick%d",
			store.serviceID, team.id, store.storeIndex, tick),
		Timeout:     timeout,
		RoundLength: cfg.TickInterval,
	}
}

// callExploit calls the exploit checker for one (team, service, store). The
// result is not persisted until the NOP canary for the same store and tick wins.
func (b *outbreakBot) callExploit(
	ctx context.Context,
	tick int,
	store orangeStore,
	team greenTeam,
	flag outbreakFlag,
	checker Checker,
	taskTimeout time.Duration,
) (botRead, error) {
	task := newExploitTask(b.cfg, tick, store, team, flag, taskTimeout)
	id, err := b.openTask(team.id, store.serviceID, task)
	if err != nil {
		return botRead{}, fmt.Errorf("open checker task: %w", err)
	}
	if id <= 0 {
		return botRead{}, fmt.Errorf("open checker task returned invalid id %d", id)
	}
	task.ID = id

	tctx, cancel := context.WithTimeout(ctx, taskTimeout)
	res := checker.Run(tctx, Target{Host: team.vulnboxIP, Port: store.port}, task)
	cancel()
	res.Step = task.Method
	auditResult := res
	auditResult.Flag = ""
	auditResult.Code = ""
	auditResult.Message = ""
	if err := b.closeTask(id, auditResult); err != nil {
		return botRead{}, fmt.Errorf("close checker task %d: %w", id, err)
	}

	return botRead{flagID: flag.id, teamID: team.id, isNOP: team.isNOP, outcome: botOutcome(res, flag.value), latency: res.Latency}, nil
}

func (b *outbreakBot) openTask(teamID, serviceID uuid.UUID, task Task) (int64, error) {
	if b.taskOpen != nil {
		return b.taskOpen(teamID, serviceID, task)
	}
	return openCheckerTask(b.db, teamID, serviceID, task)
}

func (b *outbreakBot) closeTask(id int64, res Result) error {
	if b.taskClose != nil {
		return b.taskClose(id, res)
	}
	return closeCheckerTask(b.db, id, res)
}

func canariedBotReads(reads []botRead) []botRead {
	for _, read := range reads {
		if read.isNOP && read.outcome == "stolen" {
			return reads
		}
	}
	return nil
}

func (b *outbreakBot) runStore(ctx context.Context, tick int, store orangeStore, teams []greenTeam, checker Checker, taskTimeout time.Duration) {
	type candidate struct {
		team greenTeam
		flag outbreakFlag
	}
	candidates := make([]candidate, 0, len(teams))
	for _, team := range teams {
		flag, err := b.currentFlag(ctx, tick, team.id, store.serviceID, store.storeIndex)
		if err != nil || flag.value == "" {
			b.logger.Warn("outbreak: incomplete flag batch",
				zap.Int("tick", tick), zap.Stringer("service", store.serviceID),
				zap.Int("store", store.storeIndex), zap.Stringer("team", team.id), zap.Error(err))
			return
		}
		candidates = append(candidates, candidate{team: team, flag: flag})
	}

	type callResult struct {
		read botRead
		err  error
	}
	results := make(chan callResult, len(candidates))
	var wg sync.WaitGroup
	for _, item := range candidates {
		wg.Add(1)
		go func(candidate candidate) {
			defer wg.Done()
			read, err := b.callExploit(ctx, tick, store, candidate.team, candidate.flag, checker, taskTimeout)
			results <- callResult{read: read, err: err}
		}(item)
	}
	wg.Wait()
	close(results)

	reads := make([]botRead, 0, len(teams))
	for result := range results {
		if result.err != nil {
			b.logger.Warn("outbreak: incomplete checker batch",
				zap.Int("tick", tick), zap.Stringer("service", store.serviceID),
				zap.Int("store", store.storeIndex), zap.Error(result.err))
			return
		}
		reads = append(reads, result.read)
	}
	approved := canariedBotReads(reads)
	if len(approved) != len(candidates) {
		return
	}
	if err := b.recordBotReads(tick, approved); err != nil {
		b.logger.Warn("outbreak: record bot read batch",
			zap.Int("tick", tick), zap.Stringer("service", store.serviceID),
			zap.Int("store", store.storeIndex), zap.Error(err))
	}
}

func (b *outbreakBot) recordBotReads(tick int, reads []botRead) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	tx, err := b.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, read := range reads {
		if _, err := tx.Exec(ctx,
			`INSERT INTO game_bot_reads (flag_id, tick_number, outcome, latency_ms)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (flag_id, tick_number) DO NOTHING`,
			read.flagID, tick, read.outcome, read.latency.Milliseconds(),
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// botOutcome maps a checker result to the game_bot_reads outcome vocabulary.
func botOutcome(res Result, expectedFlag string) string {
	switch res.Status {
	case models.SLAOk:
		// checker returned OK; stolen only if its dedicated flag field matches.
		if res.Flag == expectedFlag {
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
