package game

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type emitStanding struct {
	TeamID  string  `json:"team_id"`
	Team    string  `json:"team"`
	Attack  float64 `json:"attack"`
	Defense float64 `json:"defense"`
	SLA     float64 `json:"sla"`
	Koth    float64 `json:"koth"`
	Total   float64 `json:"total"`
	Rank    *int    `json:"rank,omitempty"`
}

// emitStandings posts the current signed standings to the configured webhook.
func (c *Controller) emitStandings(ctx context.Context, tick int) {
	if !c.cfg.Webhook.Enabled || c.cfg.Webhook.URL == "" {
		return
	}

	rows, err := c.db.Pool.Query(ctx,
		`SELECT t.id, t.name, s.attack, s.defense, s.sla, s.koth, s.total, s.rank
		 FROM game_standings s JOIN game_teams t ON t.id = s.team_id
		 WHERE t.is_nop = false
		   -- skip mirrors of organizer test teams (no active non-staff member)
		   AND NOT EXISTS (SELECT 1 FROM teams rt WHERE rt.id = t.id AND NOT EXISTS (
		       SELECT 1 FROM users pm WHERE pm.team_id = rt.id
		         AND pm.status = 'active' AND pm.role NOT IN ('admin', 'author')))
		 ORDER BY s.rank ASC NULLS LAST`)
	if err != nil {
		c.logger.Warn("emit: query standings", zap.Error(err))
		return
	}
	standings := []emitStanding{}
	for rows.Next() {
		var e emitStanding
		var id uuid.UUID
		if rows.Scan(&id, &e.Team, &e.Attack, &e.Defense, &e.SLA, &e.Koth, &e.Total, &e.Rank) == nil {
			e.TeamID = id.String()
			standings = append(standings, e)
		}
	}
	rows.Close()

	body, err := json.Marshal(map[string]any{"tick": tick, "standings": standings})
	if err != nil {
		c.logger.Warn("emit: marshal", zap.Error(err))
		return
	}
	if err := c.postSigned(ctx, body); err != nil {
		c.logger.Warn("emit: post", zap.Error(err))
	}
}

// postSigned signs the body with HMAC-SHA256 over "{timestamp}.{id}.{body}"
// (rCTF's dynamic-scoring scheme) and POSTs it.
func (c *Controller) postSigned(ctx context.Context, body []byte) error {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(c.cfg.Webhook.Secret))
	fmt.Fprintf(mac, "%s.%s.%s", ts, c.cfg.Webhook.ID, body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RCTF-Timestamp", ts)
	req.Header.Set("X-RCTF-Signature", sig)

	resp, err := c.emitClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}
