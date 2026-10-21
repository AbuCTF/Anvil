package game

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const gameTickLockID int64 = 0x416e76696c47616d

// Controller drives the game clock. It opens and closes ticks on a fixed
// interval; checker dispatch, KotH control, and scoring hang off each tick.
type Controller struct {
	cfg        config.GameConfig
	db         *database.DB
	logger     *zap.Logger
	dispatcher *Dispatcher
	emitClient *http.Client
	// the outbreak wave bot, run beside dispatch on finals ticks when set
	outbreak func(ctx context.Context, tick int, started time.Time)
}

// outbreakFactory is filled in by the outbreak bot's own file; it returns nil when
// the bot is disabled, so the engine runs without it.
var outbreakFactory func(*Controller) func(ctx context.Context, tick int, started time.Time)

// finishing a finals tick (koth fold, standings, close) gets its own budget, since
// checker tasks may run right up to the tick's tail margin
const finishTimeout = 30 * time.Second

func NewController(cfg config.GameConfig, db *database.DB, logger *zap.Logger) *Controller {
	c := &Controller{
		cfg:    cfg,
		db:     db,
		logger: logger,
		dispatcher: &Dispatcher{
			db:             db,
			logger:         logger,
			flagPrefix:     cfg.FlagPrefix,
			flagValidTicks: cfg.FlagValidTicks,
			tickLength:     cfg.TickInterval,
			checker:        cfg.Checker,
			// no client timeout: every task carries its own deadline
			client: &http.Client{Transport: &http.Transport{
				MaxIdleConns:        512,
				MaxIdleConnsPerHost: 128,
				IdleConnTimeout:     90 * time.Second,
			}},
		},
		emitClient: &http.Client{Timeout: 10 * time.Second},
	}
	if outbreakFactory != nil {
		c.outbreak = outbreakFactory(c)
	}
	return c
}

// Run ticks until ctx is cancelled. It returns immediately unless the engine is enabled.
func (c *Controller) Run(ctx context.Context) {
	if !c.cfg.Enabled {
		return
	}
	if c.cfg.TickInterval <= 0 {
		c.logger.Error("game engine requires a positive tick interval",
			zap.Duration("tick_interval", c.cfg.TickInterval))
		return
	}

	tick, _, err := c.lastTickState(ctx)
	if err != nil {
		c.logger.Error("game engine failed to read tick state", zap.Error(err))
		return
	}

	c.logger.Info("game engine started",
		zap.Duration("tick_interval", c.cfg.TickInterval),
		zap.Int("resume_from_tick", tick))
	if running, ok, err := c.runningTick(ctx); err != nil {
		c.logger.Error("game engine failed to read running tick", zap.Error(err))
		return
	} else if ok && c.tickingAllowed(ctx) {
		if err := c.runTick(ctx, running); err != nil && ctx.Err() == nil {
			c.logger.Error("resume tick failed", zap.Int("tick", running), zap.Error(err))
		}
	}

	ticker := time.NewTicker(c.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("game engine stopped")
			return
		case <-ticker.C:
			// dormant when KotH is off (the stop signal) or the event has ended: keep the
			// loop alive so arena reads still work, but advance no tick - otherwise the engine
			// runs past the close, advancing rounds and writing score snapshots against dead
			// arenas, which pollutes the arena graph. Flipping koth_native_enabled back on
			// resumes ticking live, no restart.
			if !c.tickingAllowed(ctx) {
				continue
			}
			next, err := c.nextTick(ctx)
			if err != nil {
				c.logger.Error("game engine failed to choose next tick", zap.Error(err))
				continue
			}
			if err := c.runTick(ctx, next); err != nil && ctx.Err() == nil {
				c.logger.Error("tick failed", zap.Int("tick", next), zap.Error(err))
			}
		}
	}
}

