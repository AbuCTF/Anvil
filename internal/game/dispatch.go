package game

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// writes outlive the tick deadline on purpose: a verdict reached in the last second
// must still land, or the job reruns and the team is checked twice
const dbWriteTimeout = 5 * time.Second

// Dispatcher runs every checker task for every active team x released service on a
// tick and records one SLA verdict per pair.
type Dispatcher struct {
	db             *database.DB
	logger         *zap.Logger
	flagPrefix     string
	flagValidTicks int
	tickLength     time.Duration
	checker        config.CheckerConfig
	client         *http.Client
}

type job struct {
	team          uuid.UUID
	teamNumber    int
	teamName      string
	service       uuid.UUID
	target        Target
	checker       Checker
	flagVariants  int
	noiseVariants int
	havocVariants int
	sem           chan struct{}
}

// tickPlan places tasks inside the tick the way enowars does: putflag/putnoise in the
// first quarter, old-flag getflags in the second and third, current getflag/getnoise
// in the third, havoc anywhere in the first three. Nothing runs in the tail margin.
type tickPlan struct {
	start   time.Time
	end     time.Time // every task is done by here
	quarter time.Duration
	timeout time.Duration
	spread  bool
}

func (d *Dispatcher) plan(start time.Time) tickPlan {
	q := d.tickLength / 4
	timeout := d.checker.TaskTimeout
	if timeout <= 0 || timeout > q {
		timeout = q
	}
	return tickPlan{
		start:   start,
		end:     start.Add(d.tickLength - d.checker.TailMargin),
		quarter: q,
		timeout: timeout,
		spread:  d.checker.Spread,
	}
}

// pick returns a start time inside [lo, hi) quarters of the tick, clamped so the task
// can still finish before the tail. Without spread every task starts immediately.
func (p tickPlan) pick(lo, hi float64) time.Time {
	if !p.spread {
		return p.start
	}
	latest := p.end.Add(-p.timeout)
	a := p.start.Add(time.Duration(lo * float64(p.quarter)))
	b := p.start.Add(time.Duration(hi * float64(p.quarter)))
	if b.After(latest) {
		b = latest
	}
	if !b.After(a) {
		if a.After(latest) {
			return latest
		}
		return a
	}
	return a.Add(time.Duration(rand.Int64N(int64(b.Sub(a)))))
}

func (d *Dispatcher) dispatch(ctx context.Context, tick int, started time.Time) error {
	jobs, err := d.buildJobs(ctx, tick)
	if err != nil {
		return fmt.Errorf("build jobs: %w", err)
	}
	p := d.plan(started)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs error
	)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			if err := d.runJob(ctx, tick, p, j); err != nil {
				mu.Lock()
				errs = errors.Join(errs, err)
				mu.Unlock()
			}
		}(j)
	}
	wg.Wait()
	return errs
}

func chainID(kind string, j job, tick, variant int) string {
	return fmt.Sprintf("%s_s%s_r%d_t%s_i%d", kind, j.service, tick, j.team, variant)
}

