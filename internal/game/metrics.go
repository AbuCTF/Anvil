package game

// metrics.go — engine health snapshot queried from the live DB.
// Called by GET /api/v1/internal/arena/metrics (auth: same internal token).

import (
	"context"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
)

// EngineMetrics is the full snapshot returned by QueryMetrics.
type EngineMetrics struct {
	Tick     TickMetrics     `json:"tick"`
	Services []ServiceHealth `json:"services"`
}

// TickMetrics describes the current / most recent tick.
type TickMetrics struct {
	Current   int     `json:"current"`    // tick number of the latest row
	Status    string  `json:"status"`     // "running" | "closed"
	OpenForS  float64 `json:"open_for_s"` // seconds since started_at (0 when closed)
	IntervalS int     `json:"interval_s"` // configured tick interval, for context
}

// ServiceHealth is per-service health over the last lookback ticks.
type ServiceHealth struct {
	Slug         string  `json:"slug"`
	TicksSampled int     `json:"ticks_sampled"`
	TasksTotal   int     `json:"tasks_total"`
	TasksOK      int     `json:"tasks_ok"`
	ErrorRate    float64 `json:"error_rate"`     // (non-OK tasks) / total; 0 when no tasks
	LatencyP95Ms int     `json:"latency_p95_ms"` // 0 when no latency data
}

// QueryMetrics reads the engine health snapshot from the DB.
// tickInterval is the configured tick interval — not stored in the DB.
// lookbackTicks controls how many recent ticks to sample for per-service stats.
func QueryMetrics(ctx context.Context, db *database.DB, tickInterval time.Duration, lookbackTicks int) (EngineMetrics, error) {
	if lookbackTicks <= 0 {
		lookbackTicks = 5
	}

	var m EngineMetrics
	m.Tick.IntervalS = int(tickInterval.Seconds())

	// current tick info
	var startedAt time.Time
	err := db.Pool.QueryRow(ctx,
		`SELECT tick_number, status, started_at
		 FROM game_ticks ORDER BY tick_number DESC LIMIT 1`,
	).Scan(&m.Tick.Current, &m.Tick.Status, &startedAt)
	if err != nil {
		// no ticks yet — return zero struct, not an error
		return m, nil
	}
	if m.Tick.Status == "running" {
		m.Tick.OpenForS = time.Since(startedAt).Seconds()
	}

	// per-service error rate over the last lookbackTicks ticks
	rows, err := db.Pool.Query(ctx, `
		SELECT
		    gs.slug,
		    COUNT(*)::int                                          AS tasks_total,
		    SUM(CASE WHEN ct.status = 'OK' THEN 1 ELSE 0 END)::int AS tasks_ok
		FROM game_checker_tasks ct
		JOIN game_services gs ON gs.id = ct.service_id
		WHERE ct.tick_number > (
		    SELECT COALESCE(MAX(tick_number) - $1, 0) FROM game_ticks
		) AND ct.method <> 'exploit'
		GROUP BY gs.slug
		ORDER BY gs.slug`, lookbackTicks)
	if err != nil {
		return m, err
	}
	defer rows.Close()

	slugIdx := make(map[string]int)
	for rows.Next() {
		var sh ServiceHealth
		if err := rows.Scan(&sh.Slug, &sh.TasksTotal, &sh.TasksOK); err != nil {
			return m, err
		}
		if sh.TasksTotal > 0 {
			sh.ErrorRate = float64(sh.TasksTotal-sh.TasksOK) / float64(sh.TasksTotal)
			// round to 4 decimal places to avoid floating-point noise
			sh.ErrorRate = float64(int(sh.ErrorRate*10000)) / 10000
		}
		slugIdx[sh.Slug] = len(m.Services)
		m.Services = append(m.Services, sh)
	}
	if err := rows.Err(); err != nil {
		return m, err
	}

	// p95 latency per service (same lookback window)
	p95Rows, err := db.Pool.Query(ctx, `
		SELECT
		    gs.slug,
		    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY ct.latency_ms)::int AS p95
		FROM game_checker_tasks ct
		JOIN game_services gs ON gs.id = ct.service_id
		WHERE ct.tick_number > (
		    SELECT COALESCE(MAX(tick_number) - $1, 0) FROM game_ticks
		) AND ct.method <> 'exploit' AND ct.latency_ms IS NOT NULL
		GROUP BY gs.slug`, lookbackTicks)
	if err != nil {
		return m, err
	}
	defer p95Rows.Close()

	for p95Rows.Next() {
		var slug string
		var p95 int
		if err := p95Rows.Scan(&slug, &p95); err != nil {
			return m, err
		}
		if idx, ok := slugIdx[slug]; ok {
			m.Services[idx].LatencyP95Ms = p95
		}
	}
	if err := p95Rows.Err(); err != nil {
		return m, err
	}

	return m, nil
}