// tickingAllowed reports whether the engine should advance a tick right now. It goes
// dormant when KotH native scoring is disabled (stop.sh sets koth_native_enabled=false at
// the close) or the event's end_at has passed. The controller's Run loop only starts when
// game.enabled is true, and the arena read endpoints are gated on that same flag - so we
// cannot simply turn game.enabled off to stop ticking without also hiding the arena view.
// This decouples the two: game.enabled stays true (arena stays visible, frozen at its last
// tick), while the loop advances nothing once the event is over.
//
// Finals mode (game.state set) ignores koth_native_enabled: it ticks only while the
// state is running and the clock is inside the event window.
func (c *Controller) tickingAllowed(ctx context.Context) bool {
	state, err := ReadState(ctx, c.db)
	if err != nil {
		c.logger.Warn("game engine failed to read game.state", zap.Error(err))
		return false
	}
	if state != StateLegacy {
		start, end, err := eventWindow(ctx, c.db)
		if err != nil {
			c.logger.Warn("game engine failed to read event window", zap.Error(err))
			return false
		}
		return phaseAt(state, time.Now(), start, end) == PhaseLive
	}
	if !c.kothNativeEnabled(ctx) {
		return false
	}
	var endStr string
	if err := c.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(value #>> '{}', '') FROM platform_settings WHERE key = 'event.end_at'`,
	).Scan(&endStr); err == nil && endStr != "" {
		if end, perr := time.Parse(time.RFC3339, endStr); perr == nil && !time.Now().Before(end) {
			return false
		}
	}
	return true
}

func (c *Controller) runTick(parent context.Context, tick int) error {
	state, err := ReadState(parent, c.db)
	if err != nil {
		return fmt.Errorf("read game state: %w", err)
	}
	if state != StateLegacy {
		return c.runFinalsTick(parent, tick)
	}
	return c.runLegacyTick(parent, tick)
}

// runLegacyTick is the quals tick: koth and standings only. Quals never ran
// attack-defense, so checker dispatch exists only in finals mode.
func (c *Controller) runLegacyTick(parent context.Context, tick int) error {
	ctx, cancel := context.WithTimeout(parent, c.cfg.TickInterval)
	defer cancel()

	release, acquired, err := c.acquireTickLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire tick lock: %w", err)
	}
	if !acquired {
		return nil
	}
	defer release()

	_, runnable, err := c.openTick(ctx, tick)
	if err != nil {
		return fmt.Errorf("open tick: %w", err)
	}
	if !runnable {
		return nil
	}

	// quals shared-arena KotH: mirror bought-in real teams into game_teams so the
	// engine scores them (migration 030). No-op when koth_native_enabled is off.
	native := c.kothNativeEnabled(ctx)
	if native {
		if err := c.syncNativeTeams(ctx); err != nil {
			return fmt.Errorf("sync native teams: %w", err)
		}
	}

	if err := c.runKoth(ctx, tick); err != nil {
		return fmt.Errorf("run KotH: %w", err)
	}
	if err := c.recomputeStandings(ctx); err != nil {
		return fmt.Errorf("recompute standings: %w", err)
	}
	if native {
		if err := c.foldNativeScore(ctx); err != nil {
			return fmt.Errorf("fold native koth score: %w", err)
		}
	}
	if err := c.snapshotStandings(ctx, tick); err != nil {
		return fmt.Errorf("snapshot standings: %w", err)
	}

	if err := c.closeTick(ctx, tick); err != nil {
		return fmt.Errorf("close tick: %w", err)
	}
	c.emitStandings(ctx, tick)
	return nil
}

// runFinalsTick spreads checker tasks across the tick while hills are polled beside
// them. Checker and koth failures are logged, never fatal: a tick that cannot close
// would be retried forever and freeze the whole game.
func (c *Controller) runFinalsTick(parent context.Context, tick int) error {
	release, acquired, err := c.acquireTickLock(parent)
	if err != nil {
		return fmt.Errorf("acquire tick lock: %w", err)
	}
	if !acquired {
		return nil
	}
	defer release()

	started, runnable, err := c.openTick(parent, tick)
	if err != nil {
		return fmt.Errorf("open tick: %w", err)
	}
	if !runnable {
		return nil
	}

	native := c.kothNativeEnabled(parent)
	if native {
		if err := c.syncNativeTeams(parent); err != nil {
			c.logger.Error("sync native teams", zap.Int("tick", tick), zap.Error(err))
		}
	}

	tickCtx, cancelTick := context.WithDeadline(parent, started.Add(c.cfg.TickInterval))
	var beside sync.WaitGroup
	beside.Add(1)
	go func() {
		defer beside.Done()
		if err := c.runKoth(tickCtx, tick); err != nil && parent.Err() == nil {
			c.logger.Error("tick koth", zap.Int("tick", tick), zap.Error(err))
		}
	}()
	if c.outbreak != nil {
		beside.Add(1)
		go func() {
			defer beside.Done()
			c.outbreak(tickCtx, tick, started)
		}()
	}
	if err := c.dispatcher.dispatch(tickCtx, tick, started); err != nil && parent.Err() == nil {
		c.logger.Error("tick dispatch", zap.Int("tick", tick), zap.Error(err))
	}
	beside.Wait()
	cancelTick()
	if err := parent.Err(); err != nil {
		return err // shutting down: leave the tick running so a restart resumes it
	}

	ctx, cancel := context.WithTimeout(parent, finishTimeout)
	defer cancel()
	if err := c.recomputeStandings(ctx); err != nil {
		return fmt.Errorf("recompute standings: %w", err)
	}
	if native {
		if err := c.foldNativeScore(ctx); err != nil {
			return fmt.Errorf("fold native koth score: %w", err)
		}
	}
	if err := c.snapshotStandings(ctx, tick); err != nil {
		return fmt.Errorf("snapshot standings: %w", err)
	}
	if err := c.closeTick(ctx, tick); err != nil {
		return fmt.Errorf("close tick: %w", err)
	}
	c.emitStandings(ctx, tick)
	return nil
}