func (d *Dispatcher) runJob(ctx context.Context, tick int, p tickPlan, j job) error {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		current []Result
		old     []Result
		jobErr  error
	)
	add := func(isCurrent bool, r Result) {
		mu.Lock()
		defer mu.Unlock()
		if isCurrent {
			current = append(current, r)
		} else {
			old = append(old, r)
		}
	}
	fail := func(err error) {
		mu.Lock()
		jobErr = errors.Join(jobErr, err)
		mu.Unlock()
	}
	base := Task{Tick: tick, RelatedTick: tick, TeamNumber: j.teamNumber, TeamName: j.teamName}

	for v := 0; v < j.flagVariants; v++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			flagID, flag, planted, err := d.reserveFlag(ctx, tick, j.team, j.service, v)
			if err != nil {
				fail(fmt.Errorf("reserve flag team %s service %s: %w", j.team, j.service, err))
				add(true, Result{Status: models.SLAError, Message: "flag reservation failed"})
				return
			}
			task := base
			task.Variant, task.Flag, task.ChainID = v, flag, chainID("flag", j, tick, v)
			if !planted {
				// a retried tick verifies an already-planted value rather than placing it twice
				task.Method = MethodPutFlag
				res := d.runTask(ctx, p, j, task, p.pick(0, 1))
				add(true, res)
				if res.Status != models.SLAOk {
					return
				}
				if err := d.markFlagPlanted(tick, j.team, j.service, v, res.AttackInfo); err != nil {
					fail(fmt.Errorf("mark flag planted team %s service %s: %w", j.team, j.service, err))
					return
				}
			}
			task.Method = MethodGetFlag
			res := d.runTask(ctx, p, j, task, p.pick(2, 3))
			d.recordRead(flagID, tick, res)
			add(true, res)
		}(v)
	}

	olds, err := d.oldFlags(ctx, tick, j.team, j.service)
	if err != nil {
		fail(fmt.Errorf("load old flags team %s service %s: %w", j.team, j.service, err))
	}
	for _, f := range olds {
		wg.Add(1)
		go func(f oldFlag) {
			defer wg.Done()
			task := base
			task.Method, task.Variant, task.Flag = MethodGetFlag, f.variant, f.flag
			task.RelatedTick, task.ChainID = f.tick, chainID("flag", j, f.tick, f.variant)
			res := d.runTask(ctx, p, j, task, p.pick(1, 3))
			d.recordRead(f.id, tick, res)
			add(false, res)
		}(f)
	}

	for v := 0; v < j.noiseVariants; v++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			task := base
			task.Method, task.Variant, task.ChainID = MethodPutNoise, v, chainID("noise", j, tick, v)
			res := d.runTask(ctx, p, j, task, p.pick(0, 1))
			add(true, res)
			if res.Status != models.SLAOk {
				return
			}
			task.Method = MethodGetNoise
			add(true, d.runTask(ctx, p, j, task, p.pick(2, 3)))
		}(v)
	}

	for v := 0; v < j.havocVariants; v++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			task := base
			task.Method, task.Variant, task.ChainID = MethodHavoc, v, chainID("havoc", j, tick, v)
			add(true, d.runTask(ctx, p, j, task, p.pick(0, 3)))
		}(v)
	}

	wg.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return jobErr // shutting down: no verdict, so the resumed tick reruns this job
	}
	verdict, ok := aggregate(current, old)
	if !ok {
		return jobErr
	}
	if err := d.recordSLA(tick, j.team, j.service, verdict); err != nil {
		fail(fmt.Errorf("record SLA team %s service %s: %w", j.team, j.service, err))
	}
	return jobErr
}

// runTask waits for its slot in the tick, then runs one checker call with its own
// timeout, so one hung checker costs that task and nothing else.
func (d *Dispatcher) runTask(ctx context.Context, p tickPlan, j job, task Task, at time.Time) Result {
	if !sleepUntil(ctx, at) {
		return Result{Status: models.SLAError, Message: "tick cancelled"}
	}
	select {
	case j.sem <- struct{}{}:
		defer func() { <-j.sem }()
	case <-ctx.Done():
		return Result{Status: models.SLAError, Message: "tick cancelled"}
	}

	timeout := p.timeout
	if rem := time.Until(p.end); rem < timeout {
		timeout = rem
	}
	if timeout < time.Second {
		// we ran out of tick (a restart mid-tick, or an overloaded runner): our fault
		return Result{Status: models.SLAError, Message: "tick window elapsed"}
	}
	task.Timeout, task.RoundLength = timeout, d.tickLength

	id, err := d.openTask(j, task)
	if err != nil {
		d.logger.Warn("checker task log", zap.Error(err))
	}
	task.ID = id

	tctx, cancel := context.WithTimeout(ctx, timeout)
	res := j.checker.Run(tctx, j.target, task)
	cancel()
	res = sanitizeCheckerResult(res)
	res.Step = task.Method

	if id != 0 {
		if err := d.closeTask(id, res); err != nil {
			d.logger.Warn("checker task log", zap.Error(err))
		}
	}
	return res
}

