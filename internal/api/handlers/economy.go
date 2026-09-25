package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func isEconomyMode(ctx context.Context, db *database.DB) (bool, error) {
	var enabled bool
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(
			(SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'economy_mode'),
			false)`,
	).Scan(&enabled)
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// easy/medium/hard/insane -> the four economy bands (insane == the sim's "novel").
func bandIndex(difficulty string) int {
	switch difficulty {
	case "easy":
		return 0
	case "medium":
		return 1
	case "hard":
		return 2
	case "insane":
		return 3
	default:
		return 1
	}
}

func bandParam(arr []float64, idx int) float64 {
	if len(arr) == 0 {
		return 0
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(arr) {
		idx = len(arr) - 1
	}
	return arr[idx]
}

// ceiling * (floor + (1-floor)*0.5^(solves/halflife)) * max(wrongFloor, (1-penalty)^wrong)
// challengeValue is a full solve's worth; crowd is the sum of holders' shares
// (for single-flag challenges, the plain solve count).
func challengeValue(cfg config.EconomyConfig, difficulty string, crowd float64, wrongSubs int) float64 {
	b := bandIndex(difficulty)
	ceiling := bandParam(cfg.Ceilings, b)
	floor := bandParam(cfg.CrowdFloors, b)
	halflife := bandParam(cfg.CrowdHalflives, b)

	decay := floor
	if halflife > 0 {
		decay = floor + (1-floor)*math.Pow(0.5, crowd/halflife)
	}
	mult := math.Pow(1-cfg.WrongSubPenalty, float64(wrongSubs))
	if mult < cfg.WrongSubFloor {
		mult = cfg.WrongSubFloor
	}
	return ceiling * decay * mult
}

func launchCost(cfg config.EconomyConfig, difficulty string) float64 {
	return bandParam(cfg.LaunchCosts, bandIndex(difficulty))
}

func bandTimer(cfg config.EconomyConfig, difficulty string) time.Duration {
	steps := bandParam(cfg.TimerSteps, bandIndex(difficulty))
	return time.Duration(steps*cfg.StepMinutes) * time.Minute
}

type EconomyOpError struct {
	Status  int
	Message string
}

func (e *EconomyOpError) Error() string { return e.Message }

// lazy per-team grant, so teams created before economy_mode was on still get it.
func ensureTeamEconomy(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, cfg config.EconomyConfig) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO economy_team_score (team_id) VALUES ($1) ON CONFLICT (team_id) DO NOTHING`, teamID,
	); err != nil {
		return err
	}
	var granted bool
	var credits float64
	if err := tx.QueryRow(ctx,
		`SELECT grant_issued, credits FROM economy_team_score WHERE team_id = $1 FOR UPDATE`, teamID,
	).Scan(&granted, &credits); err != nil {
		return err
	}
	if granted {
		return nil
	}
	newBal := credits + cfg.Grant
	if _, err := tx.Exec(ctx,
		`UPDATE economy_team_score SET credits = $2, grant_issued = TRUE, updated_at = NOW() WHERE team_id = $1`,
		teamID, newBal,
	); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO economy_credit_events (team_id, kind, amount, balance_after) VALUES ($1, 'grant', $2, $3)`,
		teamID, cfg.Grant, newBal)
	return err
}

func applyCredit(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, kind string, amount float64, challengeID, instanceID *uuid.UUID) (float64, error) {
	var credits float64
	if err := tx.QueryRow(ctx,
		`SELECT credits FROM economy_team_score WHERE team_id = $1 FOR UPDATE`, teamID,
	).Scan(&credits); err != nil {
		return 0, err
	}
	newBal := credits + amount
	if newBal < 0 {
		return credits, &EconomyOpError{Status: http.StatusPaymentRequired, Message: "insufficient credits"}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE economy_team_score SET credits = $2, updated_at = NOW() WHERE team_id = $1`, teamID, newBal,
	); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO economy_credit_events (team_id, kind, amount, balance_after, challenge_id, instance_id)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		teamID, kind, amount, newBal, challengeID, instanceID,
	); err != nil {
		return 0, err
	}
	return newBal, nil
}

