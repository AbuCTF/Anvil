package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// WebVerse Labs is the web-category sponsor: 5 web challenges are played on
// WebVerse's own platform and scored here by polling their leaderboard export and
// capturing solves through the NORMAL economy path (the team must have opened the
// challenge; the existing capture 409s otherwise, which we reuse rather than
// bypass). Everything is config-driven behind a master off switch so nothing runs
// until the owner enables it after prod + slug confirmation.

// advisory lock so only one replica polls at a time (mirrors the game engine's tick lock).
const webverseSyncLockID int64 = 0x5765625665727365 // "WebVerse"

// runtime pause key: ops can pause sync live (no redeploy) once the master switch is on.
const webverseRuntimeKey = "webverse_sync_enabled"

// WebVersePoller is the background worker. Start it from cmd/server like the game
// controller: go NewWebVersePoller(cfg, db, logger).Run(ctx).
type WebVersePoller struct {
	cfg      config.WebVerseConfig
	econ     config.EconomyConfig
	db       *database.DB
	logger   *zap.Logger
	client   *http.Client
	eventEnd time.Time
	slugMap  map[string]string // normalized webverse slug -> anvil slug
	authMode string            // cached working auth mode ("header"/"query"); set on first success
}

func NewWebVersePoller(cfg *config.Config, db *database.DB, logger *zap.Logger) *WebVersePoller {
	wc := cfg.WebVerse
	timeout := wc.HTTPTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	p := &WebVersePoller{
		cfg:     wc,
		econ:    cfg.Economy,
		db:      db,
		logger:  logger.With(zap.String("worker", "webverse")),
		client:  &http.Client{Timeout: timeout},
		slugMap: map[string]string{},
	}
	for k, v := range wc.SlugMap {
		nk := normSlug(k)
		nv := strings.TrimSpace(v)
		if nk != "" && nv != "" {
			p.slugMap[nk] = nv
		}
	}
	if wc.EventEnd != "" {
		if t, err := time.Parse(time.RFC3339, wc.EventEnd); err == nil {
			p.eventEnd = t.UTC()
		} else {
			p.logger.Warn("invalid webverse.event_end; no upper time bound applied", zap.String("value", wc.EventEnd))
		}
	}
	return p
}

// Run polls until ctx is cancelled. It returns immediately unless the master switch
// is on, so it is always safe to start unconditionally from cmd/server.
func (p *WebVersePoller) Run(ctx context.Context) {
	if !p.cfg.Enabled {
		p.logger.Info("webverse sync disabled (master off switch); not polling")
		return
	}
	interval := p.cfg.PollInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	p.logger.Info("webverse sync started",
		zap.Duration("interval", interval),
		zap.Bool("dry_run", p.cfg.DryRun),
		zap.String("base_url", p.cfg.BaseURL),
		zap.Int("slug_map_entries", len(p.slugMap)),
		zap.Time("event_end", p.eventEnd))

	// first pass shortly after boot, then on the interval.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("webverse sync stopped")
			return
		case <-timer.C:
			p.tick(ctx)
			timer.Reset(interval)
		}
	}
}

