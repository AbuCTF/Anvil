package handlers

import (
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

// economy gate: with the economy on, a non-staff caller sees only a challenge's
// card until their team opens it. abandoned/expired count as closed again.
func economyLocked(economyOn, staff bool, status string) bool {
	return economyOn && !staff && status != "open" && status != "solved"
}

// economyGate is the economy gate as it applies to one caller.
type economyGate struct {
	on       bool
	staff    bool
	statuses map[string]string // challenge id -> the caller's team status
}

func (g economyGate) locked(challengeID string) bool {
	return economyLocked(g.on, g.staff, g.statuses[challengeID])
}

// anonymous and teamless callers get no statuses, so everything is locked.
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
		`SELECT e.challenge_id::text, e.status FROM economy_challenge_state e
		 JOIN users u ON u.team_id = e.team_id WHERE u.id = $1`, uid)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	g.statuses = map[string]string{}
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return g, err
		}
		g.statuses[id] = status
	}
	return g, rows.Err()
}

// rejectLocked writes the gate's response when the caller may not use the challenge yet.
func rejectLocked(c *gin.Context, db *database.DB, logger *zap.Logger, challengeID string) bool {
	gate, err := loadEconomyGate(c, db)
	if err != nil {
		logger.Error("failed to load economy gate", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "economy unavailable"})
		return true
	}
	if gate.locked(challengeID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "open this challenge first"})
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
	return `EXISTS (SELECT 1 FROM users pm WHERE pm.team_id = ` + alias + `.id
		AND pm.status = 'active' AND pm.role NOT IN ('admin', 'author'))`
}

// arena rows mirror real teams by id (koth native); hide mirrors of test teams.
func publicGameTeamSQL(alias string) string {
	return `NOT EXISTS (SELECT 1 FROM teams rt WHERE rt.id = ` + alias + `.id AND NOT ` + publicTeamSQL("rt") + `)`
}
