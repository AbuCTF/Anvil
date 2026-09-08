package game

import (
	"context"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"go.uber.org/zap"
)

// Controller drives the game clock. It opens and closes ticks on a fixed
// interval; checker dispatch, KotH control, and scoring hang off each tick.
type Controller struct {
	cfg    config.GameConfig
	db     *database.DB
	logger *zap.Logger
}

func NewController(cfg config.GameConfig, db *database.DB, logger *zap.Logger) *Controller {
	return &Controller{cfg: cfg, db: db, logger: logger}
}

// Run ticks until ctx is cancelled. It returns immediately unless the engine is enabled.
func (c *Controller) Run(ctx context.Context) {
	if !c.cfg.Enabled {
		return
	}

	tick, err := c.lastTick(ctx)
	if err != nil {
		c.logger.Error("game engine failed to read tick state", zap.Error(err))
		return
	}

	c.logger.Info("game engine started",
		zap.Duration("tick_interval", c.cfg.TickInterval),
		zap.Int("resume_from_tick", tick))

	ticker := time.NewTicker(c.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("game engine stopped")
			return
		case <-ticker.C:
			tick++
			c.runTick(ctx, tick)
		}
	}
}

func (c *Controller) runTick(parent context.Context, tick int) {
	ctx, cancel := context.WithTimeout(parent, c.cfg.TickInterval)
	defer cancel()

	if err := c.openTick(ctx, tick); err != nil {
		c.logger.Error("open tick failed", zap.Int("tick", tick), zap.Error(err))
		return
	}

	// Checker dispatch (flags + SLA), KotH control, and scoring run here.

	if err := c.closeTick(ctx, tick); err != nil {
		c.logger.Error("close tick failed", zap.Int("tick", tick), zap.Error(err))
	}
}

func (c *Controller) lastTick(ctx context.Context) (int, error) {
	var last int
	err := c.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks`).Scan(&last)
	return last, err
}

func (c *Controller) openTick(ctx context.Context, tick int) error {
	_, err := c.db.Pool.Exec(ctx,
		`INSERT INTO game_ticks (tick_number, started_at, status)
		 VALUES ($1, NOW(), 'running')
		 ON CONFLICT (tick_number) DO NOTHING`, tick)
	return err
}

func (c *Controller) closeTick(ctx context.Context, tick int) error {
	_, err := c.db.Pool.Exec(ctx,
		`UPDATE game_ticks SET ended_at = NOW(), status = 'closed' WHERE tick_number = $1`, tick)
	return err
}
