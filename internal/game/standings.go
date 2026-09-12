package game

import (
	"context"
	"fmt"
	"sort"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type standing struct {
	attack  float64
	defense float64
	sla     float64
	koth    float64
}

// recomputeStandings rebuilds the cached board from the raw tables: attack and
// defense from captures, SLA from checks, KotH from hill control plus round
// rank bonuses. Called each tick after dispatch.
func (c *Controller) recomputeStandings(ctx context.Context) error {
	tx, err := c.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	teams, err := c.scoredTeams(ctx, tx)
	if err != nil {
		return fmt.Errorf("load teams: %w", err)
	}

	acc := make(map[uuid.UUID]*standing, len(teams))
	for _, id := range teams {
		acc[id] = &standing{}
	}

	if err := c.accumulateCaptures(ctx, tx, acc); err != nil {
		return fmt.Errorf("accumulate captures: %w", err)
	}
	if err := c.accumulateSLA(ctx, tx, acc, len(teams)); err != nil {
		return fmt.Errorf("accumulate SLA: %w", err)
	}
	if err := c.accumulateKoth(ctx, tx, acc); err != nil {
		return fmt.Errorf("accumulate KotH: %w", err)
	}

	if err := c.writeStandings(ctx, tx, acc); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// snapshotStandings records the current board as a point in time, for the
// score-over-time chart, sparklines, and rank deltas.
func (c *Controller) snapshotStandings(ctx context.Context, tick int) error {
	_, err := c.db.Pool.Exec(ctx,
		`INSERT INTO game_score_snapshots (tick_number, team_id, total, rank)
		 SELECT $1, team_id, total, rank FROM game_standings
		 ON CONFLICT (tick_number, team_id) DO UPDATE SET total = EXCLUDED.total, rank = EXCLUDED.rank`,
		tick)
	return err
}

// accumulateCaptures computes both sides of every capture from one query. The
// window count prevents a capture arriving between separate defense and attack
// queries from producing a zero-value attack or mismatched penalty.
func (c *Controller) accumulateCaptures(ctx context.Context, tx pgx.Tx, acc map[uuid.UUID]*standing) error {
	rows, err := tx.Query(ctx,
		`SELECT c.attacker_team_id, f.team_id, c.flag_id,
		        COUNT(*) OVER (PARTITION BY c.flag_id)
		 FROM game_captures c
		 JOIN game_flags f ON f.id = c.flag_id
		 ORDER BY c.flag_id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	penalized := make(map[uuid.UUID]bool)
	for rows.Next() {
		var attacker, owner, flag uuid.UUID
		var captors int
		if err := rows.Scan(&attacker, &owner, &flag, &captors); err != nil {
			return err
		}
		if s := acc[attacker]; s != nil {
			s.attack += attackContribution(c.cfg.Scoring.AttackBase, captors)
		}
		if !penalized[flag] {
			if s := acc[owner]; s != nil {
				s.defense -= defensePenalty(c.cfg.Scoring.DefenseFactor, captors)
			}
			penalized[flag] = true
		}
	}
	return rows.Err()
}

func (c *Controller) accumulateSLA(ctx context.Context, tx pgx.Tx, acc map[uuid.UUID]*standing, numTeams int) error {
	rows, err := tx.Query(ctx,
		`SELECT team_id, status, COUNT(*) FROM game_sla_checks GROUP BY team_id, status`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var team uuid.UUID
		var status string
		var n int
		if err := rows.Scan(&team, &status, &n); err != nil {
			return err
		}
		if s := acc[team]; s != nil {
			s.sla += slaTickPoints(c.cfg.Scoring.SLAPoints, numTeams, models.SLAStatus(status)) * float64(n)
		}
	}
	return rows.Err()
}

func (c *Controller) accumulateKoth(ctx context.Context, tx pgx.Tx, acc map[uuid.UUID]*standing) error {
	held, err := tx.Query(ctx,
		`SELECT controller_team_id, COUNT(*) FROM game_koth_control
		 WHERE controller_team_id IS NOT NULL GROUP BY controller_team_id`)
	if err != nil {
		return err
	}
	for held.Next() {
		var team uuid.UUID
		var n int
		if err := held.Scan(&team, &n); err != nil {
			held.Close()
			return err
		}
		if s := acc[team]; s != nil {
			s.koth += c.cfg.Scoring.KothHold * float64(n)
		}
	}
	if err := held.Err(); err != nil {
		held.Close()
		return err
	}
	held.Close()

	bonus, err := tx.Query(ctx,
		`SELECT team_id, SUM(points) FROM game_score_events WHERE stream = 'KOTH' GROUP BY team_id`)
	if err != nil {
		return err
	}
	for bonus.Next() {
		var team uuid.UUID
		var pts float64
		if err := bonus.Scan(&team, &pts); err != nil {
			bonus.Close()
			return err
		}
		if s := acc[team]; s != nil {
			s.koth += pts
		}
	}
	if err := bonus.Err(); err != nil {
		bonus.Close()
		return err
	}
	bonus.Close()
	return nil
}

func (c *Controller) writeStandings(ctx context.Context, tx pgx.Tx, acc map[uuid.UUID]*standing) error {
	type row struct {
		team  uuid.UUID
		s     *standing
		total float64
	}
	rows := make([]row, 0, len(acc))
	for id, s := range acc {
		rows = append(rows, row{id, s, s.attack + s.defense + s.sla + s.koth})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].total == rows[j].total {
			return rows[i].team.String() < rows[j].team.String()
		}
		return rows[i].total > rows[j].total
	})

	if _, err := tx.Exec(ctx,
		`DELETE FROM game_standings s
		 WHERE NOT EXISTS (
		   SELECT 1 FROM game_teams t
		   WHERE t.id = s.team_id AND t.status = 'active' AND t.is_nop = FALSE
		 )`); err != nil {
		return err
	}

	for i, r := range rows {
		_, err := tx.Exec(ctx,
			`INSERT INTO game_standings (team_id, attack, defense, sla, koth, total, rank, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			 ON CONFLICT (team_id) DO UPDATE SET
			   attack = $2, defense = $3, sla = $4, koth = $5, total = $6, rank = $7, updated_at = NOW()`,
			r.team, r.s.attack, r.s.defense, r.s.sla, r.s.koth, r.total, i+1)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) scoredTeams(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT id FROM game_teams WHERE status = 'active' AND is_nop = FALSE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
