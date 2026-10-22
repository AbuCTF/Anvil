package game

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

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
// hills) and legacyHTTPProbe (polls the target's HTTP /koth endpoints — challenge-backed
// arenas like GridWatch, the k8s-native path with no binary on the api pod).
type hillProbe interface {
	controller(ctx context.Context, t Target) (string, error)
	reset(ctx context.Context, t Target, round int) error
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

func (h hillChecker) controller(ctx context.Context, t Target) (string, error) {
	out, err := h.run(ctx, "control", t)
	if err != nil {
		return "", probeError("status", "unavailable")
	}
	var reply kothControlReply
	if json.Unmarshal(bytes.TrimSpace(out), &reply) != nil {
		return "", probeError("status", "invalid_response")
	}
	return reply.Controller, nil
}

func (h hillChecker) reset(ctx context.Context, t Target, _ int) error {
	_, err := h.run(ctx, "reset", t)
	if err != nil {
		return probeError("reset", "unavailable")
	}
	return nil
}

// httpProbe reads a challenge-backed arena's holder over HTTP and drives its reset.
// The contract (GridWatch): GET /koth/status -> {"holder":"<token>","since":<unix>}
// (holder null = unheld, never rate-limited so the poll always reads), and an
// engine-authenticated POST /koth/reset (Bearer resetSecret) that clears the holder.
type legacyHTTPProbe struct {
	client      *http.Client
	baseURL     string // e.g. https://web-<id>.web.h7tex.com — the shared instance's endpoint
	resetSecret string
}

type kothStatusReply struct {
	Holder *string `json:"holder"`
}

func (p legacyHTTPProbe) controller(ctx context.Context, _ Target) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/koth/status", nil)
	if err != nil {
		return "", probeError("status", "invalid_request")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", probeError("status", "unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", probeError("status", "http_status")
	}
	var reply kothStatusReply
	if json.NewDecoder(resp.Body).Decode(&reply) != nil {
		return "", probeError("status", "invalid_response")
	}
	if reply.Holder == nil {
		return "", nil
	}
	return *reply.Holder, nil
}

