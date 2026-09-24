package game

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// kothControlReply is read from a hill checker's stdout for the "control" action.
type kothControlReply struct {
	Controller string `json:"controller"`
	Message    string `json:"message"`
}

// hillProbe reports which team-token currently holds a hill and resets it clean.
// Two implementations: hillChecker (execs a local checker binary — legacy AD-style
// hills) and httpProbe (polls the target's HTTP /koth endpoints — challenge-backed
// arenas like GridWatch, the k8s-native path with no binary on the api pod).
type hillProbe interface {
	controller(ctx context.Context, t Target) string
	reset(ctx context.Context, t Target) error
}

// hillChecker execs a hill's checker executable. It handles "control" (which
// team marker currently holds the hill) and "reset" (restore the hill clean).
type hillChecker struct {
	command string
}

func (h hillChecker) run(ctx context.Context, action string, t Target) ([]byte, error) {
	task, _ := json.Marshal(checkerTask{Action: action, Host: t.Host, Port: t.Port})
	return runCheckerCommand(ctx, h.command, task)
}

func (h hillChecker) controller(ctx context.Context, t Target) string {
	out, err := h.run(ctx, "control", t)
	if err != nil {
		return ""
	}
	var reply kothControlReply
	if json.Unmarshal(bytes.TrimSpace(out), &reply) != nil {
		return ""
	}
	return reply.Controller
}

func (h hillChecker) reset(ctx context.Context, t Target) error {
	_, err := h.run(ctx, "reset", t)
	return err
}

// httpProbe reads a challenge-backed arena's holder over HTTP and drives its reset.
// The contract (GridWatch): GET /koth/status -> {"holder":"<token>","since":<unix>}
// (holder null = unheld, never rate-limited so the poll always reads), and an
// engine-authenticated POST /koth/reset (Bearer resetSecret) that clears the holder.
type httpProbe struct {
	client      *http.Client
	resetSecret string
}

type kothStatusReply struct {
	Holder *string `json:"holder"`
}

