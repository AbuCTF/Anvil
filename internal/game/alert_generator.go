package game

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// alertBucket maps a raw exploit count to the bucketed display value.
// Buckets: 1, 3 (2-3), 7 (4-7), 8+ (8 or more).
func alertBucket(n int) int {
	switch {
	case n == 1:
		return 1
	case n <= 3:
		return 3
	case n <= 7:
		return 7
	default:
		return 8
	}
}

// AlertGenerator publishes E8 exploitation alerts to game_team_alerts at the
// end of each tick (one tick late: events from tick t are published in tick t+1).
// It also emits "holding" alerts when a team has held a service clean for 5+ ticks
// while at least one opponent is actively attacking them.
type AlertGenerator struct {
	db     *database.DB
	logger *zap.Logger
}

func NewAlertGenerator(db *database.DB, logger *zap.Logger) *AlertGenerator {
	return &AlertGenerator{db: db, logger: logger}
}

// PublishForTick generates alerts for the completed tick t. Called by the game
// controller after tick t closes. The controller owns the tick lifecycle; this
// function only reads and inserts, never changes tick state.
func (g *AlertGenerator) PublishForTick(ctx context.Context, completedTick int) {
	if err := g.publishExploitAlerts(ctx, completedTick); err != nil {
		g.logger.Error("alert generator: exploit alerts", zap.Int("tick", completedTick), zap.Error(err))
	}
	if err := g.publishHoldingAlerts(ctx, completedTick); err != nil {
		g.logger.Error("alert generator: holding alerts", zap.Int("tick", completedTick), zap.Error(err))
	}
}

type exploitCapture struct {
	victimID  uuid.UUID
	serviceID uuid.UUID
	count     int
}

// publishExploitAlerts inserts one game_team_alerts row per (victim, service) pair
// where count > 0 in the completed tick.
func (g *AlertGenerator) publishExploitAlerts(ctx context.Context, tick int) error {
	rows, err := g.db.Pool.Query(ctx,
		`SELECT victim_team_id, service_id, COUNT(*) AS captures
		 FROM game_captures
		 WHERE tick_number = $1
		 GROUP BY victim_team_id, service_id`, tick)
	if err != nil {
		return err
	}
	defer rows.Close()

	var captures []exploitCapture
	for rows.Next() {
		var c exploitCapture
		if err := rows.Scan(&c.victimID, &c.serviceID, &c.count); err != nil {
			return err
		}
		captures = append(captures, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range captures {
		bucket := alertBucket(c.count)
		detail, _ := json.Marshal(map[string]any{"count": c.count})
		_, err := g.db.Pool.Exec(ctx,
			`INSERT INTO game_team_alerts (team_id, tick_number, kind, service_id, bucket, detail)
			 VALUES ($1, $2, 'exploit', $3, $4, $5::jsonb)
			 ON CONFLICT (team_id, tick_number, kind, service_id) DO UPDATE
			   SET bucket = EXCLUDED.bucket, detail = EXCLUDED.detail`,
			c.victimID, tick, c.serviceID, bucket, string(detail))
		if err != nil {
			g.logger.Error("alert generator: insert exploit alert",
				zap.Int("tick", tick), zap.Error(err))
		}
	}
	return nil
}

// publishHoldingAlerts emits a "holding" alert for a (team, service) pair when:
// - the team has had 0 captures on that service for the last 5 ticks, AND
// - at least one opponent attacked them in the last 5 ticks (the "under attack" condition).
func (g *AlertGenerator) publishHoldingAlerts(ctx context.Context, tick int) error {
	if tick < 5 {
		return nil
	}
	const holdWindow = 5

	// find (team, service) pairs with 0 captures in the last 5 ticks but
	// ≥1 attacker active against them in the same window.
	rows, err := g.db.Pool.Query(ctx,
		`SELECT t.id, s.id
		 FROM game_teams t
		 CROSS JOIN game_services s
		 WHERE t.status = 'active' AND t.is_nop = FALSE
		   AND NOT EXISTS (
		       SELECT 1 FROM game_captures c
		       WHERE c.victim_team_id = t.id AND c.service_id = s.id
		         AND c.tick_number > $1::integer - $2::integer
		   )
		   AND EXISTS (
		       SELECT 1 FROM game_captures c2
		       WHERE c2.victim_team_id = t.id AND c2.service_id = s.id
		         AND c2.tick_number > $1::integer - $2::integer - 5
		   )`,
		tick, holdWindow)
	if err != nil {
		return err
	}
	defer rows.Close()

	type holdPair struct{ teamID, serviceID uuid.UUID }
	var pairs []holdPair
	for rows.Next() {
		var p holdPair
		if err := rows.Scan(&p.teamID, &p.serviceID); err != nil {
			return err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range pairs {
		_, err := g.db.Pool.Exec(ctx,
			`INSERT INTO game_team_alerts (team_id, tick_number, kind, service_id)
			 VALUES ($1, $2, 'holding', $3)
			 ON CONFLICT (team_id, tick_number, kind, service_id) DO NOTHING`,
			p.teamID, tick, p.serviceID)
		if err != nil {
			g.logger.Error("alert generator: insert holding alert",
				zap.Int("tick", tick), zap.Error(err))
		}
	}
	return nil
}

// PublishShieldUp emits a "shield_up" alert for a team on a service.
// Called by the scoring engine when the SHIELD UP award is issued.
func (g *AlertGenerator) PublishShieldUp(ctx context.Context, teamID, serviceID uuid.UUID, tick int) error {
	_, err := g.db.Pool.Exec(ctx,
		`INSERT INTO game_team_alerts (team_id, tick_number, kind, service_id)
		 VALUES ($1, $2, 'shield_up', $3)
		 ON CONFLICT (team_id, tick_number, kind, service_id) DO NOTHING`,
		teamID, tick, serviceID)
	return err
}

// RunAlertWorker is a lightweight goroutine that polls for new closed ticks and
// calls PublishForTick. Used when the game controller doesn't call PublishForTick
// directly. Exits when ctx is cancelled.
func RunAlertWorker(ctx context.Context, db *database.DB, logger *zap.Logger) {
	gen := NewAlertGenerator(db, logger)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	var lastProcessed int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		var latestClosed int
		err := db.Pool.QueryRow(ctx,
			`SELECT COALESCE(MAX(tick_number), 0) FROM game_ticks WHERE status = 'closed'`,
		).Scan(&latestClosed)
		if err != nil || latestClosed <= lastProcessed {
			continue
		}

		for t := lastProcessed + 1; t <= latestClosed; t++ {
			gen.PublishForTick(ctx, t)
		}
		lastProcessed = latestClosed
	}
}