func (c *Controller) acquireTickLock(ctx context.Context) (func(), bool, error) {
	conn, err := c.db.Pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, gameTickLockID).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !acquired {
		conn.Release()
		return func() {}, false, nil
	}
	release := func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, gameTickLockID).Scan(&unlocked); err != nil || !unlocked {
			c.logger.Error("game engine failed to release tick lock", zap.Error(err))
			// Never return a session with a possibly-held advisory lock to the pool.
			raw := conn.Hijack()
			_ = raw.Close(context.Background())
			return
		}
		conn.Release()
	}
	return release, true, nil
}

func (c *Controller) lastTickState(ctx context.Context) (int, string, error) {
	var tick int
	var status string
	err := c.db.Pool.QueryRow(ctx,
		`SELECT tick_number, status FROM game_ticks ORDER BY tick_number DESC LIMIT 1`,
	).Scan(&tick, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", nil
	}
	return tick, status, err
}

func (c *Controller) nextTick(ctx context.Context) (int, error) {
	if tick, ok, err := c.runningTick(ctx); err != nil {
		return 0, err
	} else if ok {
		return tick, nil
	}
	tick, _, err := c.lastTickState(ctx)
	if err != nil {
		return 0, err
	}
	return tick + 1, nil
}

func (c *Controller) runningTick(ctx context.Context) (int, bool, error) {
	var tick int
	err := c.db.Pool.QueryRow(ctx,
		`SELECT tick_number FROM game_ticks WHERE status = 'running' ORDER BY tick_number LIMIT 1`,
	).Scan(&tick)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return tick, true, nil
}

func (c *Controller) openTick(ctx context.Context, tick int) (time.Time, bool, error) {
	// cadence gate: every api replica runs a controller, so without this a new tick
	// opens as fast as any replica's ticker fires (N replicas => N x speed). Only open
	// a new tick when none has STARTED within the interval; an existing tick (resume, or
	// a peer that just opened it) falls through to the status check below.
	var started time.Time
	err := c.db.Pool.QueryRow(ctx,
		`INSERT INTO game_ticks (tick_number, started_at, status)
		 SELECT $1, NOW(), 'running'
		 WHERE NOT EXISTS (
		   SELECT 1 FROM game_ticks WHERE started_at > NOW() - make_interval(secs => $2))
		 ON CONFLICT (tick_number) DO NOTHING
		 RETURNING started_at`, tick, c.cfg.TickInterval.Seconds()).Scan(&started)
	if err == nil {
		return started, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, err
	}

	var status string
	if err := c.db.Pool.QueryRow(ctx,
		`SELECT status, started_at FROM game_ticks WHERE tick_number = $1`, tick,
	).Scan(&status, &started); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, false, nil // gated: too soon for a new tick, skip this cycle
		}
		return time.Time{}, false, err
	}
	return started, status == "running", nil
}

func (c *Controller) closeTick(ctx context.Context, tick int) error {
	_, err := c.db.Pool.Exec(ctx,
		`UPDATE game_ticks SET ended_at = NOW(), status = 'closed' WHERE tick_number = $1`, tick)
	return err
}