func (p legacyHTTPProbe) reset(ctx context.Context, _ Target, _ int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/koth/reset", nil)
	if err != nil {
		return probeError("reset", "invalid_request")
	}
	if p.resetSecret != "" {
		req.Header.Set("Authorization", "Bearer "+p.resetSecret)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return probeError("reset", "unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return probeError("reset", "http_status")
	}
	return nil
}

type hillV2HTTPProbe struct {
	client      *http.Client
	baseURL     string
	resetSecret string
	hillSlug    string
}

type hillV2StatusReply struct {
	Hill            string            `json:"hill"`
	Round           int               `json:"round"`
	StartedMS       int64             `json:"started_ms"`
	EndsMS          int64             `json:"ends_ms"`
	Holder          *string           `json:"holder"`
	OpenDoorCount   int               `json:"open_door_count"`
	LockedDoorCount int               `json:"locked_door_count"`
	PendingLock     bool              `json:"pending_lock"`
	Events          []json.RawMessage `json:"events"`
}

var hillV2Identity = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type safeProbeError struct{ operation, kind string }

func (e safeProbeError) Error() string { return "hill " + e.operation + " " + e.kind }
func probeError(operation, kind string) error {
	return safeProbeError{operation: operation, kind: kind}
}

func decodeHillV2Status(body io.Reader, expectedHill string) (hillV2StatusReply, error) {
	var reply hillV2StatusReply
	decoder := json.NewDecoder(io.LimitReader(body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil || reply.Hill != expectedHill || reply.Round < 1 ||
		reply.StartedMS < 1 || reply.EndsMS < reply.StartedMS || reply.OpenDoorCount < 0 || reply.OpenDoorCount > 5 ||
		reply.LockedDoorCount < 0 || reply.LockedDoorCount > 1 || reply.Events == nil ||
		(reply.Holder != nil && !hillV2Identity.MatchString(*reply.Holder)) {
		return hillV2StatusReply{}, probeError("status", "invalid_response")
	}
	var extra json.RawMessage
	if decoder.Decode(&extra) != io.EOF {
		return hillV2StatusReply{}, probeError("status", "invalid_response")
	}
	return reply, nil
}

func (p hillV2HTTPProbe) status(ctx context.Context) (hillV2StatusReply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/status", nil)
	if err != nil {
		return hillV2StatusReply{}, probeError("status", "invalid_request")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return hillV2StatusReply{}, probeError("status", "unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hillV2StatusReply{}, probeError("status", "http_status")
	}
	return decodeHillV2Status(resp.Body, p.hillSlug)
}

func (p hillV2HTTPProbe) controller(ctx context.Context, _ Target) (string, error) {
	reply, err := p.status(ctx)
	if err != nil || reply.Holder == nil {
		return "", err
	}
	return *reply.Holder, nil
}

func hillV2ResetKey(round int) (string, error) {
	if round < 1 {
		return "", probeError("reset", "invalid_round")
	}
	return fmt.Sprintf("anvil:round:%08d", round), nil
}

func (p hillV2HTTPProbe) reset(ctx context.Context, _ Target, round int) error {
	key, err := hillV2ResetKey(round)
	if err != nil {
		return err
	}
	before, err := p.status(ctx)
	if err != nil {
		return probeError("reset", "status_unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/reset", strings.NewReader("{}"))
	if err != nil {
		return probeError("reset", "invalid_request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reset-Token", p.resetSecret)
	req.Header.Set("Idempotency-Key", key)
	resp, err := p.client.Do(req)
	if err != nil {
		return probeError("reset", "unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return probeError("reset", "http_status")
	}
	after, err := decodeHillV2Status(resp.Body, p.hillSlug)
	if err != nil {
		return probeError("reset", "invalid_response")
	}
	if after.Round != before.Round+1 && (after.Round != before.Round || after.Holder != nil) {
		return probeError("reset", "round_mismatch")
	}
	return nil
}

type hill struct {
	id          uuid.UUID
	challengeID *uuid.UUID // nil = legacy manual hill (global game_teams tokens); set = per-challenge arena
	protocol    string
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
			c.resetHills(ctx, hills, round)
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
	// A challenge-backed hill is probed over HTTP at base_url (no checker binary);
	// a legacy manual hill needs host+port + a non-empty checker_ref.
	rows, err := c.db.Pool.Query(ctx,
		`SELECT id, slug, COALESCE(host(host), ''), COALESCE(port, 0), COALESCE(checker_ref, ''),
		        challenge_id, COALESCE(reset_secret, ''), COALESCE(base_url, ''), controller_protocol
		 FROM game_koth_hills
		 WHERE enabled = TRUE
		   AND ( (challenge_id IS NOT NULL AND COALESCE(base_url, '') <> '')
		      OR (host IS NOT NULL AND port IS NOT NULL AND COALESCE(checker_ref, '') <> '') )`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []hill
	for rows.Next() {
		var id uuid.UUID
		var slug, host string
		var port int
		var checker string
		var challengeID *uuid.UUID
		var resetSecret, baseURL, protocol string
		if err := rows.Scan(&id, &slug, &host, &port, &checker, &challengeID, &resetSecret, &baseURL, &protocol); err != nil {
			return nil, err
		}
		var probe hillProbe
		if challengeID != nil {
			if protocol == "hill-v2-http" {
				probe = hillV2HTTPProbe{client: c.emitClient, baseURL: baseURL, resetSecret: resetSecret, hillSlug: slug}
			} else {
				probe = legacyHTTPProbe{client: c.emitClient, baseURL: baseURL, resetSecret: resetSecret}
			}
		} else {
			probe = hillChecker{command: checker}
		}
		out = append(out, hill{id: id, challengeID: challengeID, protocol: protocol, target: Target{Host: host, Port: port}, checker: probe})
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
		if h.challengeID != nil && h.protocol == "hill-v2-http" {
			tokens, err = c.entryHolders(ctx, *h.challengeID)
			if err != nil {
				return err
			}
		} else if h.challengeID != nil {
			// per-arena: only tokens issued for THIS challenge's buy-in count, so a
			// token planted on the wrong arena is never attributed.
			tokens, err = c.entryTokens(ctx, *h.challengeID)
			if err != nil {
				return err
			}
		}
		token, probeErr := h.checker.controller(ctx, h.target)
		if probeErr != nil {
			c.logger.Warn("koth: probe hill", zap.String("hill", h.id.String()), zap.Error(probeErr))
		}
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

func (c *Controller) resetHills(ctx context.Context, hills []hill, round int) {
	for _, h := range hills {
		if err := h.checker.reset(ctx, h.target, round); err != nil {
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