func openChallengeEconomy(ctx context.Context, tx pgx.Tx, teamID, challengeID uuid.UUID, difficulty string, cfg config.EconomyConfig) *EconomyOpError {
	if err := ensureTeamEconomy(ctx, tx, teamID, cfg); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to prepare team economy"}
	}

	var st economyState
	err := tx.QueryRow(ctx,
		`SELECT status, expires_at FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
		teamID, challengeID,
	).Scan(&st.status, &st.expiresAt)
	if err == nil && economyCanAct(st, time.Now()) {
		return nil // live timer or solved; an expired open re-opens (and pays) below
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to read challenge state"}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE economy_challenge_state SET status = 'expired'
		 WHERE team_id = $1 AND status = 'open' AND expires_at IS NOT NULL AND expires_at < NOW()`, teamID,
	); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to expire stale opens"}
	}

	var openCount int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM economy_challenge_state WHERE team_id = $1 AND status = 'open'`, teamID,
	).Scan(&openCount); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to check concurrency"}
	}
	if openCount >= cfg.ConcurrencyCap {
		return &EconomyOpError{Status: http.StatusConflict,
			Message: fmt.Sprintf("you already have %d challenges open (max %d); solve or abandon one first", openCount, cfg.ConcurrencyCap)}
	}

	cid := challengeID
	if _, err := applyCredit(ctx, tx, teamID, "launch_spend", -launchCost(cfg, difficulty), &cid, nil); err != nil {
		if opErr, ok := err.(*EconomyOpError); ok {
			return opErr
		}
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to charge launch cost"}
	}

	expires := time.Now().Add(bandTimer(cfg, difficulty))
	if _, err := tx.Exec(ctx,
		`INSERT INTO economy_challenge_state (team_id, challenge_id, status, opened_at, expires_at)
		 VALUES ($1, $2, 'open', NOW(), $3)
		 ON CONFLICT (team_id, challenge_id) DO UPDATE
		   SET status = 'open', opened_at = NOW(), expires_at = $3`,
		teamID, challengeID, expires,
	); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to open challenge"}
	}
	return nil
}

// crowd decay is field-wide and retroactive, so a solve recomputes the value for
// every team that holds the challenge, not just the solver. no-op if already held.
func applyEconomySolve(ctx context.Context, tx pgx.Tx, cfg config.EconomyConfig, teamID, challengeID uuid.UUID, difficulty string) error {
	frac, err := teamFlagFrac(ctx, tx, teamID, challengeID)
	if err != nil {
		return err
	}
	return applyEconomyFrac(ctx, tx, cfg, teamID, challengeID, difficulty, frac)
}

// applyGradedScore raises a graded challenge's share to the reported best.
func applyGradedScore(ctx context.Context, tx pgx.Tx, cfg config.EconomyConfig, teamID, challengeID uuid.UUID, difficulty string, score float64) error {
	return applyEconomyFrac(ctx, tx, cfg, teamID, challengeID, difficulty, score)
}

// teamFlagFrac is the team's share of a challenge's flags, weighted by flag points
// (equal weights when every flag is worth 0). called after the solve row is written.
func teamFlagFrac(ctx context.Context, tx pgx.Tx, teamID, challengeID uuid.UUID) (float64, error) {
	var held, total float64
	var heldN, totalN int
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(f.points) FILTER (WHERE h.flag_id IS NOT NULL), 0),
		        COALESCE(SUM(f.points), 0),
		        COUNT(h.flag_id), COUNT(*)
		 FROM flags f
		 LEFT JOIN (SELECT DISTINCT s.flag_id FROM solves s JOIN users u ON u.id = s.user_id
		            WHERE u.team_id = $1 AND s.challenge_id = $2) h ON h.flag_id = f.id
		 WHERE f.challenge_id = $2`, teamID, challengeID,
	).Scan(&held, &total, &heldN, &totalN)
	if err != nil {
		return 0, err
	}
	return shareOf(held, total, heldN, totalN), nil
}

func shareOf(held, total float64, heldN, totalN int) float64 {
	if total > 0 {
		return held / total
	}
	if totalN > 0 {
		return float64(heldN) / float64(totalN)
	}
	return 1
}

