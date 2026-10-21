package game

import (
	"context"
	"fmt"
	"sort"

	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/models"
	"github.com/google/uuid"
)

// SLATick is one team's verdict for one service on one tick, before and after
// organizer credit. Effective ERROR means the tick was credited (ORG_FAULT).
type SLATick struct {
	ServiceID   uuid.UUID        `json:"service_id"`
	Tick        int              `json:"tick"`
	Status      models.SLAStatus `json:"status"`
	Effective   models.SLAStatus `json:"effective_status"`
	Credited    string           `json:"credited_reason,omitempty"`
	Step        string           `json:"step,omitempty"`
	Code        string           `json:"code,omitempty"`
	Message     string           `json:"message,omitempty"`
	ImageDigest string           `json:"image_digest,omitempty"`
}

// EffectiveSLA returns a team's per-tick verdicts from `since` on, with the same
// credit rules the scorer applies, so the status page and the board never disagree.
func EffectiveSLA(ctx context.Context, db *database.DB, teamID uuid.UUID, since, massFault int) ([]SLATick, error) {
	in := &mirrorInput{
		Teams:      map[uuid.UUID]bool{},
		SLA:        map[slaKey]slaTick{},
		BotCrashed: map[slaKey]bool{},
		MassFault:  massFault,
	}
	rows, err := db.Pool.Query(ctx, `SELECT id, is_nop FROM game_teams WHERE status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("load teams: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var nop bool
		if err := rows.Scan(&id, &nop); err != nil {
			rows.Close()
			return nil, err
		}
		in.Teams[id] = nop
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type extra struct{ code, message, digest string }
	mine := map[slaKey]extra{}
	rows, err = db.Pool.Query(ctx,
		`SELECT team_id, service_id, tick_number, status, COALESCE(step, ''), COALESCE(code, ''),
		        COALESCE(message, ''), COALESCE(image_digest, '')
		 FROM game_sla_checks WHERE tick_number >= $1`, since)
	if err != nil {
		return nil, fmt.Errorf("load sla: %w", err)
	}
	for rows.Next() {
		var k slaKey
		var st, step, code, msg, digest string
		if err := rows.Scan(&k.team, &k.service, &k.tick, &st, &step, &code, &msg, &digest); err != nil {
			rows.Close()
			return nil, err
		}
		in.SLA[k] = slaTick{Status: models.SLAStatus(st), Step: step}
		if k.team == teamID {
			mine[k] = extra{code, msg, digest}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := loadCredits(ctx, db.Pool, in); err != nil {
		return nil, err
	}
	rows, err = db.Pool.Query(ctx,
		`SELECT f.team_id, f.service_id, b.tick_number FROM game_bot_reads b
		 JOIN game_flags f ON f.id = b.flag_id
		 WHERE b.outcome = 'crashed' AND b.tick_number >= $1`, since)
	if err != nil {
		return nil, fmt.Errorf("load bot crashes: %w", err)
	}
	for rows.Next() {
		var k slaKey
		if err := rows.Scan(&k.team, &k.service, &k.tick); err != nil {
			rows.Close()
			return nil, err
		}
		in.BotCrashed[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	eff, why := effectiveSLA(in)
	out := make([]SLATick, 0, len(mine))
	for k, x := range mine {
		raw := in.SLA[k]
		t := SLATick{
			ServiceID: k.service, Tick: k.tick, Status: raw.Status, Effective: eff[k],
			Credited: why[k], Step: raw.Step, Code: x.code, Message: x.message, ImageDigest: x.digest,
		}
		if raw.Status == models.SLAError {
			// a checker's own error text may carry private detail; teams only see it was ours
			t.Message, t.Code = "checker error, credited", ""
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tick != out[j].Tick {
			return out[i].Tick < out[j].Tick
		}
		return out[i].ServiceID.String() < out[j].ServiceID.String()
	})
	return out, nil
}
