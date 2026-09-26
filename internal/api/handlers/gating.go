package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// economyState is a team's economy_challenge_state row for one challenge
// (zero value = never opened).
type economyState struct {
	status    string
	expiresAt *time.Time
}

// VIEW: the team paid to open it once, so the card, brief, files and hints stay
// readable even after the timer ran out. abandoned/unopened are locked.
func economyCanView(st economyState) bool {
	return st.status == "open" || st.status == "solved" || st.status == "expired"
}

// ACT (submit, instance, hint unlock, extend, abandon) needs a live timer or a solve.
func economyCanAct(st economyState, now time.Time) bool {
	return st.status == "solved" || (st.status == "open" && st.expiresAt != nil && st.expiresAt.After(now))
}

// economyDenied is the 403 text for a team that can't act on a challenge right now.
func economyDenied(st economyState) string {
	if economyCanView(st) {
		return "your timer on this challenge ran out; open it again first"
	}
	return "open this challenge first"
}

// economyGate is the economy gate as it applies to one caller: economy off and
// staff (admin/author) pass everything.
type economyGate struct {
	on     bool
	staff  bool
	states map[string]economyState // challenge id -> the caller's team state
}

func (g economyGate) canView(challengeID string) bool {
	return !g.on || g.staff || economyCanView(g.states[challengeID])
}

func (g economyGate) canAct(challengeID string, now time.Time) bool {
	return !g.on || g.staff || economyCanAct(g.states[challengeID], now)
}

// anonymous and teamless callers get no states, so everything is locked.
func loadEconomyGate(c *gin.Context, db *database.DB) (economyGate, error) {
	g := economyGate{staff: isStaff(c)}
	if g.staff {
		return g, nil
	}
	ctx := c.Request.Context()
	on, err := isEconomyMode(ctx, db)
	if err != nil || !on {
		return g, err
	}
	g.on = true
	uid, ok := contextUserID(c)
	if !ok {
		return g, nil
	}
	rows, err := db.Pool.Query(ctx,
		`SELECT e.challenge_id::text, e.status, e.expires_at FROM economy_challenge_state e
		 JOIN users u ON u.team_id = e.team_id WHERE u.id = $1`, uid)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	g.states = map[string]economyState{}
	for rows.Next() {
		var id string
		var st economyState
		if err := rows.Scan(&id, &st.status, &st.expiresAt); err != nil {
			return g, err
		}
		g.states[id] = st
	}
	return g, rows.Err()
}

// rejectLocked writes the gate's 403 when the caller may not view (or, with act,
// use) the challenge yet, and reports whether it did.
func rejectLocked(c *gin.Context, db *database.DB, logger *zap.Logger, challengeID string, act bool) bool {
	gate, err := loadEconomyGate(c, db)
	if err != nil {
		logger.Error("failed to load economy gate", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "economy unavailable"})
		return true
	}
	if !gate.canView(challengeID) || (act && !gate.canAct(challengeID, time.Now())) {
		c.JSON(http.StatusForbidden, gin.H{"error": economyDenied(gate.states[challengeID])})
		return true
	}
	return false
}

// download links are plain <a href> with no auth header, so the detail view signs
// each attachment it reveals and a valid ticket stands in for the gate check.
const attachmentTicketTTL = 12 * time.Hour

func attachmentTicket(key []byte, attachmentID string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	return e + "." + attachmentTicketMAC(key, attachmentID, e)
}

func validAttachmentTicket(key []byte, attachmentID, ticket string, now time.Time) bool {
	e, sig, ok := strings.Cut(ticket, ".")
	if !ok || len(key) == 0 {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(attachmentTicketMAC(key, attachmentID, e)))
}

func attachmentTicketMAC(key []byte, attachmentID, exp string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("attachment|" + attachmentID + "|" + exp))
	return hex.EncodeToString(m.Sum(nil))
}

// organizer test teams (no active non-staff member) never show publicly.
func publicTeamSQL(alias string) string {
	// public = has an active non-staff member AND is not a zz- test/QA team. the
	// zz- prefix is our test convention (also swept by the pre-event reset); adding
	// it here keeps a QA account that plays as a real user off the live board/crowd.
	// parenthesized so `NOT ` + publicTeamSQL negates the whole predicate correctly.
	return `(EXISTS (SELECT 1 FROM users pm WHERE pm.team_id = ` + alias + `.id
		AND pm.status = 'active' AND pm.role NOT IN ('admin', 'author'))
		AND ` + alias + `.name NOT LIKE 'zz-%')`
}

// arena rows mirror real teams by id (koth native); hide mirrors of test teams.
func publicGameTeamSQL(alias string) string {
	return `NOT EXISTS (SELECT 1 FROM teams rt WHERE rt.id = ` + alias + `.id AND NOT ` + publicTeamSQL("rt") + `)`
}

// teamIsKothQA reports whether teamID is the one team allowed to enter a DRAFT KotH
// arena for pre-drop QA: the arenas stay unpublished (hidden from the field) but this
// team can buy in and play the real participant flow. Set via platform_settings
// koth_qa_team_id; empty = nobody (normal published-only gate). Cleared after QA.
func teamIsKothQA(ctx context.Context, db *database.DB, teamID string) bool {
	var v string
	if err := db.Pool.QueryRow(ctx,
		`SELECT value FROM platform_settings WHERE key = 'koth_qa_team_id'`).Scan(&v); err != nil {
		return false
	}
	return v != "" && v == teamID
}
