package game

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
}

func NewController(cfg config.GameConfig, db *database.DB, logger *zap.Logger) *Controller {
	return &Controller{
		cfg:    cfg,
		db:     db,
		logger: logger,
		dispatcher: &Dispatcher{
			db:             db,
			logger:         logger,
			flagPrefix:     cfg.FlagPrefix,
			flagValidTicks: cfg.FlagValidTicks,
		},
		emitClient: &http.Client{Timeout: 10 * time.Second},
	}
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
	} else if ok {
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

func (c *Controller) runTick(parent context.Context, tick int) error {
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

	runnable, err := c.openTick(ctx, tick)
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

	if err := c.dispatcher.dispatch(ctx, tick); err != nil {
		return fmt.Errorf("dispatch: %w", err)
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

func (c *Controller) openTick(ctx context.Context, tick int) (bool, error) {
	tag, err := c.db.Pool.Exec(ctx,
		`INSERT INTO game_ticks (tick_number, started_at, status)
		 VALUES ($1, NOW(), 'running')
		 ON CONFLICT (tick_number) DO NOTHING`, tick)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}

	var status string
	if err := c.db.Pool.QueryRow(ctx,
		`SELECT status FROM game_ticks WHERE tick_number = $1`, tick,
	).Scan(&status); err != nil {
		return false, err
	}
	return status == "running", nil
}

func (c *Controller) closeTick(ctx context.Context, tick int) error {
	_, err := c.db.Pool.Exec(ctx,
		`UPDATE game_ticks SET ended_at = NOW(), status = 'closed' WHERE tick_number = $1`, tick)
	return err
}
