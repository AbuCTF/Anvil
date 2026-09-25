package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// graded challenges: a grader inside the team's instance scores each solution a
// team submits through the challenge's own portal. per solution it asks
// /evaluate (the ledger charge point), runs, then posts the score to /report.
// docs/GRADED.md is the contract.
const (
	gradedMaxBody      = 8 << 10
	gradedMaxRaw       = 4 << 10
	gradedMaxID        = 64
	gradedClockSkew    = 120 * time.Second
	gradedRaceSize     = 10
	gradedFreeEvals    = 3
	gradedEvalCooldown = 10 * time.Minute
	gradedEvalTTL      = 15 * time.Minute // unreported evaluations expire (and refund) after this
	gradedNonceTTL     = 15 * time.Minute // the contract promises at least 10
)

var gradedNoncePattern = regexp.MustCompile(`^[0-9a-fA-F]{32,128}$`)

type GradedHandler struct {
	config  *config.Config
	db      *database.DB
	logger  *zap.Logger
	limiter *gradedLimiter
	now     func() time.Time
}

func NewGradedHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *GradedHandler {
	perMinute := cfg.Graded.ReportsPerMinute
	if perMinute <= 0 {
		perMinute = 60
	}
	return &GradedHandler{
		config:  cfg,
		db:      db,
		logger:  logger,
		limiter: newGradedLimiter(perMinute, time.Minute),
		now:     time.Now,
	}
}

type gradedError struct {
	status     int
	msg        string
	retryAfter int // seconds, for 429s
}

func (e *gradedError) write(c *gin.Context) {
	body := gin.H{"error": e.msg}
	if e.retryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(e.retryAfter))
		body["retry_after"] = e.retryAfter
	}
	c.JSON(e.status, body)
}

type gradedHeaders struct {
	slug, instance, ts, nonce, sig string
}

// checkGradedHeaders does every check that needs no database: presence, shape,
// and the clock window, so junk is turned away before a secret lookup.
func checkGradedHeaders(h http.Header, now time.Time) (gradedHeaders, int, string) {
	g := gradedHeaders{
		slug:     strings.TrimSpace(h.Get("X-Anvil-Challenge")),
		instance: strings.TrimSpace(h.Get("X-Anvil-Instance")),
		ts:       strings.TrimSpace(h.Get("X-Anvil-Timestamp")),
		nonce:    strings.TrimSpace(h.Get("X-Anvil-Nonce")),
		sig:      strings.TrimSpace(h.Get("X-Anvil-Signature")),
	}
	if g.slug == "" || g.instance == "" || g.ts == "" || g.nonce == "" || g.sig == "" {
		return g, http.StatusUnauthorized, "X-Anvil-Challenge, X-Anvil-Instance, X-Anvil-Timestamp, X-Anvil-Nonce and X-Anvil-Signature are required"
	}
	if len(g.slug) > 255 {
		return g, http.StatusBadRequest, "invalid X-Anvil-Challenge"
	}
	if len(g.instance) > 100 {
		return g, http.StatusBadRequest, "invalid X-Anvil-Instance"
	}
	ts, err := strconv.ParseInt(g.ts, 10, 64)
	if err != nil {
		return g, http.StatusBadRequest, "X-Anvil-Timestamp must be unix seconds"
	}
	if skew := now.Sub(time.Unix(ts, 0)); skew > gradedClockSkew || skew < -gradedClockSkew {
		return g, http.StatusUnauthorized, "X-Anvil-Timestamp is outside the allowed 120s window"
	}
	if !gradedNoncePattern.MatchString(g.nonce) {
		return g, http.StatusBadRequest, "X-Anvil-Nonce must be 16-64 random bytes, hex encoded"
	}
	return g, 0, ""
}

// graderKey is what a grader role receives as ${GRADER_SECRET}: the challenge
// secret narrowed to one instance (its ${INSTANCE_ID}), so a key leaked out of
// one team's instance can't sign for any other.
func graderKey(challengeSecret, instanceID string) string {
	m := hmac.New(sha256.New, []byte(challengeSecret))
	m.Write([]byte(instanceID))
	return hex.EncodeToString(m.Sum(nil))
}

func gradedMAC(key []byte, ts, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ts + "\n" + nonce + "\n" + hex.EncodeToString(digest[:])))
	return m.Sum(nil)
}

// gradedSignature is the documented form: the key is the GRADER_SECRET string as-is.
func gradedSignature(key, ts, nonce string, body []byte) string {
	return hex.EncodeToString(gradedMAC([]byte(key), ts, nonce, body))
}

// verifyGradedSignature also accepts the hex-decoded key: authors read "secret"
// both ways and a mid-event mismatch is not worth the argument.
func verifyGradedSignature(secret, ts, nonce string, body []byte, sigHex string) bool {
	got, err := hex.DecodeString(sigHex)
	if err != nil || len(got) != sha256.Size {
		return false
	}
	asText := hmac.Equal(got, gradedMAC([]byte(secret), ts, nonce, body))
	asBytes := false
	if key, err := hex.DecodeString(secret); err == nil && len(key) > 0 {
		asBytes = hmac.Equal(got, gradedMAC(key, ts, nonce, body))
	}
	return asText || asBytes
}