// applyEconomyFrac raises a team's share of a challenge (flags held, or a graded
// best) to frac and re-prices every holder. the crowd is the sum of holders'
// shares, so shallow grabs decay a challenge less than full solves do, and each
// holder earns value x its own share. first capture counts the team as a holder
// and pays the clean refund, once.
func applyEconomyFrac(ctx context.Context, tx pgx.Tx, cfg config.EconomyConfig, teamID, challengeID uuid.UUID, difficulty string, frac float64) error {
	if math.IsNaN(frac) || frac <= 0 {
		return nil
	}
	frac = math.Min(frac, 1)

	var wrongSubs int
	var holds bool
	var cur float64
	err := tx.QueryRow(ctx,
		`SELECT wrong_subs, holds_solve, frac FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2 FOR UPDATE`,
		teamID, challengeID,
	).Scan(&wrongSubs, &holds, &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, e := tx.Exec(ctx,
			`INSERT INTO economy_challenge_state (team_id, challenge_id, status) VALUES ($1, $2, 'open')
			 ON CONFLICT (team_id, challenge_id) DO NOTHING`, teamID, challengeID); e != nil {
			return e
		}
	} else if err != nil {
		return err
	}
	if holds && frac <= cur {
		return nil // nothing deeper than what the team already holds
	}
	first := !holds

	if _, err := tx.Exec(ctx,
		`UPDATE economy_challenge_state SET status = 'solved', holds_solve = TRUE, frac = $3
		 WHERE team_id = $1 AND challenge_id = $2`, teamID, challengeID, frac); err != nil {
		return err
	}
	if first {
		// display count: teams holding any share
		if _, err := tx.Exec(ctx,
			`UPDATE challenges SET economy_solve_count = economy_solve_count + 1 WHERE id = $1`, challengeID); err != nil {
			return err
		}
	}

	var crowd float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(frac), 0) FROM economy_challenge_state WHERE challenge_id = $1 AND holds_solve`,
		challengeID).Scan(&crowd); err != nil {
		return err
	}

	rows, err := tx.Query(ctx,
		`SELECT team_id, wrong_subs, current_value, frac FROM economy_challenge_state
		 WHERE challenge_id = $1 AND holds_solve = TRUE ORDER BY team_id FOR UPDATE`, challengeID)
	if err != nil {
		return err
	}
	type holder struct {
		team  uuid.UUID
		wrong int
		cur   float64
		frac  float64
	}
	var holders []holder
	for rows.Next() {
		var hh holder
		if err := rows.Scan(&hh.team, &hh.wrong, &hh.cur, &hh.frac); err != nil {
			rows.Close()
			return err
		}
		holders = append(holders, hh)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, hh := range holders {
		newVal := challengeValue(cfg, difficulty, crowd, hh.wrong) * hh.frac
		delta := newVal - hh.cur
		if _, err := tx.Exec(ctx,
			`UPDATE economy_challenge_state SET current_value = $3 WHERE team_id = $1 AND challenge_id = $2`,
			hh.team, challengeID, newVal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE economy_team_score SET points = points + $2, updated_at = NOW() WHERE team_id = $1`,
			hh.team, delta); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO economy_point_events (team_id, challenge_id, kind, value_after)
			 VALUES ($1, $2, 'crowd_recompute', $3)`, hh.team, challengeID, newVal); err != nil {
			return err
		}
	}
	// the refund comes last: team score rows are taken in team_id order above, and
	// the solver's is already among them, so two solves on different challenges
	// can't each hold one team's row while waiting on the other's (deadlock -> 500).
	if first && wrongSubs == 0 {
		if refund := cfg.CleanRefundFrac * launchCost(cfg, difficulty); refund > 0 {
			cid := challengeID
			if _, err := applyCredit(ctx, tx, teamID, "clean_refund", refund, &cid, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func abandonChallengeEconomy(ctx context.Context, tx pgx.Tx, teamID, challengeID uuid.UUID, difficulty string, cfg config.EconomyConfig) *EconomyOpError {
	var st economyState
	err := tx.QueryRow(ctx,
		`SELECT status, expires_at FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2 FOR UPDATE`,
		teamID, challengeID).Scan(&st.status, &st.expiresAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (st.status != "open" || !economyCanAct(st, time.Now()))) {
		return &EconomyOpError{Status: http.StatusBadRequest, Message: "challenge is not open"}
	}
	if err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to read challenge state"}
	}
	if refund := cfg.AbandonRefundFrac * launchCost(cfg, difficulty); refund > 0 {
		cid := challengeID
		if _, err := applyCredit(ctx, tx, teamID, "abandon_refund", refund, &cid, nil); err != nil {
			return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to refund"}
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE economy_challenge_state SET status = 'abandoned' WHERE team_id = $1 AND challenge_id = $2`,
		teamID, challengeID); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to abandon"}
	}
	return nil
}

