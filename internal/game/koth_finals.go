package game

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// finals king of the hill: a round closes on the clock (15 min by day, 30 overnight),
// every hill ranks its own round by ticks held (12/7/4/2/1, ties split), and ending a
// long reign pays a crown bounty. the quals path (koth.go) keeps tick-derived rounds.

// roundLength is the length of a round that starts at t.
func roundLength(k config.KothConfig, t time.Time) time.Duration {
	length := k.RoundInterval
	if _, end, night := nightBounds(k, t); night && k.NightRoundInterval > 0 {
		length = k.NightRoundInterval
		if untilEnd := end.Sub(t); untilEnd > 0 && untilEnd < length {
			return untilEnd
		}
		return length
	}
	if start, ok := nextNightStart(k, t); ok {
		if untilStart := start.Sub(t); untilStart > 0 && untilStart < length {
			return untilStart
		}
	}
	return length
}

func atNight(k config.KothConfig, t time.Time) bool {
	_, _, ok := nightBounds(k, t)
	return ok
}

// ServiceReleaseWindow keeps public visibility on the dispatcher's Night Watch boundary.
func ServiceReleaseWindow(k config.KothConfig, t time.Time) (time.Time, bool) {
	start, _, night := nightBounds(k, t)
	return start, night
}

func nightBounds(k config.KothConfig, t time.Time) (time.Time, time.Time, bool) {
	loc, err := time.LoadLocation(k.Timezone)
	if err != nil {
		loc = time.UTC
	}
	start, err1 := time.Parse("15:04", k.NightStart)
	end, err2 := time.Parse("15:04", k.NightEnd)
	if err1 != nil || err2 != nil || (start.Hour() == end.Hour() && start.Minute() == end.Minute()) {
		return time.Time{}, time.Time{}, false
	}
	local := t.In(loc)
	s, e := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
	startToday := time.Date(local.Year(), local.Month(), local.Day(), start.Hour(), start.Minute(), 0, 0, loc)
	endToday := time.Date(local.Year(), local.Month(), local.Day(), end.Hour(), end.Minute(), 0, 0, loc)
	if s <= e {
		if !local.Before(startToday) && local.Before(endToday) {
			return startToday, endToday, true
		}
		return time.Time{}, time.Time{}, false
	}
	if !local.Before(startToday) {
		return startToday, endToday.AddDate(0, 0, 1), true
	}
	if local.Before(endToday) {
		return startToday.AddDate(0, 0, -1), endToday, true
	}
	return time.Time{}, time.Time{}, false
}

func nextNightStart(k config.KothConfig, t time.Time) (time.Time, bool) {
	loc, err := time.LoadLocation(k.Timezone)
	if err != nil {
		loc = time.UTC
	}
	start, err := time.Parse("15:04", k.NightStart)
	if err != nil || k.NightStart == k.NightEnd {
		return time.Time{}, false
	}
	local := t.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), start.Hour(), start.Minute(), 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next, true
}

// rankPointsSplit ranks by ticks held; teams that tie share the points of the
// ranks they occupy. teams with no hold score nothing.
func rankPointsSplit(table []int, holds []teamHold) map[uuid.UUID]float64 {
	sorted := make([]teamHold, 0, len(holds))
	for _, h := range holds {
		if h.Held > 0 {
			sorted = append(sorted, h)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Held != sorted[j].Held {
			return sorted[i].Held > sorted[j].Held
		}
		return sorted[i].Team.String() < sorted[j].Team.String()
	})
	out := map[uuid.UUID]float64{}
	for i := 0; i < len(sorted); {
		j := i
		for j < len(sorted) && sorted[j].Held == sorted[i].Held {
			j++
		}
		var pool float64
		for r := i; r < j && r < len(table); r++ {
			pool += float64(table[r])
		}
		for r := i; r < j; r++ {
			out[sorted[r].Team] = pool / float64(j-i)
		}
		i = j
	}
	return out
}