func validGradedID(s string) bool {
	return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= gradedMaxID && !strings.ContainsRune(s, 0)
}

type gradedEvaluate struct {
	teamID uuid.UUID
	evalID string
}

func parseGradedEvaluate(body []byte) (gradedEvaluate, string) {
	var in struct {
		TeamID string `json:"team_id"`
		EvalID string `json:"eval_id"`
	}
	var r gradedEvaluate
	dec := json.NewDecoder(bytes.NewReader(body))
	// strict, so a signed report body can never pass for an evaluate request
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() {
		return r, "body must be a JSON object with exactly team_id and eval_id"
	}
	teamID, err := uuid.Parse(strings.TrimSpace(in.TeamID))
	if err != nil {
		return r, "team_id must be the anvil team uuid"
	}
	if !validGradedID(in.EvalID) {
		return r, "eval_id must be 1-64 characters"
	}
	return gradedEvaluate{teamID: teamID, evalID: in.EvalID}, ""
}

type gradedReport struct {
	teamID uuid.UUID
	evalID string
	status string // ok | infra_error
	score  float64
	key    string
	raw    []byte // nil when the grader sent none
}

func parseGradedReport(body []byte) (gradedReport, string) {
	var in struct {
		TeamID         string          `json:"team_id"`
		EvalID         string          `json:"eval_id"`
		Status         string          `json:"status"`
		Score          *float64        `json:"score"`
		IdempotencyKey string          `json:"idempotency_key"`
		Raw            json.RawMessage `json:"raw"`
	}
	var r gradedReport
	if err := json.Unmarshal(body, &in); err != nil {
		return r, "body must be a JSON object with team_id, eval_id, status, score and idempotency_key"
	}
	teamID, err := uuid.Parse(strings.TrimSpace(in.TeamID))
	if err != nil {
		return r, "team_id must be the anvil team uuid"
	}
	if !validGradedID(in.EvalID) {
		return r, "eval_id must be 1-64 characters"
	}
	status := in.Status
	if status == "" {
		status = "ok"
	}
	if status != "ok" && status != "infra_error" {
		return r, `status must be "ok" or "infra_error"`
	}
	if status == "ok" {
		if in.Score == nil {
			return r, "score is required"
		}
		if s := *in.Score; math.IsNaN(s) || math.IsInf(s, 0) || s < 0 || s > 1 {
			return r, "score must be a finite number in [0,1]"
		}
		r.score = *in.Score
	}
	if !validGradedID(in.IdempotencyKey) {
		return r, "idempotency_key must be 1-64 characters"
	}
	raw := bytes.TrimSpace(in.Raw)
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if len(raw) > gradedMaxRaw {
			return r, "raw must be at most 4KB"
		}
		if raw[0] != '{' {
			return r, "raw must be a JSON object"
		}
		r.raw = raw
	}
	r.teamID, r.evalID, r.status, r.key = teamID, in.EvalID, status, in.IdempotencyKey
	return r, ""
}

// gradedPoints is the non-economy award for a best: round(best * base_points).
func gradedPoints(best float64, basePoints int) int {
	return int(math.Round(best * float64(basePoints)))
}

// gradedEvalCost prices a team's k-th evaluation since opening: the first three
// are free, then 0.2 x the band's launch cost, growing 1.5x each time.
func gradedEvalCost(cfg config.EconomyConfig, difficulty string, k int) float64 {
	if k <= gradedFreeEvals {
		return 0
	}
	return math.Round(0.2 * launchCost(cfg, difficulty) * math.Pow(1.5, float64(k-gradedFreeEvals-1)))
}

// gradedLimiter is a fixed-window counter per (challenge, team). it is process
// local, so the effective cap scales with api replicas: a brake, not a quota.
type gradedLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]gradedWindow
}

type gradedWindow struct {
	start time.Time
	n     int
}

func newGradedLimiter(limit int, window time.Duration) *gradedLimiter {
	return &gradedLimiter{limit: limit, window: window, hits: make(map[string]gradedWindow)}
}

func (l *gradedLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) >= l.window {
		if len(l.hits) >= 4096 {
			for k, v := range l.hits {
				if now.Sub(v.start) >= l.window {
					delete(l.hits, k)
				}
			}
		}
		l.hits[key] = gradedWindow{start: now, n: 1}
		return true, 0
	}
	if w.n >= l.limit {
		return false, w.start.Add(l.window).Sub(now)
	}
	w.n++
	l.hits[key] = w
	return true, 0
}

type gradedChallenge struct {
	id         uuid.UUID
	mode       string
	secret     string
	basePoints int
	difficulty string
}

type gradedRequest struct {
	body []byte
	now  time.Time
	ch   gradedChallenge
	team uuid.UUID // the calling instance's owner; the only team it may speak for
}