func sleepUntil(ctx context.Context, at time.Time) bool {
	wait := time.Until(at)
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// severity orders verdicts worst-last. ERROR outranks everything, as in enowars: when
// our checker broke we cannot say the service did, so the tick is credited instead.
func severity(s models.SLAStatus) int {
	switch s {
	case models.SLAOk:
		return 0
	case models.SLARecovering:
		return 1
	case models.SLAFlagNotFound:
		return 2
	case models.SLAFaulty:
		return 3
	case models.SLADown:
		return 4
	default:
		return 5
	}
}

// aggregate folds a job's tasks into one verdict: the worst current-tick result, and
// RECOVERING when the service works now but lost flags from earlier ticks.
func aggregate(current, old []Result) (Result, bool) {
	if len(current) == 0 && len(old) == 0 {
		return Result{}, false
	}
	verdict := Result{Status: models.SLAOk}
	var slowest time.Duration
	for _, r := range current {
		if severity(r.Status) > severity(verdict.Status) {
			verdict = r
		}
		slowest = max(slowest, r.Latency)
	}
	verdict.Latency = slowest
	if verdict.Status != models.SLAOk {
		return verdict, true
	}
	for _, r := range old {
		// an old check our own checker botched is not the team's lost flag
		if r.Status != models.SLAOk && r.Status != models.SLAError {
			verdict.Status = models.SLARecovering
			break
		}
	}
	return sanitizeCheckerResult(verdict), true
}

type serviceTarget struct {
	id            uuid.UUID
	port          int
	checker       string
	flagVariants  int
	noiseVariants int
	havocVariants int
}

type teamTarget struct {
	id     uuid.UUID
	number int
	name   string
	host   string
}

type jobKey struct {
	team    uuid.UUID
	service uuid.UUID
}

func (d *Dispatcher) buildJobs(ctx context.Context, tick int) ([]job, error) {
	services, err := d.releasedServices(ctx)
	if err != nil {
		return nil, err
	}
	teams, err := d.teamTargets(ctx)
	if err != nil {
		return nil, err
	}
	completed, err := d.completedJobs(ctx, tick)
	if err != nil {
		return nil, err
	}

	concurrency := d.checker.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	jobs := make([]job, 0, len(services)*len(teams))
	for _, s := range services {
		// one pool per service: a hung checker queues its own tasks, not everyone's
		sem := make(chan struct{}, concurrency)
		checker := checkerFor(s.checker, d.client)
		for _, t := range teams {
			if completed[jobKey{team: t.id, service: s.id}] {
				continue
			}
			jobs = append(jobs, job{
				team:          t.id,
				teamNumber:    t.number,
				teamName:      t.name,
				service:       s.id,
				target:        Target{Host: t.host, Port: s.port},
				checker:       checker,
				flagVariants:  s.flagVariants,
				noiseVariants: s.noiseVariants,
				havocVariants: s.havocVariants,
				sem:           sem,
			})
		}
	}
	return jobs, nil
}

func (d *Dispatcher) completedJobs(ctx context.Context, tick int) (map[jobKey]bool, error) {
	rows, err := d.db.Pool.Query(ctx,
		`SELECT team_id, service_id FROM game_sla_checks WHERE tick_number = $1`, tick)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	completed := make(map[jobKey]bool)
	for rows.Next() {
		var key jobKey
		if err := rows.Scan(&key.team, &key.service); err != nil {
			return nil, err
		}
		completed[key] = true
	}
	return completed, rows.Err()
}

func (d *Dispatcher) releasedServices(ctx context.Context) ([]serviceTarget, error) {
	rows, err := d.db.Pool.Query(ctx,
		`SELECT id, port, checker_ref, GREATEST(flag_stores, 1), noise_variants, havoc_variants
		 FROM game_services
		 WHERE enabled = TRUE AND port IS NOT NULL AND checker_ref IS NOT NULL AND checker_ref <> ''
		   AND (release_at IS NULL OR release_at <= NOW())`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []serviceTarget
	for rows.Next() {
		var s serviceTarget
		if err := rows.Scan(&s.id, &s.port, &s.checker, &s.flagVariants, &s.noiseVariants, &s.havocVariants); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// teamTargets includes the nop team: it gets flags and checks like everyone else so
// teams can test exploits on it. standings already leave it out.
func (d *Dispatcher) teamTargets(ctx context.Context) ([]teamTarget, error) {
	rows, err := d.db.Pool.Query(ctx,
		`SELECT id, COALESCE(team_number, 0), name, host(vulnbox_ip) FROM game_teams
		 WHERE status = 'active' AND vulnbox_ip IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []teamTarget
	for rows.Next() {
		var t teamTarget
		if err := rows.Scan(&t.id, &t.number, &t.name, &t.host); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type oldFlag struct {
	id      uuid.UUID
	tick    int
	variant int
	flag    string
}

// oldFlags are the planted flags from the last checked ticks, all re-checked every
// tick so a team that drops older flags loses SLA for it.
func (d *Dispatcher) oldFlags(ctx context.Context, tick int, team, service uuid.UUID) ([]oldFlag, error) {
	checked := d.checker.CheckedTicks
	if checked <= 0 {
		checked = d.flagValidTicks
	}
	rows, err := d.db.Pool.Query(ctx,
		`SELECT id, tick_number, store_index, flag FROM game_flags
		 WHERE team_id = $1 AND service_id = $2 AND planted_at IS NOT NULL
		   AND tick_number < $3 AND tick_number >= $3 - $4
		 ORDER BY tick_number, store_index`, team, service, tick, checked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []oldFlag
	for rows.Next() {
		var f oldFlag
		if err := rows.Scan(&f.id, &f.tick, &f.variant, &f.flag); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// reserveFlag persists the value before invoking the checker. If the process dies
// between those operations, retrying the running tick places the same value;
// planted_at keeps the reservation from becoming submittable early.
func (d *Dispatcher) reserveFlag(ctx context.Context, tick int, team, service uuid.UUID, variant int) (uuid.UUID, string, bool, error) {
	var id uuid.UUID
	var flag string
	var planted bool
	err := d.db.Pool.QueryRow(ctx,
		`INSERT INTO game_flags
		   (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at)
		 VALUES ($1, $2, $3, $4, $5, $1, $6, NULL)
		 ON CONFLICT (tick_number, team_id, service_id, store_index) DO UPDATE
		 SET tick_number = EXCLUDED.tick_number
		 RETURNING id, flag, planted_at IS NOT NULL`,
		tick, team, service, variant, mintFlag(d.flagPrefix), tick+d.flagValidTicks,
	).Scan(&id, &flag, &planted)
	return id, flag, planted, err
}

// recordRead notes whether a getflag read the flag back on this tick; the mirror
// life rule needs every tick of a flag's life, not just the last verdict.
func (d *Dispatcher) recordRead(flagID uuid.UUID, tick int, res Result) {
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	if _, err := d.db.Pool.Exec(ctx,
		`INSERT INTO game_flag_checks (flag_id, tick_number, readable) VALUES ($1, $2, $3)
		 ON CONFLICT (flag_id, tick_number) DO UPDATE SET readable = EXCLUDED.readable, checked_at = NOW()`,
		flagID, tick, res.Status == models.SLAOk); err != nil {
		d.logger.Warn("record flag read", zap.Error(err))
	}
}

func (d *Dispatcher) markFlagPlanted(tick int, team, service uuid.UUID, variant int, attackInfo string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	tag, err := d.db.Pool.Exec(ctx,
		`UPDATE game_flags SET planted_at = NOW(), attack_info = NULLIF($5, '')
		 WHERE tick_number = $1 AND team_id = $2 AND service_id = $3 AND store_index = $4
		   AND planted_at IS NULL`, tick, team, service, variant, attackInfo)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("expected one reserved flag, updated %d", tag.RowsAffected())
	}
	return nil
}

func (d *Dispatcher) openTask(j job, task Task) (int64, error) {
	return openCheckerTask(d.db, j.team, j.service, task)
}

func openCheckerTask(db *database.DB, teamID, serviceID uuid.UUID, task Task) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	var id int64
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO game_checker_tasks (tick_number, related_tick, team_id, service_id, method, variant)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		task.Tick, task.RelatedTick, teamID, serviceID, task.Method, task.Variant).Scan(&id)
	return id, err
}

func (d *Dispatcher) closeTask(id int64, res Result) error {
	return closeCheckerTask(d.db, id, res)
}

func closeCheckerTask(db *database.DB, id int64, res Result) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	res = sanitizeCheckerResult(res)
	_, err := db.Pool.Exec(ctx,
		`UPDATE game_checker_tasks SET status = $2, code = NULLIF($3, ''), message = NULLIF($4, ''), latency_ms = $5, finished_at = NOW()
		 WHERE id = $1`, id, string(res.Status), res.Code, res.Message, int(res.Latency.Milliseconds()))
	return err
}

func (d *Dispatcher) recordSLA(tick int, team, service uuid.UUID, res Result) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	res = sanitizeCheckerResult(res)
	// stamp the image that was live, so a disputed verdict names the exact build
	_, err := d.db.Pool.Exec(ctx,
		`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status, latency_ms, message, step, code, image_digest)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''),
		   (SELECT image_digest FROM game_deploys WHERE team_id = $2 AND service_id = $3
		    ORDER BY deployed_at DESC LIMIT 1))
		 ON CONFLICT (tick_number, team_id, service_id) DO UPDATE SET
		   status = EXCLUDED.status,
		   latency_ms = EXCLUDED.latency_ms,
		   message = EXCLUDED.message,
		   step = EXCLUDED.step,
		   code = EXCLUDED.code,
		   image_digest = EXCLUDED.image_digest,
		   checked_at = NOW()`,
		tick, team, service, string(res.Status), int(res.Latency.Milliseconds()), res.Message, res.Step, res.Code)
	return err
}