// crownBounty pays the team that ends a reign of minStreak rounds or more.
func crownBounty(k config.KothConfig, prevWinner *uuid.UUID, prevStreak int, winner *uuid.UUID) float64 {
	if k.BountyPerRound <= 0 || prevWinner == nil || winner == nil || *prevWinner == *winner ||
		prevStreak < k.BountyMinStreak {
		return 0
	}
	b := float64(prevStreak) * k.BountyPerRound
	if k.BountyCap > 0 {
		b = math.Min(b, k.BountyCap)
	}
	return b
}

// runKothFinals rolls the round on the clock, then polls the hills.
func (c *Controller) runKothFinals(ctx context.Context, tick int, started time.Time) error {
	if err := c.applyHillWindows(ctx, tick, started); err != nil {
		return err
	}
	hills, err := c.enabledHills(ctx)
	if err != nil {
		return fmt.Errorf("load hills: %w", err)
	}
	round, planned, err := c.runningRound(ctx)
	if err != nil {
		return err
	}
	if round == 0 || !started.Before(planned) {
		if round > 0 {
			if err := c.closeRoundFinals(ctx, round); err != nil {
				return fmt.Errorf("close round %d: %w", round, err)
			}
			if c.cfg.Koth.ResetEnabled {
				c.resetHills(ctx, hills, round+1)
			}
		}
		if round, err = c.openRoundFinals(ctx, started); err != nil {
			return fmt.Errorf("open round: %w", err)
		}
	}
	if len(hills) == 0 {
		return nil
	}
	return c.pollHillsFinals(ctx, tick, round, hills)
}

func (c *Controller) runningRound(ctx context.Context) (int, time.Time, error) {
	var round int
	var planned *time.Time
	err := c.db.Pool.QueryRow(ctx,
		`SELECT round_number, planned_end FROM game_koth_rounds
		 WHERE status = 'running' ORDER BY round_number DESC LIMIT 1`).Scan(&round, &planned)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	if planned == nil {
		// a round opened by the quals path: close it now
		return round, time.Time{}, nil
	}
	return round, *planned, nil
}

func (c *Controller) openRoundFinals(ctx context.Context, started time.Time) (int, error) {
	var round int
	err := c.db.Pool.QueryRow(ctx,
		`INSERT INTO game_koth_rounds (round_number, started_at, planned_end, status)
		 SELECT COALESCE(MAX(round_number), 0) + 1, $1, $2, 'running' FROM game_koth_rounds
		 RETURNING round_number`, started, started.Add(roundLength(c.cfg.Koth, started))).Scan(&round)
	return round, err
}

// pollHillsFinals records each hill's holder and emits a crown event on a change.
func (c *Controller) pollHillsFinals(ctx context.Context, tick, round int, hills []hill) error {
	before := map[uuid.UUID]*uuid.UUID{}
	rows, err := c.db.Pool.Query(ctx,
		`SELECT hill_id, controller_team_id FROM game_koth_control WHERE tick_number = $1`, tick-1)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h uuid.UUID
		var team *uuid.UUID
		if err := rows.Scan(&h, &team); err != nil {
			rows.Close()
			return err
		}
		before[h] = team
	}
	rows.Close()

	if err := c.pollHills(ctx, tick, round, hills); err != nil {
		return err
	}
	for _, h := range hills {
		var now *uuid.UUID
		if err := c.db.Pool.QueryRow(ctx,
			`SELECT controller_team_id FROM game_koth_control WHERE tick_number = $1 AND hill_id = $2`,
			tick, h.id).Scan(&now); err != nil {
			continue
		}
		prev := before[h.id]
		if now != nil && (prev == nil || *prev != *now) {
			if _, err := c.db.Pool.Exec(ctx,
				`INSERT INTO game_events (kind, tick_number, team_id, hill_id, payload)
				 VALUES ('crown', $1, $2, $3, jsonb_build_object('round', $4::int, 'from', $5::text))`,
				tick, *now, h.id, round, uuidText(prev)); err != nil {
				c.logger.Warn("crown event", zap.Error(err))
			}
		}
	}
	return nil
}