// authenticate runs the checks both grader endpoints share: the signature under
// the calling instance's key, the nonce (spent here), and that the instance is
// a team's instance of this challenge. it writes the error response itself.
func (h *GradedHandler) authenticate(c *gin.Context) (gradedRequest, bool) {
	var req gradedRequest
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, gradedMaxBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be at most 8KB"})
		return req, false
	}
	req.body, req.now = body, h.now()
	hdr, status, msg := checkGradedHeaders(c.Request.Header, req.now)
	if status != 0 {
		c.JSON(status, gin.H{"error": msg})
		return req, false
	}
	ctx := c.Request.Context()
	err = h.db.Pool.QueryRow(ctx,
		`SELECT id, scoring_mode, graded_secret, base_points, difficulty FROM challenges WHERE slug = $1`,
		hdr.slug).Scan(&req.ch.id, &req.ch.mode, &req.ch.secret, &req.ch.basePoints, &req.ch.difficulty)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return req, false
	}
	if err != nil {
		h.logger.Error("graded: load challenge", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "grader request failed"})
		return req, false
	}
	if !verifyGradedSignature(graderKey(req.ch.secret, hdr.instance), hdr.ts, hdr.nonce, body, hdr.sig) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return req, false
	}
	// spent even when a later check rejects the request, so a captured request
	// can't be replayed once whatever it failed on (cooldown, credits) clears.
	if _, err := h.db.Pool.Exec(ctx, `INSERT INTO graded_nonces (nonce) VALUES ($1)`, hdr.nonce); err != nil {
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "nonce already used"})
			return req, false
		}
		h.logger.Error("graded: spend nonce", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "grader request failed"})
		return req, false
	}
	if req.ch.mode != "graded" {
		c.JSON(http.StatusForbidden, gin.H{"error": "challenge is not graded"})
		return req, false
	}
	// the instance id is the deterministic per-team cr name, so every launch of
	// one team's instance shares it. admin launches leave team_id null but are
	// keyed on the admin's team, hence the fallback to the launcher's team.
	var owner *uuid.UUID
	err = h.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(i.team_id, u.team_id) FROM instances i
		 LEFT JOIN users u ON u.id = i.user_id
		 WHERE i.challenge_id = $1 AND i.container_id = $2
		 ORDER BY i.created_at DESC LIMIT 1`, req.ch.id, hdr.instance).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner == nil) {
		c.JSON(http.StatusForbidden, gin.H{"error": "no team instance of this challenge with that X-Anvil-Instance"})
		return req, false
	}
	if err != nil {
		h.logger.Error("graded: load instance", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "grader request failed"})
		return req, false
	}
	req.team = *owner
	return req, true
}

// ownTeam refuses a body that speaks for a team other than the instance's owner.
func ownTeam(c *gin.Context, req gradedRequest, teamID uuid.UUID) bool {
	if teamID != req.team {
		c.JSON(http.StatusForbidden, gin.H{"error": "team_id is not the owner of this instance"})
		return false
	}
	return true
}

func (h *GradedHandler) limit(c *gin.Context, req gradedRequest) bool {
	ok, wait := h.limiter.allow(req.ch.id.String()+"|"+req.team.String(), req.now)
	if !ok {
		(&gradedError{
			status:     http.StatusTooManyRequests,
			msg:        "too many grader calls for this team",
			retryAfter: int(math.Ceil(wait.Seconds())),
		}).write(c)
	}
	return ok
}

// phase is the event phase for a grader call ("" when no window is set).
func (h *GradedHandler) phase(ctx context.Context, now time.Time) string {
	start, end, err := loadEventWindow(ctx, h.db)
	if err != nil || start == nil || end == nil {
		return ""
	}
	return eventPhase(now.UTC(), *start, *end)
}

func (h *GradedHandler) internalError(where string, err error) *gradedError {
	h.logger.Error("graded: "+where, zap.Error(err))
	return &gradedError{status: http.StatusInternalServerError, msg: "grader request failed"}
}

type gradedQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadEvalState summarizes a team's evaluations of a challenge: how many count
// toward pricing (those since it was opened, when given), whether one is still
// running, and seconds until another may start. infra errors and expired
// evaluations were refunded, so they neither count nor cool the team down.
func loadEvalState(ctx context.Context, q gradedQuerier, teamID, challengeID uuid.UUID, since *time.Time) (used int, inFlight bool, retryAfter int, err error) {
	err = q.QueryRow(ctx, `
		WITH live AS (
			SELECT status, created_at FROM graded_evaluations
			WHERE team_id = $1 AND challenge_id = $2
			  AND (status = 'ok' OR (status = 'pending' AND created_at > NOW() - ($4 * INTERVAL '1 second')))
		)
		SELECT COUNT(*) FILTER (WHERE $3::timestamptz IS NULL OR created_at >= $3::timestamptz),
		       COALESCE(BOOL_OR(status = 'pending'), false),
		       COALESCE(CEIL(EXTRACT(EPOCH FROM GREATEST(
		           MAX(created_at) FILTER (WHERE status = 'pending') + ($4 * INTERVAL '1 second'),
		           MAX(created_at) + ($5 * INTERVAL '1 second')) - NOW())), 0)::int
		FROM live`,
		teamID, challengeID, since, int(gradedEvalTTL.Seconds()), int(gradedEvalCooldown.Seconds()),
	).Scan(&used, &inFlight, &retryAfter)
	return used, inFlight, max(retryAfter, 0), err
}

type gradedEvaluateResponse struct {
	EvalID          string  `json:"eval_id"`
	Charged         float64 `json:"charged"`
	EvaluationsUsed int     `json:"evaluations_used"`
}

// Evaluate is step one for each submitted solution: the grader asks leave to
// run it. anvil checks the team may evaluate now and charges for it up front.
func (h *GradedHandler) Evaluate(c *gin.Context) {
	req, ok := h.authenticate(c)
	if !ok {
		return
	}
	in, msg := parseGradedEvaluate(req.body)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if !ownTeam(c, req, in.teamID) || !h.limit(c, req) {
		return
	}
	ctx := c.Request.Context()
	phase := h.phase(ctx, req.now)
	if phase == "scheduled" {
		c.JSON(http.StatusForbidden, gin.H{"error": "the competition hasn't started yet"})
		return
	}
	// after the end it's practice: free and ungated. settings are read before the
	// tx: a second pooled read under an open tx starves the pool.
	economyOn := false
	if phase != "ended" {
		var err error
		if economyOn, err = isEconomyMode(ctx, h.db); err != nil {
			h.internalError("read economy_mode", err).write(c)
			return
		}
	}
	resp, gErr := h.admit(ctx, req.ch, in, economyOn)
	if gErr != nil {
		gErr.write(c)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *GradedHandler) admit(ctx context.Context, ch gradedChallenge, in gradedEvaluate, economyOn bool) (gradedEvaluateResponse, *gradedError) {
	resp := gradedEvaluateResponse{EvalID: in.evalID}
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		return resp, h.internalError("begin", err)
	}
	defer tx.Rollback(ctx)

	// admissions for one team+challenge are read-then-insert; take them in turn.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"anvil-graded-eval:"+in.teamID.String()+":"+ch.id.String()); err != nil {
		return resp, h.internalError("lock admission", err)
	}
	// a grader retrying an admission whose answer it lost gets the same answer.
	var owner uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT team_id, charged, seq FROM graded_evaluations WHERE challenge_id = $1 AND eval_id = $2`,
		ch.id, in.evalID).Scan(&owner, &resp.Charged, &resp.EvaluationsUsed)
	if err == nil {
		if owner != in.teamID {
			return gradedEvaluateResponse{}, &gradedError{status: http.StatusConflict, msg: "eval_id already used by another team"}
		}
		return resp, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return resp, h.internalError("load evaluation", err)
	}

	// under the economy only a launched challenge (timer running) or a held one
	// may be evaluated, and the free evaluations restart with each launch.
	var since *time.Time
	if economyOn {
		var st economyState
		err := tx.QueryRow(ctx,
			`SELECT status, expires_at, opened_at FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
			in.teamID, ch.id).Scan(&st.status, &st.expiresAt, &since)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return resp, h.internalError("load economy state", err)
		}
		if !economyCanAct(st, time.Now()) {
			return resp, &gradedError{status: http.StatusForbidden, msg: economyDenied(st)}
		}
	}

	used, inFlight, retry, err := loadEvalState(ctx, tx, in.teamID, ch.id, since)
	if err != nil {
		return resp, h.internalError("load evaluations", err)
	}
	if inFlight {
		return resp, &gradedError{status: http.StatusTooManyRequests, msg: "an evaluation is already running for this team", retryAfter: max(retry, 1)}
	}
	if retry > 0 {
		return resp, &gradedError{status: http.StatusTooManyRequests, msg: "evaluation cooldown", retryAfter: retry}
	}

	k := used + 1
	cost := 0.0
	if economyOn {
		cost = gradedEvalCost(h.config.Economy, ch.difficulty, k)
	}
	if cost > 0 {
		if err := ensureTeamEconomy(ctx, tx, in.teamID, h.config.Economy); err != nil {
			return resp, h.internalError("prepare team economy", err)
		}
		if _, err := applyCredit(ctx, tx, in.teamID, "graded_eval", -cost, &ch.id, nil); err != nil {
			var opErr *EconomyOpError
			if errors.As(err, &opErr) {
				return resp, &gradedError{status: opErr.Status, msg: opErr.Message}
			}
			return resp, h.internalError("charge evaluation", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO graded_evaluations (challenge_id, team_id, eval_id, seq, charged)
		 VALUES ($1, $2, $3, $4, $5)`,
		ch.id, in.teamID, in.evalID, k, cost); err != nil {
		if postgresErrorCode(err) == "23505" {
			return gradedEvaluateResponse{}, &gradedError{status: http.StatusConflict, msg: "eval_id already used by another team"}
		}
		return resp, h.internalError("record evaluation", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return resp, h.internalError("commit evaluation", err)
	}
	resp.Charged, resp.EvaluationsUsed = cost, k
	return resp, nil
}

type gradedResult struct {
	best     float64
	improved bool
	refunded float64
}

// Report is step two: the grader's verdict on an admitted evaluation.
func (h *GradedHandler) Report(c *gin.Context) {
	req, ok := h.authenticate(c)
	if !ok {
		return
	}
	rep, msg := parseGradedReport(req.body)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if !ownTeam(c, req, rep.teamID) || !h.limit(c, req) {
		return
	}
	ctx := c.Request.Context()
	phase := h.phase(ctx, req.now)
	if phase == "scheduled" {
		c.JSON(http.StatusForbidden, gin.H{"error": "the competition hasn't started yet"})
		return
	}
	practice := phase == "ended"
	economyOn := false
	if !practice && rep.status == "ok" {
		var err error
		if economyOn, err = isEconomyMode(ctx, h.db); err != nil {
			h.internalError("read economy_mode", err).write(c)
			return
		}
	}
	res, gErr := h.apply(ctx, req.ch, rep, practice, economyOn)
	if gErr != nil {
		gErr.write(c)
		return
	}
	resp := gin.H{"best": res.best, "improved": res.improved}
	if res.refunded > 0 {
		resp["refunded"] = res.refunded
	}
	if practice {
		resp["practice"] = true
	}
	c.JSON(http.StatusOK, resp)
}

// apply records the report, closes its evaluation and raises the team's best.
// the report row is the idempotency gate: a unique hit is a retry or a second
// report for one evaluation, and a missing team trips the foreign key.
func (h *GradedHandler) apply(ctx context.Context, ch gradedChallenge, rep gradedReport, practice, economyOn bool) (gradedResult, *gradedError) {
	var res gradedResult
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		return res, h.internalError("begin", err)
	}
	defer tx.Rollback(ctx)

	var score any
	if rep.status == "ok" {
		score = rep.score
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO graded_reports (challenge_id, team_id, eval_id, status, score, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		ch.id, rep.teamID, rep.evalID, rep.status, score, rep.key); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch {
			case pgErr.Code == "23503" && pgErr.ConstraintName == "graded_reports_team_id_fkey":
				return res, &gradedError{status: http.StatusNotFound, msg: "team not found"}
			case pgErr.Code == "23505":
				_ = tx.Rollback(ctx)
				return h.duplicate(ctx, ch.id, rep)
			}
		}
		return res, h.internalError("record report", err)
	}

	// a score only counts against an evaluation this team was admitted (and
	// charged) for, and each evaluation takes exactly one verdict.
	var owner uuid.UUID
	var status string
	var charged float64
	var fresh bool
	err = tx.QueryRow(ctx,
		`SELECT team_id, status, charged, created_at > NOW() - ($3 * INTERVAL '1 second')
		 FROM graded_evaluations WHERE challenge_id = $1 AND eval_id = $2 FOR UPDATE`,
		ch.id, rep.evalID, int(gradedEvalTTL.Seconds())).Scan(&owner, &status, &charged, &fresh)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != rep.teamID) {
		return res, &gradedError{status: http.StatusNotFound, msg: "no evaluation with this eval_id for this team"}
	}
	if err != nil {
		return res, h.internalError("load evaluation", err)
	}
	if status != "pending" {
		return res, &gradedError{status: http.StatusConflict, msg: "evaluation already closed (" + status + ")"}
	}
	if !fresh {
		return res, &gradedError{status: http.StatusConflict, msg: "evaluation expired"}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE graded_evaluations SET status = $3, closed_at = NOW() WHERE challenge_id = $1 AND eval_id = $2`,
		ch.id, rep.evalID, rep.status); err != nil {
		return res, h.internalError("close evaluation", err)
	}

	if rep.status == "infra_error" {
		if charged > 0 {
			if _, err := applyCredit(ctx, tx, rep.teamID, "graded_eval_refund", charged, &ch.id, nil); err != nil {
				return res, h.internalError("refund evaluation", err)
			}
			res.refunded = charged
		}
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT best FROM graded_scores WHERE team_id = $1 AND challenge_id = $2), 0)`,
			rep.teamID, ch.id).Scan(&res.best); err != nil {
			return res, h.internalError("load best", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return res, h.internalError("commit", err)
		}
		return res, nil
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO graded_scores (team_id, challenge_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		rep.teamID, ch.id); err != nil {
		return res, h.internalError("seed best", err)
	}
	// the row lock serializes concurrent reports for one team, so the delta is exact.
	var prev float64
	if err := tx.QueryRow(ctx,
		`SELECT best FROM graded_scores WHERE team_id = $1 AND challenge_id = $2 FOR UPDATE`,
		rep.teamID, ch.id).Scan(&prev); err != nil {
		return res, h.internalError("lock best", err)
	}
	if practice || rep.score <= prev {
		if err := tx.Commit(ctx); err != nil {
			return res, h.internalError("commit", err)
		}
		res.best = prev
		return res, nil
	}

	if _, err := tx.Exec(ctx,
		`UPDATE graded_scores SET best = $3, raw = $4, updated_at = NOW()
		 WHERE team_id = $1 AND challenge_id = $2`,
		rep.teamID, ch.id, rep.score, rep.raw); err != nil {
		return res, h.internalError("raise best", err)
	}
	if economyOn {
		// repricing locks every holder's row; serialize per challenge like flag
		// solves do, or two teams improving at once deadlock each other. no key
		// update: this tx's own fk inserts already hold key share on the row, and
		// two reports upgrading those to a full update lock deadlock instead.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM challenges WHERE id = $1 FOR NO KEY UPDATE`, ch.id); err != nil {
			return res, h.internalError("lock challenge", err)
		}
		if err := applyGradedScore(ctx, tx, h.config.Economy, rep.teamID, ch.id, ch.difficulty, rep.score); err != nil {
			var opErr *EconomyOpError
			if errors.As(err, &opErr) {
				return res, &gradedError{status: opErr.Status, msg: opErr.Message}
			}
			return res, h.internalError("economy score", err)
		}
	} else if delta := gradedPoints(rep.score, ch.basePoints) - gradedPoints(prev, ch.basePoints); delta != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE teams SET total_score = total_score + $1, updated_at = NOW() WHERE id = $2`,
			delta, rep.teamID); err != nil {
			return res, h.internalError("team score", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return res, h.internalError("commit", err)
	}
	return gradedResult{best: rep.score, improved: true}, nil
}

// duplicate answers a unique hit on a report: the same idempotency key is a
// retry (current best, no-op); a new key means the evaluation already has its
// verdict. a key spent by a different team is a grader bug, not a retry.
func (h *GradedHandler) duplicate(ctx context.Context, challengeID uuid.UUID, rep gradedReport) (gradedResult, *gradedError) {
	var owner uuid.UUID
	var best float64
	err := h.db.Pool.QueryRow(ctx,
		`SELECT r.team_id, COALESCE(gs.best, 0)
		 FROM graded_reports r
		 LEFT JOIN graded_scores gs ON gs.team_id = r.team_id AND gs.challenge_id = r.challenge_id
		 WHERE r.challenge_id = $1 AND r.idempotency_key = $2`,
		challengeID, rep.key).Scan(&owner, &best)
	if errors.Is(err, pgx.ErrNoRows) {
		return gradedResult{}, &gradedError{status: http.StatusConflict, msg: "evaluation already reported"}
	}
	if err != nil {
		return gradedResult{}, h.internalError("load duplicate", err)
	}
	if owner != rep.teamID {
		return gradedResult{}, &gradedError{status: http.StatusConflict, msg: "idempotency_key already used by another team's report"}
	}
	return gradedResult{best: best}, nil
}

// SweepGraded is the graded janitor: it prunes spent nonces and expires
// evaluations whose grader never reported, refunding what they charged. safe on
// every replica at once.
func SweepGraded(ctx context.Context, db *database.DB) error {
	if _, err := db.Pool.Exec(ctx,
		`DELETE FROM graded_nonces WHERE created_at < NOW() - ($1 * INTERVAL '1 second')`,
		int(gradedNonceTTL.Seconds())); err != nil {
		return fmt.Errorf("prune nonces: %w", err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		UPDATE graded_evaluations SET status = 'expired', closed_at = NOW()
		WHERE id IN (
			SELECT id FROM graded_evaluations
			WHERE status = 'pending' AND created_at <= NOW() - ($1 * INTERVAL '1 second')
			ORDER BY id LIMIT 500
			FOR UPDATE SKIP LOCKED)
		RETURNING team_id, challenge_id, charged`, int(gradedEvalTTL.Seconds()))
	if err != nil {
		return fmt.Errorf("expire evaluations: %w", err)
	}
	type refund struct {
		team, challenge uuid.UUID
		amount          float64
	}
	var refunds []refund
	for rows.Next() {
		var r refund
		if err := rows.Scan(&r.team, &r.challenge, &r.amount); err != nil {
			rows.Close()
			return err
		}
		if r.amount > 0 {
			refunds = append(refunds, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// team order, the same order economy repricing takes team rows in.
	sort.Slice(refunds, func(i, j int) bool { return refunds[i].team.String() < refunds[j].team.String() })
	for _, r := range refunds {
		if _, err := applyCredit(ctx, tx, r.team, "graded_eval_refund", r.amount, &r.challenge, nil); err != nil {
			return fmt.Errorf("refund expired evaluation: %w", err)
		}
	}
	return tx.Commit(ctx)
}

type gradedOwnBest struct {
	Best      float64         `json:"best"`
	Rank      int             `json:"rank,omitempty"` // 0 until the team scores above zero
	Points    *int            `json:"points,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"` // the grader's detail for the best run
	UpdatedAt time.Time       `json:"updated_at"`
}

type gradedEvalState struct {
	Used       int     `json:"used"`
	FreeLeft   int     `json:"free_left"`
	NextCost   float64 `json:"next_cost"`
	InFlight   bool    `json:"in_flight"`
	RetryAfter int     `json:"retry_after"`
}

type gradedRaceEntry struct {
	Rank      int       `json:"rank"`
	Team      string    `json:"team"`
	Best      float64   `json:"best"`
	UpdatedAt time.Time `json:"updated_at"`
	Own       bool      `json:"own,omitempty"`
}

type gradedRaceResponse struct {
	Slug        string            `json:"slug"`
	BasePoints  int               `json:"base_points"`
	Own         *gradedOwnBest    `json:"own"`
	Evaluations *gradedEvalState  `json:"evaluations,omitempty"`
	Top         []gradedRaceEntry `json:"top"`
	Teams       int               `json:"teams"`     // teams scoring above zero
	Attempted   int               `json:"attempted"` // teams that have run an evaluation
	LastUpdate  *time.Time        `json:"last_update"`
	Gated       bool              `json:"gated"`  // economy on and the team hasn't launched it
	Hidden      bool              `json:"hidden"` // scoreboard frozen or disabled
}

// Race is a graded challenge's depth race: the caller's best, evaluations and
// the top teams. it follows challenge gating: under the economy an unlaunched
// team sees only counts, and a frozen or disabled scoreboard hides the field.
func (h *GradedHandler) Race(c *gin.Context) {
	phase, staff := eventPlayState(c, h.db)
	if phase == "scheduled" && !staff {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ctx := c.Request.Context()
	fail := func(where string, err error) {
		h.logger.Error("graded race: "+where, zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load the race"})
	}
	statusCond := "status = 'published' AND (release_date IS NULL OR release_date <= NOW())"
	if staff {
		statusCond = "status IN ('published', 'draft')"
	}
	resp := gradedRaceResponse{Slug: c.Param("slug"), Top: []gradedRaceEntry{}}
	var chID uuid.UUID
	var mode, difficulty string
	err := h.db.Pool.QueryRow(ctx,
		`SELECT id, base_points, scoring_mode, difficulty FROM challenges WHERE slug = $1 AND `+statusCond,
		resp.Slug).Scan(&chID, &resp.BasePoints, &mode, &difficulty)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && mode != "graded") {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		fail("load challenge", err)
		return
	}
	teamID, err := resolveTeamID(ctx, h.db, uid)
	if err != nil {
		fail("resolve team", err)
		return
	}
	economyOn, err := isEconomyMode(ctx, h.db)
	if err != nil {
		fail("read economy_mode", err)
		return
	}
	var since *time.Time
	if economyOn {
		var st economyState
		if teamID != nil {
			_ = h.db.Pool.QueryRow(ctx,
				`SELECT status, expires_at, opened_at FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
				*teamID, chID).Scan(&st.status, &st.expiresAt, &since)
		}
		resp.Gated = !staff && !economyCanView(st)
	}
	if !staff {
		enabled, frozen := h.config.Platform.ScoreboardEnabled, false
		if err := h.db.Pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'scoreboard_enabled'), $1),
			        COALESCE((SELECT value = 'true'::jsonb FROM platform_settings WHERE key = 'scoreboard_frozen'), false)`,
			enabled).Scan(&enabled, &frozen); err != nil {
			fail("read scoreboard settings", err)
			return
		}
		resp.Hidden = frozen || !enabled
	}

	// organizer test teams stay off the race like every other public board
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT e.team_id) FROM graded_evaluations e JOIN teams t ON t.id = e.team_id
		 WHERE e.challenge_id = $1 AND `+publicTeamSQL("t"), chID,
	).Scan(&resp.Attempted); err != nil {
		fail("count attempts", err)
		return
	}
	rows, err := h.db.Pool.Query(ctx, `
		WITH ranked AS (
			SELECT gs.team_id, t.name, gs.best, gs.updated_at,
				ROW_NUMBER() OVER (ORDER BY gs.best DESC, gs.updated_at ASC, gs.team_id) AS rank,
				COUNT(*) OVER () AS total,
				MAX(gs.updated_at) OVER () AS last_update
			FROM graded_scores gs
			JOIN teams t ON t.id = gs.team_id
			WHERE gs.challenge_id = $1 AND gs.best > 0 AND `+publicTeamSQL("t")+`
		)
		SELECT team_id, name, best, updated_at, rank, total, last_update
		FROM ranked WHERE rank <= $3 OR team_id = $2
		ORDER BY rank`, chID, teamID, gradedRaceSize)
	if err != nil {
		fail("query", err)
		return
	}
	defer rows.Close()
	ownRank := 0
	for rows.Next() {
		var e gradedRaceEntry
		var id uuid.UUID
		var lastUpdate time.Time
		if err := rows.Scan(&id, &e.Team, &e.Best, &e.UpdatedAt, &e.Rank, &resp.Teams, &lastUpdate); err != nil {
			fail("scan", err)
			return
		}
		resp.LastUpdate = &lastUpdate
		e.Own = teamID != nil && id == *teamID
		if e.Own {
			ownRank = e.Rank
		}
		if e.Rank <= gradedRaceSize && !resp.Gated && !resp.Hidden {
			resp.Top = append(resp.Top, e)
		}
	}
	if err := rows.Err(); err != nil {
		fail("rows", err)
		return
	}
	rows.Close()

	if teamID != nil {
		own := gradedOwnBest{Rank: ownRank}
		var raw []byte
		err := h.db.Pool.QueryRow(ctx,
			`SELECT best, raw, updated_at FROM graded_scores WHERE team_id = $1 AND challenge_id = $2`,
			*teamID, chID).Scan(&own.Best, &raw, &own.UpdatedAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			fail("load own best", err)
			return
		}
		if err == nil {
			own.Raw = raw
			if !economyOn {
				pts := gradedPoints(own.Best, resp.BasePoints)
				own.Points = &pts
			}
			resp.Own = &own
		}
		used, inFlight, retry, err := loadEvalState(ctx, h.db.Pool, *teamID, chID, since)
		if err != nil {
			fail("load evaluations", err)
			return
		}
		ev := gradedEvalState{Used: used, FreeLeft: max(gradedFreeEvals-used, 0), InFlight: inFlight, RetryAfter: retry}
		if economyOn {
			ev.NextCost = gradedEvalCost(h.config.Economy, difficulty, used+1)
		}
		resp.Evaluations = &ev
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, resp)
}

type gradedEvalLog struct {
	EvalID    string     `json:"eval_id"`
	TeamID    uuid.UUID  `json:"team_id"`
	Team      string     `json:"team"`
	Seq       int        `json:"seq"`
	Charged   float64    `json:"charged"`
	Status    string     `json:"status"`
	Score     *float64   `json:"score"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at"`
}

// AdminInfo is the admin editor's grading panel: secret, wiring and the recent
// evaluations with their verdicts.
func (h *GradedHandler) AdminInfo(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	ctx := c.Request.Context()
	fail := func(where string, err error) {
		h.logger.Error("graded admin: "+where, zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load grading"})
	}
	var mode, secret string
	err = h.db.Pool.QueryRow(ctx,
		`SELECT scoring_mode, graded_secret FROM challenges WHERE id = $1`, id).Scan(&mode, &secret)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		fail("load challenge", err)
		return
	}
	var scored, evaluations int
	var charged float64
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM graded_scores WHERE challenge_id = $1 AND best > 0),
		        (SELECT COUNT(*) FROM graded_evaluations WHERE challenge_id = $1),
		        (SELECT COALESCE(SUM(charged), 0) FROM graded_evaluations
		         WHERE challenge_id = $1 AND status IN ('pending', 'ok'))`, id,
	).Scan(&scored, &evaluations, &charged); err != nil {
		fail("counts", err)
		return
	}
	rows, err := h.db.Pool.Query(ctx,
		`SELECT e.eval_id, e.team_id, t.name, e.seq, e.charged, e.status, r.score, e.created_at, e.closed_at
		 FROM graded_evaluations e
		 JOIN teams t ON t.id = e.team_id
		 LEFT JOIN graded_reports r ON r.challenge_id = e.challenge_id AND r.eval_id = e.eval_id
		 WHERE e.challenge_id = $1 ORDER BY e.id DESC LIMIT 50`, id)
	if err != nil {
		fail("evaluations", err)
		return
	}
	defer rows.Close()
	log := []gradedEvalLog{}
	for rows.Next() {
		var e gradedEvalLog
		if err := rows.Scan(&e.EvalID, &e.TeamID, &e.Team, &e.Seq, &e.Charged, &e.Status, &e.Score, &e.CreatedAt, &e.ClosedAt); err != nil {
			fail("scan evaluation", err)
			return
		}
		log = append(log, e)
	}
	if err := rows.Err(); err != nil {
		fail("evaluation rows", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"scoring_mode":    mode,
		"secret":          secret,
		"report_url":      h.config.Graded.ReportURL,
		"scored_teams":    scored,
		"evaluations":     evaluations,
		"credits_charged": charged,
		"log":             log,
	})
}

// AdminRotate replaces a challenge's grader secret. running instances keep the
// old one in their env, so their calls fail until they are restarted.
func (h *GradedHandler) AdminRotate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid challenge ID"})
		return
	}
	var secret string
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`UPDATE challenges SET graded_secret = encode(gen_random_bytes(32), 'hex'), updated_at = NOW()
		 WHERE id = $1 RETURNING graded_secret`, id).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err != nil {
		h.logger.Error("graded admin: rotate secret", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rotate the secret"})
		return
	}
	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "graded_secret_rotate", "challenge", id.String(), nil); err != nil {
			h.logger.Warn("graded admin: audit rotate", zap.Error(err))
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"secret": secret})
}