func (p *WebVersePoller) tick(ctx context.Context) {
	// runtime kill switch (no redeploy). default enabled once the master switch is on.
	if on, err := boolSettingOrDefault(ctx, p.db, webverseRuntimeKey, true); err != nil {
		p.logger.Warn("webverse: failed to read runtime toggle; proceeding", zap.Error(err))
	} else if !on {
		return
	}

	release, ok, err := p.acquireLock(ctx)
	if err != nil {
		p.logger.Error("webverse: advisory lock error", zap.Error(err))
		return
	}
	if !ok {
		return // another replica is syncing this tick
	}
	defer release()

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	st, err := p.syncOnce(runCtx)
	if err != nil {
		// survive API downtime: log and let the next poll catch up.
		p.logger.Error("webverse: sync failed (will retry next poll)", zap.Error(err),
			zap.Int("participants", st.Participants))
		return
	}
	fields := []zap.Field{
		zap.Bool("dry_run", p.cfg.DryRun),
		zap.String("event", st.Event),
		zap.String("event_status", st.EventStatus),
		zap.Int("participants", st.Participants),
		zap.Int("solves_seen", st.SolvesSeen),
		zap.Int("mapped_solves", st.Mapped),
		zap.Int("matched_teams", st.MatchedTeams),
		zap.Int("captured", st.Captured),
		zap.Int("already_solved", st.AlreadySolved),
		zap.Int("not_open", st.NotOpen),
		zap.Int("out_of_window", st.OutOfWindow),
		zap.Int("errors", st.Errors),
	}
	if len(st.UnmappedSlugs) > 0 {
		fields = append(fields, zap.Any("unmapped_webverse_slugs", st.UnmappedSlugs))
	}
	if len(st.UnmatchedEmails) > 0 {
		// ops list: players whose WebVerse email doesn't match an Anvil account.
		fields = append(fields, zap.Int("unmatched_email_count", len(st.UnmatchedEmails)),
			zap.Strings("unmatched_emails", capStrings(st.UnmatchedEmails, 25)))
	}
	p.logger.Info("webverse: sync complete", fields...)
}

func (p *WebVersePoller) acquireLock(ctx context.Context) (func(), bool, error) {
	conn, err := p.db.Pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, webverseSyncLockID).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !acquired {
		conn.Release()
		return func() {}, false, nil
	}
	release := func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, webverseSyncLockID).Scan(&unlocked); err != nil || !unlocked {
			p.logger.Error("webverse: failed to release advisory lock", zap.Error(err))
			raw := conn.Hijack()
			_ = raw.Close(context.Background())
			return
		}
		conn.Release()
	}
	return release, true, nil
}

type syncStats struct {
	Event           string
	EventStatus     string
	Participants    int
	SolvesSeen      int
	Mapped          int
	MatchedTeams    int
	Captured        int
	AlreadySolved   int
	NotOpen         int
	OutOfWindow     int
	Errors          int
	UnmappedSlugs   map[string]int
	UnmatchedEmails []string
}

type wvExport struct {
	Event struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Status string `json:"status"`
	} `json:"event"`
	GeneratedAt  string          `json:"generated_at"`
	Participants []wvParticipant `json:"participants"`
}

type wvParticipant struct {
	Username    string          `json:"username"`
	Email       string          `json:"email"`
	Points      float64         `json:"points"`
	Solves      json.RawMessage `json:"solves"`       // real API (array); element shape varies
	SolvedSlugs json.RawMessage `json:"solved_slugs"` // spec fallback key
}

type wvSolve struct {
	slug     string
	solvedAt *time.Time
}

type chalMeta struct {
	challengeID uuid.UUID
	difficulty  string
	flagID      uuid.UUID
	points      int
}

type teamRef struct {
	userID uuid.UUID
	teamID uuid.UUID
}

type captureResult int

const (
	capCaptured captureResult = iota
	capAlready
	capNotOpen
	capOutOfWindow
)

