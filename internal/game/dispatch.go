package game

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const dispatchWorkers = 32

// Dispatcher plants a flag and records an SLA verdict for every active
// team × enabled service on each tick.
type Dispatcher struct {
	db             *database.DB
	logger         *zap.Logger
	flagPrefix     string
	flagValidTicks int
}

type job struct {
	team    uuid.UUID
	service uuid.UUID
	target  Target
	checker Checker
}

func (d *Dispatcher) dispatch(ctx context.Context, tick int) error {
	jobs, err := d.buildJobs(ctx, tick)
	if err != nil {
		return fmt.Errorf("build jobs: %w", err)
	}
	if len(jobs) == 0 {
		return nil
	}

	workers := dispatchWorkers
	if len(jobs) < workers {
		workers = len(jobs)
	}

	ch := make(chan job)
	errCh := make(chan error, len(jobs))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				if err := d.runJob(ctx, tick, j); err != nil {
					errCh <- err
				}
			}
		}()
	}

	dispatching := true
	for _, j := range jobs {
		select {
		case <-ctx.Done():
			dispatching = false
		case ch <- j:
		}
		if !dispatching {
			break
		}
	}
	close(ch)
	wg.Wait()
	close(errCh)

	var dispatchErr error
	for err := range errCh {
		dispatchErr = errors.Join(dispatchErr, err)
	}
	if ctx.Err() != nil {
		dispatchErr = errors.Join(dispatchErr, ctx.Err())
	}
	return dispatchErr
}

func (d *Dispatcher) runJob(ctx context.Context, tick int, j job) error {
	previous, hasPrevious, err := d.previousFlag(ctx, tick, j.team, j.service)
	if err != nil {
		return fmt.Errorf("load previous flag for team %s service %s: %w", j.team, j.service, err)
	}

	var previousResult *Result
	if hasPrevious {
		checked := j.checker.Check(ctx, j.target, previous)
		previousResult = &checked
	}

	flag, planted, err := d.reserveFlag(ctx, tick, j.team, j.service)
	if err != nil {
		return fmt.Errorf("reserve flag for team %s service %s: %w", j.team, j.service, err)
	}

	var current Result
	if planted {
		// A retry of an interrupted tick verifies the already-planted value rather
		// than placing a second copy or minting an untracked replacement.
		current = j.checker.Check(ctx, j.target, flag)
	} else {
		current = j.checker.Place(ctx, j.target, flag)
		if current.Status == models.SLAOk {
			if err := d.markFlagPlanted(ctx, tick, j.team, j.service); err != nil {
				return fmt.Errorf("mark flag planted for team %s service %s: %w", j.team, j.service, err)
			}
		}
	}

	verdict := current
	if current.Status == models.SLAOk && previousResult != nil && previousResult.Status != models.SLAOk {
		verdict = *previousResult
	}
	if err := d.recordSLA(ctx, tick, j.team, j.service, verdict); err != nil {
		return fmt.Errorf("record SLA for team %s service %s: %w", j.team, j.service, err)
	}
	return nil
}

type serviceTarget struct {
	id      uuid.UUID
	port    int
	checker string
}

type teamTarget struct {
	id   uuid.UUID
	host string
}

type jobKey struct {
	team    uuid.UUID
	service uuid.UUID
}

func (d *Dispatcher) buildJobs(ctx context.Context, tick int) ([]job, error) {
	services, err := d.enabledServices(ctx)
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

	jobs := make([]job, 0, len(services)*len(teams))
	for _, s := range services {
		for _, t := range teams {
			if completed[jobKey{team: t.id, service: s.id}] {
				continue
			}
			jobs = append(jobs, job{
				team:    t.id,
				service: s.id,
				target:  Target{Host: t.host, Port: s.port},
				checker: execChecker{command: s.checker},
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

func (d *Dispatcher) enabledServices(ctx context.Context) ([]serviceTarget, error) {
	rows, err := d.db.Pool.Query(ctx,
		`SELECT id, port, checker_ref FROM game_services
		 WHERE enabled = TRUE AND port IS NOT NULL AND checker_ref IS NOT NULL AND checker_ref <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []serviceTarget
	for rows.Next() {
		var s serviceTarget
		if err := rows.Scan(&s.id, &s.port, &s.checker); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *Dispatcher) teamTargets(ctx context.Context) ([]teamTarget, error) {
	rows, err := d.db.Pool.Query(ctx,
		`SELECT id, host(vulnbox_ip) FROM game_teams
		 WHERE status = 'active' AND is_nop = FALSE AND vulnbox_ip IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []teamTarget
	for rows.Next() {
		var t teamTarget
		if err := rows.Scan(&t.id, &t.host); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *Dispatcher) previousFlag(ctx context.Context, tick int, team, service uuid.UUID) (string, bool, error) {
	var flag string
	err := d.db.Pool.QueryRow(ctx,
		`SELECT flag FROM game_flags
		 WHERE team_id = $1 AND service_id = $2 AND tick_number < $3
		   AND planted_at IS NOT NULL
		 ORDER BY tick_number DESC, store_index ASC
		 LIMIT 1`, team, service, tick).Scan(&flag)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return flag, true, nil
}

// reserveFlag persists the value before invoking the checker. If the process
// dies between those operations, retrying the running tick places the same
// value; planted_at keeps the reservation from becoming submittable early.
func (d *Dispatcher) reserveFlag(ctx context.Context, tick int, team, service uuid.UUID) (string, bool, error) {
	var flag string
	var planted bool
	err := d.db.Pool.QueryRow(ctx,
		`INSERT INTO game_flags
		   (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick, planted_at)
		 VALUES ($1, $2, $3, 0, $4, $1, $5, NULL)
		 ON CONFLICT (tick_number, team_id, service_id, store_index) DO UPDATE
		 SET tick_number = EXCLUDED.tick_number
		 RETURNING flag, planted_at IS NOT NULL`,
		tick, team, service, mintFlag(d.flagPrefix), tick+d.flagValidTicks,
	).Scan(&flag, &planted)
	return flag, planted, err
}

func (d *Dispatcher) markFlagPlanted(ctx context.Context, tick int, team, service uuid.UUID) error {
	tag, err := d.db.Pool.Exec(ctx,
		`UPDATE game_flags SET planted_at = NOW()
		 WHERE tick_number = $1 AND team_id = $2 AND service_id = $3 AND store_index = 0
		   AND planted_at IS NULL`, tick, team, service)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("expected one reserved flag, updated %d", tag.RowsAffected())
	}
	return nil
}

func (d *Dispatcher) recordSLA(ctx context.Context, tick int, team, service uuid.UUID, res Result) error {
	_, err := d.db.Pool.Exec(ctx,
		`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status, latency_ms, message)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (tick_number, team_id, service_id) DO UPDATE SET
		   status = EXCLUDED.status,
		   latency_ms = EXCLUDED.latency_ms,
		   message = EXCLUDED.message,
		   checked_at = NOW()`,
		tick, team, service, string(res.Status), int(res.Latency.Milliseconds()), res.Message)
	return err
}