func uuidText(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// closeRoundFinals ranks each hill's round on its own and pays any crown bounty.
// idempotent: only the first close of a running round scores.
func (c *Controller) closeRoundFinals(ctx context.Context, round int) error {
	tx, err := c.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE game_koth_rounds SET status = 'closed', ends_at = NOW()
		 WHERE round_number = $1 AND status = 'running'`, round)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}

	holds := map[uuid.UUID][]teamHold{}
	rows, err := tx.Query(ctx,
		`SELECT hill_id, controller_team_id, COUNT(*) FROM game_koth_control
		 WHERE round_number = $1 AND controller_team_id IS NOT NULL
		 GROUP BY hill_id, controller_team_id`, round)
	if err != nil {
		return err
	}
	for rows.Next() {
		var hillID uuid.UUID
		var th teamHold
		if err := rows.Scan(&hillID, &th.Team, &th.Held); err != nil {
			rows.Close()
			return err
		}
		holds[hillID] = append(holds[hillID], th)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for hillID, hs := range holds {
		pts := rankPointsSplit(c.cfg.Scoring.KothRank, hs)
		winner := soleLeader(hs)
		prevWinner, streak, err := reign(ctx, tx, hillID, round)
		if err != nil {
			return err
		}
		bounty := crownBounty(c.cfg.Koth, prevWinner, streak, winner)

		ranked := append([]teamHold(nil), hs...)
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Held > ranked[j].Held })
		rank := 0
		for i, th := range ranked {
			b := 0.0
			if winner != nil && th.Team == *winner {
				b = bounty
			}
			if i == 0 || ranked[i-1].Held != th.Held {
				rank = i + 1 // ties share the better rank
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO game_koth_results (round_number, hill_id, team_id, held_ticks, rank, points, bounty)
				 VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
				round, hillID, th.Team, th.Held, rank, pts[th.Team], b); err != nil {
				return err
			}
			if total := pts[th.Team] + b; total > 0 {
				if _, err := tx.Exec(ctx,
					`INSERT INTO game_score_events (team_id, round_number, stream, points, source)
					 VALUES ($1, $2, 'KOTH', $3, $4)`,
					th.Team, round, total, fmt.Sprintf("koth:round:%d:hill:%s", round, hillID)); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit(ctx)
}

// soleLeader is the round's single top holder; a tie at the top crowns nobody.
func soleLeader(hs []teamHold) *uuid.UUID {
	var best *teamHold
	tie := false
	for i := range hs {
		switch {
		case best == nil || hs[i].Held > best.Held:
			best, tie = &hs[i], false
		case hs[i].Held == best.Held:
			tie = true
		}
	}
	if best == nil || tie || best.Held == 0 {
		return nil
	}
	id := best.Team
	return &id
}

// reign returns the previous round's sole winner on this hill and how many rounds
// in a row it had won by then.
func reign(ctx context.Context, tx pgx.Tx, hillID uuid.UUID, round int) (*uuid.UUID, int, error) {
	// a round's winner is its sole rank-1 team on this hill; a shared top crowns nobody
	rows, err := tx.Query(ctx,
		`SELECT r.round_number,
		        (SELECT CASE WHEN COUNT(*) = 1 THEN MIN(k.team_id::text) END
		         FROM game_koth_results k
		         WHERE k.round_number = r.round_number AND k.hill_id = $1 AND k.rank = 1)
		 FROM game_koth_rounds r
		 WHERE r.round_number < $2 ORDER BY r.round_number DESC LIMIT 64`, hillID, round)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var who *uuid.UUID
	streak := 0
	for rows.Next() {
		var n int
		var team *string
		if err := rows.Scan(&n, &team); err != nil {
			return nil, 0, err
		}
		if team == nil {
			break
		}
		id, err := uuid.Parse(*team)
		if err != nil || (who != nil && id != *who) {
			break
		}
		who = &id
		streak++
	}
	return who, streak, rows.Err()
}