func (p *WebVersePoller) syncOnce(ctx context.Context) (syncStats, error) {
	st := syncStats{UnmappedSlugs: map[string]int{}}

	exp, err := p.fetchExport(ctx)
	if err != nil {
		return st, err
	}
	st.Event = exp.Event.Name
	st.EventStatus = exp.Event.Status
	if p.cfg.ExpectSlug != "" && exp.Event.Slug != "" && exp.Event.Slug != p.cfg.ExpectSlug {
		p.logger.Warn("webverse: export event slug differs from expected",
			zap.String("got", exp.Event.Slug), zap.String("expected", p.cfg.ExpectSlug))
	}
	st.Participants = len(exp.Participants)

	type want struct {
		anvilSlug string
		solvedAt  *time.Time
	}
	byEmail := map[string][]want{}
	emailSet := map[string]struct{}{}
	for _, part := range exp.Participants {
		email := normEmail(part.Email)
		if email == "" {
			continue
		}
		for _, s := range parseSolves(part.Solves, part.SolvedSlugs) {
			st.SolvesSeen++
			anvil, ok := p.slugMap[s.slug]
			if !ok || anvil == "" {
				st.UnmappedSlugs[s.slug]++
				continue
			}
			st.Mapped++
			byEmail[email] = append(byEmail[email], want{anvilSlug: anvil, solvedAt: s.solvedAt})
			emailSet[email] = struct{}{}
		}
	}
	if len(byEmail) == 0 {
		return st, nil
	}

	emailToTeam, err := p.resolveTeams(ctx, keysOf(emailSet))
	if err != nil {
		return st, fmt.Errorf("resolve teams: %w", err)
	}

	slugSet := map[string]struct{}{}
	for _, ws := range byEmail {
		for _, w := range ws {
			slugSet[w.anvilSlug] = struct{}{}
		}
	}
	chalBySlug, err := p.resolveChallenges(ctx, keysOf(slugSet))
	if err != nil {
		return st, fmt.Errorf("resolve challenges: %w", err)
	}

	// team-level capture once per (team, challenge): keep the earliest solvedAt and
	// the member who owns it (first blood by solved_at).
	type tcKey struct{ team, chal uuid.UUID }
	type tcAgg struct {
		user     uuid.UUID
		solvedAt *time.Time
		meta     chalMeta
	}
	best := map[tcKey]*tcAgg{}
	unmatched := map[string]struct{}{}
	for email, ws := range byEmail {
		tr, ok := emailToTeam[email]
		if !ok {
			unmatched[email] = struct{}{}
			continue
		}
		for _, w := range ws {
			meta, ok := chalBySlug[w.anvilSlug]
			if !ok {
				// mapped slug has no poller-scored (external-flag) challenge yet.
				p.logger.Warn("webverse: mapped slug has no external-flag challenge",
					zap.String("anvil_slug", w.anvilSlug))
				continue
			}
			k := tcKey{tr.teamID, meta.challengeID}
			if a := best[k]; a == nil {
				best[k] = &tcAgg{user: tr.userID, solvedAt: w.solvedAt, meta: meta}
			} else if earlier(w.solvedAt, a.solvedAt) {
				a.solvedAt = w.solvedAt
				a.user = tr.userID
			}
		}
	}
	st.UnmatchedEmails = sortedKeys(unmatched)

	distinctTeams := map[uuid.UUID]struct{}{}
	for k := range best {
		distinctTeams[k.team] = struct{}{}
	}
	st.MatchedTeams = len(distinctTeams)

	for k, a := range best {
		var res captureResult
		var cerr error
		if p.cfg.DryRun {
			res, cerr = p.evaluateOne(ctx, k.team, k.chal, a.solvedAt)
		} else {
			res, cerr = p.captureOne(ctx, k.team, a.user, a.meta, a.solvedAt)
		}
		if cerr != nil {
			st.Errors++
			p.logger.Error("webverse: capture error",
				zap.Error(cerr), zap.String("team", k.team.String()), zap.String("challenge", k.chal.String()))
			continue
		}
		switch res {
		case capCaptured:
			st.Captured++
			if !p.cfg.DryRun {
				p.logger.Info("webverse: captured solve",
					zap.String("team", k.team.String()), zap.String("challenge", k.chal.String()))
			}
		case capAlready:
			st.AlreadySolved++
		case capNotOpen:
			st.NotOpen++
		case capOutOfWindow:
			st.OutOfWindow++
		}
	}
	return st, nil
}