func (p httpProbe) controller(ctx context.Context, t Target) string {
	url := fmt.Sprintf("http://%s:%d/koth/status", t.Host, t.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var reply kothStatusReply
	if json.NewDecoder(resp.Body).Decode(&reply) != nil || reply.Holder == nil {
		return ""
	}
	return *reply.Holder
}

func (p httpProbe) reset(ctx context.Context, t Target) error {
	url := fmt.Sprintf("http://%s:%d/koth/reset", t.Host, t.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	if p.resetSecret != "" {
		req.Header.Set("Authorization", "Bearer "+p.resetSecret)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("koth reset: unexpected status %d", resp.StatusCode)
	}
	return nil
}

type hill struct {
	id          uuid.UUID
	challengeID *uuid.UUID // nil = legacy manual hill (global game_teams tokens); set = per-challenge arena
	target      Target
	checker     hillProbe
}

func (c *Controller) ticksPerRound() int {
	if c.cfg.TickInterval <= 0 {
		return 1
	}
	n := int(c.cfg.Koth.RoundInterval / c.cfg.TickInterval)
	if n < 1 {
		return 1
	}
	return n
}

func (c *Controller) roundForTick(tick int) int {
	if tick < 1 {
		return 1
	}
	return (tick-1)/c.ticksPerRound() + 1
}

// runKoth records who holds each hill this tick and, at a round boundary, closes
// the previous round (rank bonus) and resets the hills.
func (c *Controller) runKoth(ctx context.Context, tick int) error {
	hills, err := c.enabledHills(ctx)
	if err != nil {
		return fmt.Errorf("load hills: %w", err)
	}

	round := c.roundForTick(tick)
	if tick > 1 && round != c.roundForTick(tick-1) {
		if err := c.closeRound(ctx, round-1); err != nil {
			return fmt.Errorf("close round %d: %w", round-1, err)
		}
		if c.cfg.Koth.ResetEnabled {
			c.resetHills(ctx, hills)
		}
	}
	if len(hills) == 0 {
		return nil
	}

	if err := c.ensureRound(ctx, round); err != nil {
		return fmt.Errorf("ensure round: %w", err)
	}

	if err := c.pollHills(ctx, tick, round, hills); err != nil {
		return fmt.Errorf("poll hills: %w", err)
	}
	return nil
}

func (c *Controller) enabledHills(ctx context.Context) ([]hill, error) {
	// A challenge-backed hill (challenge_id set) is probed over HTTP and needs no
	// checker binary; a legacy manual hill needs a non-empty checker_ref.
	rows, err := c.db.Pool.Query(ctx,
		`SELECT id, host(host), port, COALESCE(checker_ref, ''), challenge_id, COALESCE(reset_secret, '')
		 FROM game_koth_hills
		 WHERE enabled = TRUE AND host IS NOT NULL AND port IS NOT NULL
		   AND (challenge_id IS NOT NULL OR (checker_ref IS NOT NULL AND checker_ref <> ''))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []hill
	for rows.Next() {
		var id uuid.UUID
		var host string
		var port int
		var checker string
		var challengeID *uuid.UUID
		var resetSecret string
		if err := rows.Scan(&id, &host, &port, &checker, &challengeID, &resetSecret); err != nil {
			return nil, err
		}
		var probe hillProbe
		if challengeID != nil {
			probe = httpProbe{client: c.emitClient, resetSecret: resetSecret}
		} else {
			probe = hillChecker{command: checker}
		}
		out = append(out, hill{id: id, challengeID: challengeID, target: Target{Host: host, Port: port}, checker: probe})
	}
	return out, rows.Err()
}

func (c *Controller) ensureRound(ctx context.Context, round int) error {
	_, err := c.db.Pool.Exec(ctx,
		`INSERT INTO game_koth_rounds (round_number, started_at, status)
		 VALUES ($1, NOW(), 'running') ON CONFLICT (round_number) DO NOTHING`, round)
	return err
}

func (c *Controller) pollHills(ctx context.Context, tick, round int, hills []hill) error {
	// legacy game_teams tokens, used only for manual hills not backed by a challenge.
	legacy, err := c.teamTokens(ctx)
	if err != nil {
		return err
	}
	for _, h := range hills {
		tokens := legacy
		if h.challengeID != nil {
			// per-arena: only tokens issued for THIS challenge's buy-in count, so a
			// token planted on the wrong arena is never attributed.
			tokens, err = c.entryTokens(ctx, *h.challengeID)
			if err != nil {
				return err
			}
		}
		token := h.checker.controller(ctx, h.target)
		var controller *uuid.UUID
		if token != "" {
			if id, ok := tokens[token]; ok {
				controller = &id
			}
		}
		_, err := c.db.Pool.Exec(ctx,
			`INSERT INTO game_koth_control (tick_number, round_number, hill_id, controller_team_id)
			 VALUES ($1, $2, $3, $4) ON CONFLICT (tick_number, hill_id) DO UPDATE SET
			   round_number = EXCLUDED.round_number,
			   controller_team_id = EXCLUDED.controller_team_id,
			   checked_at = NOW()`,
			tick, round, h.id, controller)
		if err != nil {
			return err
		}
	}
	return nil
}

// closeRound is idempotent: it awards the rank bonus only the first time a
// running round is closed.
func (c *Controller) closeRound(ctx context.Context, round int) error {
	tx, err := c.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE game_koth_rounds SET status = 'closed', ends_at = NOW()
		 WHERE round_number = $1 AND status = 'running'`, round)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	rows, err := tx.Query(ctx,
		`SELECT controller_team_id, COUNT(*) FROM game_koth_control
		 WHERE round_number = $1 AND controller_team_id IS NOT NULL
		 GROUP BY controller_team_id`, round)
	if err != nil {
		return err
	}
	var holds []teamHold
	for rows.Next() {
		var th teamHold
		if err := rows.Scan(&th.Team, &th.Held); err != nil {
			rows.Close()
			return err
		}
		holds = append(holds, th)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for team, pts := range kothRankPoints(c.cfg.Scoring.KothRank, holds) {
		_, err := tx.Exec(ctx,
			`INSERT INTO game_score_events (team_id, round_number, stream, points, source)
			 VALUES ($1, $2, 'KOTH', $3, $4)`,
			team, round, pts, fmt.Sprintf("koth:round:%d", round))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *Controller) resetHills(ctx context.Context, hills []hill) {
	for _, h := range hills {
		if err := h.checker.reset(ctx, h.target); err != nil {
			c.logger.Warn("koth: reset hill", zap.String("hill", h.id.String()), zap.Error(err))
		}
	}
}

func (c *Controller) teamTokens(ctx context.Context) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID)
	rows, err := c.db.Pool.Query(ctx,
		`SELECT token, id FROM game_teams
		 WHERE token IS NOT NULL AND status = 'active' AND is_nop = FALSE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var token string
		var id uuid.UUID
		if err := rows.Scan(&token, &id); err != nil {
			return nil, err
		}
		out[token] = id
	}
	return out, rows.Err()
}