func extendChallengeEconomy(ctx context.Context, tx pgx.Tx, teamID, challengeID uuid.UUID, difficulty string, cfg config.EconomyConfig) (time.Time, *EconomyOpError) {
	var status string
	var used int
	var expires time.Time
	err := tx.QueryRow(ctx,
		`SELECT status, extensions_used, COALESCE(expires_at, NOW()) FROM economy_challenge_state
		 WHERE team_id = $1 AND challenge_id = $2 FOR UPDATE`, teamID, challengeID).Scan(&status, &used, &expires)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (status != "open" || !expires.After(time.Now()))) {
		return time.Time{}, &EconomyOpError{Status: http.StatusBadRequest, Message: "challenge is not open"}
	}
	if err != nil {
		return time.Time{}, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to read challenge state"}
	}
	if used >= cfg.MaxExtensions {
		return time.Time{}, &EconomyOpError{Status: http.StatusConflict, Message: "no extensions remaining"}
	}
	costFrac := bandParam(cfg.ExtCostFracs, used)
	cid := challengeID
	if _, err := applyCredit(ctx, tx, teamID, "extend_spend", -costFrac*launchCost(cfg, difficulty), &cid, nil); err != nil {
		if opErr, ok := err.(*EconomyOpError); ok {
			return time.Time{}, opErr
		}
		return time.Time{}, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to charge extension"}
	}
	add := time.Duration(float64(bandTimer(cfg, difficulty)) * cfg.ExtAddStepsFrac)
	newExpiry := expires.Add(add)
	if _, err := tx.Exec(ctx,
		`UPDATE economy_challenge_state SET expires_at = $3, extensions_used = extensions_used + 1
		 WHERE team_id = $1 AND challenge_id = $2`, teamID, challengeID, newExpiry); err != nil {
		return time.Time{}, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to extend"}
	}
	return newExpiry, nil
}

// sell points for credits, block by block so the rate diminishes as more is sold.
func convertPointsToCredits(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, points float64, cfg config.EconomyConfig) (float64, *EconomyOpError) {
	if points <= 0 {
		return 0, &EconomyOpError{Status: http.StatusBadRequest, Message: "points must be positive"}
	}
	if err := ensureTeamEconomy(ctx, tx, teamID, cfg); err != nil {
		return 0, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to prepare team economy"}
	}
	var have float64
	var blocks int
	if err := tx.QueryRow(ctx,
		`SELECT points, p2c_blocks FROM economy_team_score WHERE team_id = $1 FOR UPDATE`, teamID,
	).Scan(&have, &blocks); err != nil {
		return 0, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to read balance"}
	}
	if points > have {
		return 0, &EconomyOpError{Status: http.StatusBadRequest, Message: "you do not have enough points to convert"}
	}
	remaining := points
	credits := 0.0
	block := cfg.P2CBlock
	if block <= 0 {
		block = 50
	}
	for remaining > 0 {
		chunk := math.Min(block, remaining)
		rate := cfg.P2CBase * math.Pow(cfg.P2CRateDecay, float64(blocks))
		if rate < cfg.P2CMinRate {
			rate = cfg.P2CMinRate
		}
		credits += chunk * rate
		remaining -= chunk
		blocks++
	}
	if _, err := tx.Exec(ctx,
		`UPDATE economy_team_score SET points = points - $2, p2c_blocks = $3, updated_at = NOW() WHERE team_id = $1`,
		teamID, points, blocks); err != nil {
		return 0, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to debit points"}
	}
	if _, err := applyCredit(ctx, tx, teamID, "p2c_convert", credits, nil, nil); err != nil {
		return 0, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to credit"}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO economy_point_events (team_id, challenge_id, kind, value_after)
		 SELECT $1, NULL, 'convert_out', points FROM economy_team_score WHERE team_id = $1`, teamID); err != nil {
		return 0, &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to log conversion"}
	}
	return credits, nil
}

func bailoutEconomy(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, cfg config.EconomyConfig) *EconomyOpError {
	if err := ensureTeamEconomy(ctx, tx, teamID, cfg); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to prepare team economy"}
	}
	var credits float64
	var used bool
	if err := tx.QueryRow(ctx,
		`SELECT credits, bailout_used FROM economy_team_score WHERE team_id = $1 FOR UPDATE`, teamID,
	).Scan(&credits, &used); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to read balance"}
	}
	if used {
		return &EconomyOpError{Status: http.StatusConflict, Message: "bailout already used"}
	}
	if credits > 0 {
		return &EconomyOpError{Status: http.StatusBadRequest, Message: "bailout is available only when your balance reaches zero"}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE economy_team_score SET bailout_used = TRUE WHERE team_id = $1`, teamID); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to record bailout"}
	}
	if _, err := applyCredit(ctx, tx, teamID, "bailout", cfg.Bailout, nil, nil); err != nil {
		return &EconomyOpError{Status: http.StatusInternalServerError, Message: "failed to credit bailout"}
	}
	return nil
}

type EconomyHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewEconomyHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *EconomyHandler {
	return &EconomyHandler{config: cfg, db: db, logger: logger}
}