// captureOne records the solve and captures the economy value through the existing
// applyEconomySolve path (which re-checks open+live under a row lock and 409s if the
// team's timer lapsed). Everything happens in one tx, so a skipped/failed capture
// leaves no partial state and the next poll retries. Idempotent.
func (p *WebVersePoller) captureOne(ctx context.Context, teamID, userID uuid.UUID, meta chalMeta, solvedAt *time.Time) (captureResult, error) {
	tx, err := p.db.Pool.Begin(ctx)
	if err != nil {
		return capNotOpen, err
	}
	defer tx.Rollback(ctx)

	// serialize with the normal solve path on this challenge (denormalized counts).
	var lock int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM challenges WHERE id = $1 FOR UPDATE`, meta.challengeID).Scan(&lock); err != nil {
		return capNotOpen, err
	}

	var status string
	var openedAt, expiresAt *time.Time
	var holds bool
	var frac float64
	err = tx.QueryRow(ctx,
		`SELECT status, opened_at, expires_at, holds_solve, frac
		 FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
		teamID, meta.challengeID).Scan(&status, &openedAt, &expiresAt, &holds, &frac)
	noRow := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !noRow {
		return capNotOpen, err
	}
	if !noRow {
		if status == "solved" && holds && frac >= 1 {
			return capAlready, nil // team already holds the full solve
		}
		if !windowOK(solvedAt, openedAt, p.eventEnd) {
			return capOutOfWindow, nil
		}
	}
	// a WebVerse solve credits regardless of the team's LOCAL state: never-opened,
	// abandoned, expired, or an open whose act-timer lapsed all still count. ensure the
	// row is open+live (no launch charge — it lives on WebVerse, there is no H7 instance
	// to pay for) so applyEconomySolve below can score it. it then flips to 'solved' in
	// this same tx, so a re-opened challenge never lingers as an extra open slot.
	live := !noRow && status == "open" && expiresAt != nil && expiresAt.After(time.Now())
	if noRow || (status != "solved" && !live) {
		if e := ensureTeamEconomy(ctx, tx, teamID, p.econ); e != nil {
			return capNotOpen, e
		}
		exp := p.eventEnd
		if exp.IsZero() {
			exp = time.Now().Add(72 * time.Hour)
		}
		ot := time.Now()
		if solvedAt != nil {
			ot = *solvedAt
		}
		if _, e := tx.Exec(ctx,
			`INSERT INTO economy_challenge_state (team_id, challenge_id, status, opened_at, expires_at)
			 VALUES ($1, $2, 'open', $3, $4)
			 ON CONFLICT (team_id, challenge_id) DO UPDATE
			   SET status = 'open', expires_at = EXCLUDED.expires_at,
			       opened_at = COALESCE(economy_challenge_state.opened_at, EXCLUDED.opened_at)`,
			teamID, meta.challengeID, ot, exp); e != nil {
			return capNotOpen, e
		}
		if openedAt == nil {
			openedAt = &ot
		}
	}

	// write the solve row so applyEconomySolve's teamFlagFrac sees a held flag.
	solveTime := time.Now()
	if solvedAt != nil {
		solveTime = *solvedAt
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO solves (id, user_id, challenge_id, flag_id, points_awarded, solved_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (user_id, flag_id) DO NOTHING`,
		uuid.New(), userID, meta.challengeID, meta.flagID, meta.points, solveTime)
	if err != nil {
		return capNotOpen, err
	}
	newSolve := tag.RowsAffected() == 1

	// reuse the economy capture path verbatim. no wrong-sub penalty is possible here
	// (we never bump wrong_subs for these challenges). clean refund applies as normal.
	if ecErr := applyEconomySolve(ctx, tx, p.econ, teamID, meta.challengeID, meta.difficulty); ecErr != nil {
		var opErr *EconomyOpError
		if errors.As(ecErr, &opErr) {
			if opErr.Status == http.StatusConflict {
				return capNotOpen, nil // not open/live now -> rolled back, retried next poll
			}
			return capNotOpen, opErr
		}
		return capNotOpen, ecErr
	}

	if err := p.recordSolveBookkeeping(ctx, tx, meta.challengeID, meta.flagID, userID, solveTime, newSolve); err != nil {
		return capNotOpen, err
	}
	if err := tx.Commit(ctx); err != nil {
		return capNotOpen, err
	}
	return capCaptured, nil
}

// evaluateOne is the read-only dry-run twin of captureOne: it classifies what would
// happen without writing anything (mirrors applyEconomyFrac's open/live gate).
func (p *WebVersePoller) evaluateOne(ctx context.Context, teamID, challengeID uuid.UUID, solvedAt *time.Time) (captureResult, error) {
	var status string
	var openedAt, expiresAt *time.Time
	var holds bool
	var frac float64
	err := p.db.Pool.QueryRow(ctx,
		`SELECT status, opened_at, expires_at, holds_solve, frac
		 FROM economy_challenge_state WHERE team_id = $1 AND challenge_id = $2`,
		teamID, challengeID).Scan(&status, &openedAt, &expiresAt, &holds, &frac)
	noRow := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !noRow {
		return capNotOpen, err
	}
	if !noRow {
		if status == "solved" && holds && frac >= 1 {
			return capAlready, nil
		}
		if !windowOK(solvedAt, openedAt, p.eventEnd) {
			return capOutOfWindow, nil
		}
	}
	// captureOne makes any non-solved state scorable (auto-open / re-open), so it captures.
	return capCaptured, nil
}

// recordSolveBookkeeping updates the denormalized solve counts and first blood (by
// solved_at, self-correcting so an earlier WebVerse solve seen later still wins). It
// does NOT touch team/user score: applyEconomySolve is the scoring path in economy mode.
func (p *WebVersePoller) recordSolveBookkeeping(ctx context.Context, tx pgx.Tx, challengeID, flagID, userID uuid.UUID, solveTime time.Time, newSolve bool) error {
	if !newSolve {
		return nil // counts already reflect this solve
	}
	if _, err := tx.Exec(ctx,
		`UPDATE flags SET
		    total_solves = (SELECT COUNT(*) FROM solves s JOIN users u ON u.id = s.user_id
		                    WHERE s.flag_id = $1 AND u.role NOT IN ('admin', 'author')),
		    first_blood_user_id = CASE WHEN first_blood_at IS NULL OR first_blood_at > $3 THEN $2 ELSE first_blood_user_id END,
		    first_blood_at = LEAST(COALESCE(first_blood_at, $3), $3),
		    updated_at = NOW()
		 WHERE id = $1`, flagID, userID, solveTime); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE challenges SET total_solves = (
		    SELECT COUNT(*) FROM (
		        SELECT s.user_id FROM solves s JOIN flags f ON s.flag_id = f.id
		        WHERE f.challenge_id = $1
		        GROUP BY s.user_id
		        HAVING COUNT(DISTINCT s.flag_id) = (SELECT COUNT(*) FROM flags WHERE challenge_id = $1)
		    ) fully
		 ) WHERE id = $1`, challengeID); err != nil {
		return err
	}
	return nil
}

