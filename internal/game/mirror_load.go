package game

import (
	"context"
	"fmt"
	"sort"

	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// recomputeMirror rebuilds the finals board from raw rows: a pure recompute, so an
// admin re-credit or a fix mid-game is just the next tick. current is the tick being
// closed; final settles every flag on the ticks that exist (game over).
func (c *Controller) recomputeMirror(ctx context.Context, current int, final bool) error {
	tx, err := c.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	in, err := c.loadMirror(ctx, tx, current, final)
	if err != nil {
		return err
	}
	res := scoreMirror(in)
	if err := writeMirror(ctx, tx, res); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// the per-flag count of distinct non-nop teams that stole it
const flagCaptors = `SELECT c.flag_id, COUNT(DISTINCT c.attacker_team_id) AS n
	FROM game_captures c JOIN game_teams a ON a.id = c.attacker_team_id AND NOT a.is_nop
	GROUP BY c.flag_id`

func (c *Controller) loadMirror(ctx context.Context, tx pgx.Tx, current int, final bool) (*mirrorInput, error) {
	sc := c.cfg.Scoring
	in := &mirrorInput{
		Current: current, Final: final,
		Teams:      map[uuid.UUID]bool{},
		Stores:     map[uuid.UUID]int{},
		SLA:        map[slaKey]slaTick{},
		RecShare:   map[slaKey]float64{},
		BotCrashed: map[slaKey]bool{},
		NopStolen:  map[storeTick]bool{},
		Attackers:  map[storeTick]map[uuid.UUID]bool{},
		Steal:      map[teamStore]float64{},
		Koth:       map[uuid.UUID]float64{},
		MassFault:  sc.MassFaultTeams,
		Bonus:      sc.AttackerBonus,
		WeightAD:   sc.WeightAD,
		WeightKoth: sc.WeightKoth,
	}

	scan := func(what, q string, args []any, each func(pgx.Rows) error) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("load %s: %w", what, err)
		}
		defer rows.Close()
		for rows.Next() {
			if err := each(rows); err != nil {
				return fmt.Errorf("scan %s: %w", what, err)
			}
		}
		return rows.Err()
	}
	settledArgs := []any{current, final}

	if err := scan("teams", `SELECT id, is_nop FROM game_teams WHERE status = 'active'`, nil, func(r pgx.Rows) error {
		var id uuid.UUID
		var nop bool
		if err := r.Scan(&id, &nop); err != nil {
			return err
		}
		in.Teams[id] = nop
		if !nop {
			in.N++
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if sc.NTeams > 0 {
		in.N = sc.NTeams
	}

	if err := scan("stores", `SELECT id, GREATEST(flag_stores, 1) FROM game_services`, nil, func(r pgx.Rows) error {
		var id uuid.UUID
		var n int
		if err := r.Scan(&id, &n); err != nil {
			return err
		}
		in.Stores[id] = n
		return nil
	}); err != nil {
		return nil, err
	}

	// settled flags; unread life ticks only matter for flags nobody stole
	if err := scan("flags", `
		SELECT f.id, f.team_id, f.service_id, f.store_index, f.tick_number, f.valid_until_tick,
		       COALESCE(cap.n, 0),
		       EXISTS (SELECT 1 FROM game_bot_reads b WHERE b.flag_id = f.id AND b.outcome = 'stolen'),
		       CASE WHEN COALESCE(cap.n, 0) = 0 THEN ARRAY(
		         SELECT r FROM generate_series(f.tick_number, LEAST(f.valid_until_tick, $1)) AS r
		         WHERE NOT EXISTS (SELECT 1 FROM game_flag_checks fc
		                           WHERE fc.flag_id = f.id AND fc.tick_number = r AND fc.readable))
		       ELSE '{}'::int[] END
		FROM game_flags f
		LEFT JOIN (`+flagCaptors+`) cap ON cap.flag_id = f.id
		WHERE f.planted_at IS NOT NULL AND ($2 OR f.valid_until_tick < $1)`, settledArgs, func(r pgx.Rows) error {
		var f mirrorFlag
		var unread []int32
		if err := r.Scan(&f.ID, &f.Team, &f.Service, &f.Store, &f.Tick, &f.ValidUntil, &f.Captors, &f.BotStolen, &unread); err != nil {
			return err
		}
		for _, t := range unread {
			f.Unread = append(f.Unread, int(t))
		}
		in.Flags = append(in.Flags, f)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := scan("sla", `
		SELECT team_id, service_id, tick_number, status, COALESCE(step, '')
		FROM game_sla_checks WHERE tick_number <= $1`, []any{current}, func(r pgx.Rows) error {
		var k slaKey
		var st, step string
		if err := r.Scan(&k.team, &k.service, &k.tick, &st, &step); err != nil {
			return err
		}
		in.SLA[k] = slaTick{Status: models.SLAStatus(st), Step: step}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := scan("recovering share", `
		SELECT f.team_id, f.service_id, fc.tick_number, AVG(CASE WHEN fc.readable THEN 1.0 ELSE 0.0 END)
		FROM game_flag_checks fc
		JOIN game_flags f ON f.id = fc.flag_id
		JOIN game_sla_checks s ON s.team_id = f.team_id AND s.service_id = f.service_id
		     AND s.tick_number = fc.tick_number AND s.status = 'RECOVERING'
		WHERE fc.tick_number <= $1
		GROUP BY 1, 2, 3`, []any{current}, func(r pgx.Rows) error {
		var k slaKey
		var share float64
		if err := r.Scan(&k.team, &k.service, &k.tick, &share); err != nil {
			return err
		}
		in.RecShare[k] = share
		return nil
	}); err != nil {
		return nil, err
	}

	if err := loadCredits(ctx, tx, in); err != nil {
		return nil, err
	}

	if err := scan("bot crashes", `
		SELECT f.team_id, f.service_id, b.tick_number
		FROM game_bot_reads b JOIN game_flags f ON f.id = b.flag_id
		WHERE b.outcome = 'crashed'
		  AND EXISTS (
		    SELECT 1 FROM game_bot_reads nb
		    JOIN game_flags nf ON nf.id = nb.flag_id
		    JOIN game_teams nt ON nt.id = nf.team_id AND nt.is_nop
		    WHERE nf.service_id = f.service_id AND nf.store_index = f.store_index
		      AND nb.tick_number = b.tick_number AND nb.outcome = 'stolen'
		  )`, nil, func(r pgx.Rows) error {
		var k slaKey
		if err := r.Scan(&k.team, &k.service, &k.tick); err != nil {
			return err
		}
		in.BotCrashed[k] = true
		return nil
	}); err != nil {
		return nil, err
	}

	// the wave's verdicts on a store-tick stand only if it also read the nop's flag
	if err := scan("nop canary", `
		SELECT f.service_id, f.store_index, f.tick_number
		FROM game_bot_reads b
		JOIN game_flags f ON f.id = b.flag_id
		JOIN game_teams t ON t.id = f.team_id AND t.is_nop
		WHERE b.outcome = 'stolen'`, nil, func(r pgx.Rows) error {
		var st storeTick
		if err := r.Scan(&st.service, &st.store, &st.tick); err != nil {
			return err
		}
		in.NopStolen[st] = true
		return nil
	}); err != nil {
		return nil, err
	}

	if err := scan("attackers", `
		SELECT DISTINCT f.service_id, f.store_index, f.tick_number, c.attacker_team_id
		FROM game_captures c
		JOIN game_flags f ON f.id = c.flag_id
		JOIN game_teams a ON a.id = c.attacker_team_id AND NOT a.is_nop
		WHERE $2 OR f.valid_until_tick < $1`, settledArgs, func(r pgx.Rows) error {
		var st storeTick
		var team uuid.UUID
		if err := r.Scan(&st.service, &st.store, &st.tick, &team); err != nil {
			return err
		}
		if in.Attackers[st] == nil {
			in.Attackers[st] = map[uuid.UUID]bool{}
		}
		in.Attackers[st][team] = true
		return nil
	}); err != nil {
		return nil, err
	}

	if err := scan("steal", `
		SELECT c.attacker_team_id, f.service_id, f.store_index, SUM(1 + 1.0 / cap.n)
		FROM game_captures c
		JOIN game_flags f ON f.id = c.flag_id
		JOIN game_teams a ON a.id = c.attacker_team_id AND NOT a.is_nop
		JOIN game_teams v ON v.id = f.team_id AND NOT v.is_nop
		JOIN (`+flagCaptors+`) cap ON cap.flag_id = f.id
		WHERE $2 OR f.valid_until_tick < $1
		GROUP BY 1, 2, 3`, settledArgs, func(r pgx.Rows) error {
		var k teamStore
		var v float64
		if err := r.Scan(&k.team, &k.service, &k.store, &v); err != nil {
			return err
		}
		in.Steal[k] = v
		return nil
	}); err != nil {
		return nil, err
	}

	acc := map[uuid.UUID]*standing{}
	for id, nop := range in.Teams {
		if !nop {
			acc[id] = &standing{}
		}
	}
	if err := c.accumulateKoth(ctx, tx, acc); err != nil {
		return nil, fmt.Errorf("accumulate koth: %w", err)
	}
	for id, s := range acc {
		in.Koth[id] = s.koth
	}
	return in, nil
}

func loadCredits(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, in *mirrorInput) error {
	rows, err := q.Query(ctx, `SELECT service_id, team_id, tick_from, tick_to, reason FROM game_sla_credits`)
	if err != nil {
		return fmt.Errorf("load credits: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cr slaCredit
		if err := rows.Scan(&cr.Service, &cr.Team, &cr.From, &cr.To, &cr.Reason); err != nil {
			return fmt.Errorf("scan credits: %w", err)
		}
		in.Credits = append(in.Credits, cr)
	}
	return rows.Err()
}

func writeMirror(ctx context.Context, tx pgx.Tx, res mirrorResult) error {
	type row struct {
		team uuid.UUID
		s    *mirrorScore
	}
	rows := make([]row, 0, len(res.Teams))
	for id, s := range res.Teams {
		rows = append(rows, row{id, s})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].s, rows[j].s
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		if a.AD != b.AD {
			return a.AD > b.AD
		}
		return rows[i].team.String() < rows[j].team.String()
	})

	if _, err := tx.Exec(ctx,
		`DELETE FROM game_standings s
		 WHERE NOT EXISTS (
		   SELECT 1 FROM game_teams t
		   WHERE t.id = s.team_id AND t.status = 'active' AND t.is_nop = FALSE)`); err != nil {
		return err
	}
	for i, r := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO game_standings (team_id, attack, defense, sla, koth, ad, total, rank, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
			 ON CONFLICT (team_id) DO UPDATE SET
			   attack = $2, defense = $3, sla = $4, koth = $5, ad = $6, total = $7, rank = $8, updated_at = NOW()`,
			r.team, r.s.Steal, r.s.Hold, r.s.Up, r.s.Koth, r.s.AD, r.s.Total, i+1); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM game_score_breakdown`); err != nil {
		return err
	}
	for k, b := range res.Breakdown {
		if _, err := tx.Exec(ctx,
			`INSERT INTO game_score_breakdown (team_id, service_id, store_index, steal, hold, up, flags_held, flags_settled)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			k.team, k.service, k.store, b.Steal, b.Hold, b.Up, b.FlagsHeld, b.FlagsDone); err != nil {
			return err
		}
	}
	return nil
}