func (h *EconomyHandler) econContext(c *gin.Context) (uuid.UUID, bool) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.Nil, false
	}
	on, err := isEconomyMode(c.Request.Context(), h.db)
	if err != nil {
		h.logger.Error("failed to read economy_mode", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "economy unavailable"})
		return uuid.Nil, false
	}
	if !on {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the economy is not enabled"})
		return uuid.Nil, false
	}
	teamID, err := resolveTeamID(c.Request.Context(), h.db, uid)
	if err != nil {
		h.logger.Error("failed to resolve team", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "economy unavailable"})
		return uuid.Nil, false
	}
	if teamID == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "join a team to use the economy"})
		return uuid.Nil, false
	}
	return *teamID, true
}

func (h *EconomyHandler) Balance(c *gin.Context) {
	teamID, ok := h.econContext(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var credits, points float64
	var grantIssued, bailoutUsed bool
	err := h.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(credits,0), COALESCE(points,0), COALESCE(grant_issued,false), COALESCE(bailout_used,false)
		 FROM economy_team_score WHERE team_id = $1`, teamID).Scan(&credits, &points, &grantIssued, &bailoutUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, gin.H{"credits": 0, "points": 0, "grant_issued": false, "bailout_used": false, "open": []gin.H{}})
		return
	}
	if err != nil {
		h.logger.Error("failed to read economy balance", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read balance"})
		return
	}
	rows, err := h.db.Pool.Query(ctx,
		`SELECT c.slug, c.name, e.status, e.expires_at, e.wrong_subs, e.current_value
		 FROM economy_challenge_state e JOIN challenges c ON c.id = e.challenge_id
		 WHERE e.team_id = $1 AND e.status IN ('open','solved')
		 ORDER BY e.opened_at DESC NULLS LAST`, teamID)
	open := []gin.H{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug, name, status string
			var expires *time.Time
			var wrong int
			var val float64
			if err := rows.Scan(&slug, &name, &status, &expires, &wrong, &val); err == nil {
				open = append(open, gin.H{"slug": slug, "name": name, "status": status,
					"expires_at": expires, "wrong_subs": wrong, "value": val})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"credits": credits, "points": points, "grant_issued": grantIssued,
		"bailout_used": bailoutUsed, "open": open,
	})
}

func (h *EconomyHandler) Bailout(c *gin.Context) {
	teamID, ok := h.econContext(c)
	if !ok {
		return
	}
	h.runTx(c, func(tx pgx.Tx) *EconomyOpError {
		return bailoutEconomy(c.Request.Context(), tx, teamID, h.config.Economy)
	})
}

func (h *EconomyHandler) Convert(c *gin.Context) {
	teamID, ok := h.econContext(c)
	if !ok {
		return
	}
	var req struct {
		Points float64 `json:"points" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "points is required"})
		return
	}
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "conversion failed"})
		return
	}
	defer tx.Rollback(ctx)
	gained, opErr := convertPointsToCredits(ctx, tx, teamID, req.Points, h.config.Economy)
	if opErr != nil {
		c.JSON(opErr.Status, gin.H{"error": opErr.Message})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "conversion failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"credits_gained": gained, "points_spent": req.Points})
}

func (h *EconomyHandler) runTx(c *gin.Context, op func(tx pgx.Tx) *EconomyOpError) {
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
		return
	}
	defer tx.Rollback(ctx)
	if opErr := op(tx); opErr != nil {
		c.JSON(opErr.Status, gin.H{"error": opErr.Message})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// freeze converts every team's leftover credits to points and blinds the board.
func (h *EconomyHandler) Freeze(c *gin.Context) {
	ctx := c.Request.Context()
	rate := h.config.Economy.C2PRate
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze failed"})
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO economy_credit_events (team_id, kind, amount, balance_after)
		 SELECT team_id, 'c2p_freeze', -credits, 0 FROM economy_team_score WHERE credits > 0`); err != nil {
		h.logger.Error("freeze: credit log failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze failed"})
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO economy_point_events (team_id, challenge_id, kind, value_after)
		 SELECT team_id, NULL, 'freeze_convert_in', points + credits * $1 FROM economy_team_score WHERE credits > 0`, rate); err != nil {
		h.logger.Error("freeze: point log failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze failed"})
		return
	}
	tag, err := tx.Exec(ctx,
		`UPDATE economy_team_score SET points = points + credits * $1, credits = 0, updated_at = NOW() WHERE credits > 0`, rate)
	if err != nil {
		h.logger.Error("freeze: convert failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze failed"})
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE platform_settings SET value = 'true'::jsonb, updated_at = NOW() WHERE key = 'scoreboard_frozen'`); err != nil {
		h.logger.Error("freeze: flag set failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze failed"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "frozen", "teams_converted": tag.RowsAffected(), "c2p_rate": rate})
}