// resolveTeams maps lowercased emails to their team, EXCLUDING staff (admin/author)
// and organizer test accounts: a matched user must be an active role='user', which
// also guarantees the team is a public (non-test) team.
func (p *WebVersePoller) resolveTeams(ctx context.Context, emails []string) (map[string]teamRef, error) {
	// index EVERY eligible account by its NORMALIZED email (normEmail: lowercase,
	// trim, drop +tag, gmail dots) so a solver whose WebVerse email differs from
	// their Anvil email only by formatting still matches. the WebVerse side is keyed
	// the same way (normEmail at build time), so the two are symmetric. an ambiguous
	// normalized key (two distinct teams) is dropped — never credit the wrong team.
	if len(emails) == 0 {
		return map[string]teamRef{}, nil
	}
	rows, err := p.db.Pool.Query(ctx,
		`SELECT email, id, team_id FROM users
		 WHERE team_id IS NOT NULL AND status = 'active' AND role = 'user'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]teamRef{}
	ambiguous := map[string]struct{}{}
	for rows.Next() {
		var email string
		var uid, tid uuid.UUID
		if err := rows.Scan(&email, &uid, &tid); err != nil {
			return nil, err
		}
		k := normEmail(email)
		if k == "" {
			continue
		}
		if ex, ok := out[k]; ok && ex.teamID != tid {
			ambiguous[k] = struct{}{}
			continue
		}
		out[k] = teamRef{userID: uid, teamID: tid}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for k := range ambiguous {
		delete(out, k)
	}
	return out, nil
}

// resolveChallenges maps anvil slugs to their poller-scored challenge meta. Only
// challenges with an 'external' flag (the poller-only flag) are returned, so the
// poller can never capture an ordinary challenge.
func (p *WebVersePoller) resolveChallenges(ctx context.Context, slugs []string) (map[string]chalMeta, error) {
	out := map[string]chalMeta{}
	if len(slugs) == 0 {
		return out, nil
	}
	rows, err := p.db.Pool.Query(ctx,
		`SELECT c.slug, c.id, c.difficulty, f.id, f.points
		 FROM challenges c
		 JOIN LATERAL (
		     SELECT id, points FROM flags
		     WHERE challenge_id = c.id AND flag_type = 'external'
		     ORDER BY sort_order LIMIT 1
		 ) f ON TRUE
		 WHERE c.slug = ANY($1)`, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var m chalMeta
		if err := rows.Scan(&slug, &m.challengeID, &m.difficulty, &m.flagID, &m.points); err != nil {
			return nil, err
		}
		out[slug] = m
	}
	return out, rows.Err()
}

// fetchExport pulls and decodes the leaderboard export. The token goes in an
// Authorization header or the authtoken query param; it tries the configured
// preference and falls back on 401/403, caching whichever works. The token is never
// logged (only a token-free URL is).
func (p *WebVersePoller) fetchExport(ctx context.Context) (*wvExport, error) {
	token, err := p.token()
	if err != nil {
		return nil, err
	}
	modes := []string{"query"}
	if p.cfg.AuthHeader {
		modes = []string{"header", "query"}
	}
	if p.authMode != "" {
		modes = []string{p.authMode}
	}
	var lastErr error
	for _, mode := range modes {
		exp, status, err := p.doFetch(ctx, token, mode)
		if err == nil {
			p.authMode = mode
			return exp, nil
		}
		lastErr = err
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			break // a non-auth error won't be fixed by switching modes
		}
		p.logger.Warn("webverse: auth mode rejected, trying fallback",
			zap.String("mode", mode), zap.Int("status", status))
	}
	return nil, lastErr
}

func (p *WebVersePoller) doFetch(ctx context.Context, token, mode string) (*wvExport, int, error) {
	u, err := url.Parse(strings.TrimRight(p.cfg.BaseURL, "/") + p.cfg.ExportPath)
	if err != nil {
		return nil, 0, fmt.Errorf("bad webverse url: %w", err)
	}
	if mode == "query" {
		q := u.Query()
		q.Set("authtoken", token)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if mode == "header" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, resp.StatusCode, fmt.Errorf("webverse export %s returned %d: %s", safeURL(p.cfg.BaseURL, p.cfg.ExportPath), resp.StatusCode, snippet)
	}
	var exp wvExport
	if err := json.Unmarshal(body, &exp); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode webverse export: %w", err)
	}
	return &exp, resp.StatusCode, nil
}

func (p *WebVersePoller) token() (string, error) {
	if p.cfg.TokenFile != "" {
		b, err := os.ReadFile(p.cfg.TokenFile)
		if err != nil {
			return "", fmt.Errorf("read webverse token_file: %w", err)
		}
		t := strings.TrimSpace(string(b))
		if t == "" {
			return "", errors.New("webverse token_file is empty")
		}
		return t, nil
	}
	t := strings.TrimSpace(p.cfg.Token)
	if t == "" {
		return "", errors.New("no webverse token configured (set webverse.token or webverse.token_file)")
	}
	return t, nil
}

// ---- pure helpers (unit-tested in webverse_test.go) ----

// windowOK reports whether a WebVerse solve counts by time: after the team opened
// the challenge here and before the event ends. An unknown solvedAt defers to the
// caller's open-at-sync-time check (returns true).
func windowOK(solvedAt, openedAt *time.Time, eventEnd time.Time) bool {
	if solvedAt == nil {
		return true
	}
	if openedAt == nil {
		return false
	}
	// WebVerse solves live on WebVerse's own timeline: a team that has opened the
	// challenge on H7 gets credit whether they solved before or after opening here.
	// (external + authoritative from WebVerse's leaderboard, so no ordering cheese.)
	if !eventEnd.IsZero() && solvedAt.After(eventEnd) {
		return false
	}
	return true
}

// parseSolves reads a participant's solve list from the primary key, falling back to
// the legacy key. Each element may be a bare slug string or an object; unknown shapes
// are skipped. Duplicate slugs collapse.
func parseSolves(primary, fallback json.RawMessage) []wvSolve {
	raw := primary
	if isEmptyJSON(raw) {
		raw = fallback
	}
	if isEmptyJSON(raw) {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		if one := parseSolveElement(raw); one.slug != "" { // tolerate a single non-array value
			return []wvSolve{one}
		}
		return nil
	}
	out := make([]wvSolve, 0, len(arr))
	seen := map[string]struct{}{}
	for _, el := range arr {
		s := parseSolveElement(el)
		if s.slug == "" {
			continue
		}
		if _, dup := seen[s.slug]; dup {
			continue
		}
		seen[s.slug] = struct{}{}
		out = append(out, s)
	}
	return out
}

func parseSolveElement(el json.RawMessage) wvSolve {
	t := bytes.TrimSpace([]byte(el))
	if len(t) == 0 {
		return wvSolve{}
	}
	switch t[0] {
	case '"':
		var s string
		if err := json.Unmarshal(t, &s); err == nil {
			return wvSolve{slug: normSlug(s)}
		}
	case '{':
		var m map[string]any
		if err := json.Unmarshal(t, &m); err == nil {
			lm := lowerKeys(m)
			slug := firstString(lm, "slug", "challenge_slug", "chal_slug", "challenge", "chal", "lab", "lab_slug", "name", "key")
			at := firstTime(lm, "solved_at", "solvedat", "solved_time", "solvedtime", "solved", "timestamp", "time", "at", "date", "created_at", "createdat")
			return wvSolve{slug: normSlug(slug), solvedAt: at}
		}
	}
	return wvSolve{}
}

// isEmptyJSON treats absent/null and an empty array/object as "no data", so an empty
// primary key (the current "solves":[]) falls through to the legacy fallback key.
func isEmptyJSON(r json.RawMessage) bool {
	switch string(bytes.TrimSpace([]byte(r))) {
	case "", "null", "[]", "{}":
		return true
	}
	return false
}

func lowerKeys(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func firstTime(m map[string]any, keys ...string) *time.Time {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if t := parseFlexTime(v); t != nil {
				return t
			}
		}
	}
	return nil
}

// parseFlexTime accepts RFC3339 strings, a few loose layouts, and epoch seconds or
// milliseconds (as a number or numeric string).
func parseFlexTime(v any) *time.Time {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				u := t.UTC()
				return &u
			}
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return epochToTime(n)
		}
		return nil
	case float64:
		return epochToTime(x)
	case json.Number:
		if n, err := x.Float64(); err == nil {
			return epochToTime(n)
		}
	}
	return nil
}

func epochToTime(n float64) *time.Time {
	if n <= 0 {
		return nil
	}
	sec, nsec := int64(n), int64(0)
	if n > 1e12 { // milliseconds
		sec = int64(n) / 1000
		nsec = (int64(n) % 1000) * int64(time.Millisecond)
	}
	t := time.Unix(sec, nsec).UTC()
	return &t
}

func normSlug(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func normEmail(s string) string {
	e := strings.ToLower(strings.TrimSpace(s))
	at := strings.LastIndexByte(e, '@')
	if at <= 0 || at == len(e)-1 {
		return e
	}
	local, domain := e[:at], e[at+1:]
	// drop a +tag (sub-addressing) — same mailbox on every major provider
	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	// gmail/googlemail ignore dots in the local part and are the same domain
	if domain == "googlemail.com" {
		domain = "gmail.com"
	}
	if domain == "gmail.com" {
		local = strings.ReplaceAll(local, ".", "")
	}
	return local + "@" + domain
}

// earlier reports whether a is a strictly earlier timestamp than b, treating a known
// time as earlier than an unknown one (so first blood prefers a real solved_at).
func earlier(a, b *time.Time) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	return a.Before(*b)
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := keysOf(m)
	sort.Strings(out)
	return out
}

func capStrings(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func safeURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
