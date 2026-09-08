package game

import (
	"context"
	"sync"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
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

func (d *Dispatcher) dispatch(ctx context.Context, tick int) {
	jobs, err := d.buildJobs(ctx)
	if err != nil {
		d.logger.Error("dispatch: build jobs", zap.Int("tick", tick), zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}

	workers := dispatchWorkers
	if len(jobs) < workers {
		workers = len(jobs)
	}

	ch := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				d.runJob(ctx, tick, j)
			}
		}()
	}
	for _, j := range jobs {
		select {
		case <-ctx.Done():
		case ch <- j:
		}
	}
	close(ch)
	wg.Wait()
}

func (d *Dispatcher) runJob(ctx context.Context, tick int, j job) {
	flag := mintFlag(d.flagPrefix)
	res := j.checker.Place(ctx, j.target, flag)

	if res.Status == models.SLAOk {
		if err := d.insertFlag(ctx, tick, j.team, j.service, flag); err != nil {
			d.logger.Warn("dispatch: insert flag", zap.Error(err))
		}
	}
	d.recordSLA(ctx, tick, j.team, j.service, res)
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

func (d *Dispatcher) buildJobs(ctx context.Context) ([]job, error) {
	services, err := d.enabledServices(ctx)
	if err != nil {
		return nil, err
	}
	teams, err := d.teamTargets(ctx)
	if err != nil {
		return nil, err
	}

	jobs := make([]job, 0, len(services)*len(teams))
	for _, s := range services {
		for _, t := range teams {
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
		`SELECT id, vulnbox_ip::text FROM game_teams
		 WHERE status = 'active' AND vulnbox_ip IS NOT NULL`)
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

func (d *Dispatcher) insertFlag(ctx context.Context, tick int, team, service uuid.UUID, flag string) error {
	_, err := d.db.Pool.Exec(ctx,
		`INSERT INTO game_flags (tick_number, team_id, service_id, store_index, flag, valid_from_tick, valid_until_tick)
		 VALUES ($1, $2, $3, 0, $4, $1, $5)`,
		tick, team, service, flag, tick+d.flagValidTicks)
	return err
}

func (d *Dispatcher) recordSLA(ctx context.Context, tick int, team, service uuid.UUID, res Result) {
	_, err := d.db.Pool.Exec(ctx,
		`INSERT INTO game_sla_checks (tick_number, team_id, service_id, status, latency_ms, message)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (tick_number, team_id, service_id) DO NOTHING`,
		tick, team, service, string(res.Status), int(res.Latency.Milliseconds()), res.Message)
	if err != nil {
		d.logger.Warn("dispatch: record sla", zap.Error(err))
	}
}
