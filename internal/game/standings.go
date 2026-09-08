package game

import (
	"context"
	"sort"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
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
func (c *Controller) recomputeStandings(ctx context.Context) {
	teams, err := c.scoredTeams(ctx)
	if err != nil {
		c.logger.Error("standings: load teams", zap.Error(err))
		return
	}
	if len(teams) == 0 {
		return
	}

	acc := make(map[uuid.UUID]*standing, len(teams))
	for _, id := range teams {
		acc[id] = &standing{}
	}

	captors := c.accumulateDefense(ctx, acc)
	c.accumulateAttack(ctx, acc, captors)
	c.accumulateSLA(ctx, acc, len(teams))
	c.accumulateKoth(ctx, acc)

	c.writeStandings(ctx, acc)
}

// accumulateDefense subtracts a sublinear penalty for each captured flag and
// returns the captor count per flag (reused for attack).
func (c *Controller) accumulateDefense(ctx context.Context, acc map[uuid.UUID]*standing) map[uuid.UUID]int {
	captors := make(map[uuid.UUID]int)
	rows, err := c.db.Pool.Query(ctx,
		`SELECT c.flag_id, f.team_id, COUNT(*) FROM game_captures c
		 JOIN game_flags f ON f.id = c.flag_id
		 GROUP BY c.flag_id, f.team_id`)
	if err != nil {
		c.logger.Warn("standings: defense query", zap.Error(err))
		return captors
	}
	defer rows.Close()

	for rows.Next() {
		var flag, owner uuid.UUID
		var n int
		if rows.Scan(&flag, &owner, &n) != nil {
			continue
		}
		captors[flag] = n
		if s := acc[owner]; s != nil {
			s.defense -= defensePenalty(c.cfg.Scoring.DefenseFactor, n)
		}
	}
	return captors
}

func (c *Controller) accumulateAttack(ctx context.Context, acc map[uuid.UUID]*standing, captors map[uuid.UUID]int) {
	rows, err := c.db.Pool.Query(ctx, `SELECT attacker_team_id, flag_id FROM game_captures`)
	if err != nil {
		c.logger.Warn("standings: attack query", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var team, flag uuid.UUID
		if rows.Scan(&team, &flag) != nil {
			continue
		}
		if s := acc[team]; s != nil {
			s.attack += attackContribution(c.cfg.Scoring.AttackBase, captors[flag])
		}
	}
}

func (c *Controller) accumulateSLA(ctx context.Context, acc map[uuid.UUID]*standing, numTeams int) {
	rows, err := c.db.Pool.Query(ctx,
		`SELECT team_id, status, COUNT(*) FROM game_sla_checks GROUP BY team_id, status`)
	if err != nil {
		c.logger.Warn("standings: sla query", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var team uuid.UUID
		var status string
		var n int
		if rows.Scan(&team, &status, &n) != nil {
			continue
		}
		if s := acc[team]; s != nil {
			s.sla += slaTickPoints(c.cfg.Scoring.SLAPoints, numTeams, models.SLAStatus(status)) * float64(n)
		}
	}
}

func (c *Controller) accumulateKoth(ctx context.Context, acc map[uuid.UUID]*standing) {
	held, err := c.db.Pool.Query(ctx,
		`SELECT controller_team_id, COUNT(*) FROM game_koth_control
		 WHERE controller_team_id IS NOT NULL GROUP BY controller_team_id`)
	if err == nil {
		for held.Next() {
			var team uuid.UUID
			var n int
			if held.Scan(&team, &n) == nil {
				if s := acc[team]; s != nil {
					s.koth += c.cfg.Scoring.KothHold * float64(n)
				}
			}
		}
		held.Close()
	}

	bonus, err := c.db.Pool.Query(ctx,
		`SELECT team_id, SUM(points) FROM game_score_events WHERE stream = 'KOTH' GROUP BY team_id`)
	if err == nil {
		for bonus.Next() {
			var team uuid.UUID
			var pts float64
			if bonus.Scan(&team, &pts) == nil {
				if s := acc[team]; s != nil {
					s.koth += pts
				}
			}
		}
		bonus.Close()
	}
}

func (c *Controller) writeStandings(ctx context.Context, acc map[uuid.UUID]*standing) {
	type row struct {
		team  uuid.UUID
		s     *standing
		total float64
	}
	rows := make([]row, 0, len(acc))
	for id, s := range acc {
		rows = append(rows, row{id, s, s.attack + s.defense + s.sla + s.koth})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].total > rows[j].total })

	for i, r := range rows {
		_, err := c.db.Pool.Exec(ctx,
			`INSERT INTO game_standings (team_id, attack, defense, sla, koth, total, rank, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			 ON CONFLICT (team_id) DO UPDATE SET
			   attack = $2, defense = $3, sla = $4, koth = $5, total = $6, rank = $7, updated_at = NOW()`,
			r.team, r.s.attack, r.s.defense, r.s.sla, r.s.koth, r.total, i+1)
		if err != nil {
			c.logger.Warn("standings: upsert", zap.Error(err))
		}
	}
}

func (c *Controller) scoredTeams(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := c.db.Pool.Query(ctx,
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
