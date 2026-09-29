package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	marketPulseCadence      = 5 * time.Minute
	marketPulseDelay        = 10 * time.Minute
	marketPulseTrendWindow  = 30 * time.Minute
	marketPulseMinAnonymity = 3
	marketPulseQueryTimeout = 5 * time.Second
)

type pulsePolicy struct {
	GeneratedAt       time.Time                      `json:"generated_at"`
	SourceCutoff      time.Time                      `json:"source_cutoff"`
	CadenceSeconds    int                            `json:"cadence_seconds"`
	DelaySeconds      int                            `json:"delay_seconds"`
	MinAnonymity      int                            `json:"min_anonymity"`
	FieldHidden       bool                           `json:"field_hidden"`
	FieldHiddenReason string                         `json:"field_hidden_reason,omitempty"`
	Economy           config.EconomyPolicyDescriptor `json:"economy"`
}

type pulseAffordability struct {
	Difficulty string  `json:"difficulty"`
	Cost       float64 `json:"cost"`
	Affordable bool    `json:"affordable"`
	Count      int     `json:"count"`
}

type pulseOpen struct {
	Slug             string     `json:"slug"`
	Name             string     `json:"name"`
	Difficulty       string     `json:"difficulty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	WrongSubmissions int        `json:"wrong_submissions"`
	CurrentValue     float64    `json:"current_value"`
	ExtensionsUsed   int        `json:"extensions_used"`
	NextExtension    *float64   `json:"next_extension_cost,omitempty"`
}

type pulseTeam struct {
	Credits            float64              `json:"credits"`
	Points             float64              `json:"points"`
	GrantIssued        bool                 `json:"grant_issued"`
	BailoutUsed        bool                 `json:"bailout_used"`
	OpenSlotsUsed      int                  `json:"open_slots_used"`
	OpenSlotsTotal     int                  `json:"open_slots_total"`
	P2CBlocksUsed      int                  `json:"p2c_blocks_used"`
	NextP2CRate        float64              `json:"next_p2c_rate"`
	SettlementExposure float64              `json:"settlement_exposure"`
	Affordability      []pulseAffordability `json:"affordability"`
	Open               []pulseOpen          `json:"open"`
}

type pulseFieldChallenge struct {
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Category      string     `json:"category"`
	Difficulty    string     `json:"difficulty"`
	ScoringMode   string     `json:"scoring_mode"`
	SolveBand     string     `json:"solve_band"`
	Heat          string     `json:"heat"`
	Direction     string     `json:"direction"`
	SignalQuality string     `json:"signal_quality"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
}

type pulseNotice struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Href     string `json:"href,omitempty"`
}

type pulseResponse struct {
	Policy  pulsePolicy           `json:"policy"`
	Team    pulseTeam             `json:"team"`
	Field   []pulseFieldChallenge `json:"field"`
	Notices []pulseNotice         `json:"notices"`
}

func pulseSolveBand(captures int) string {
	switch {
	case captures < marketPulseMinAnonymity:
		return "insufficient_sample"
	case captures <= 5:
		return "few"
	case captures <= 10:
		return "several"
	case captures <= 25:
		return "crowded"
	default:
		return "saturated"
	}
}

func pulseHeat(recent int) string {
	switch {
	case recent < marketPulseMinAnonymity:
		return "quiet"
	case recent <= 5:
		return "warming"
	case recent <= 10:
		return "active"
	default:
		return "hot"
	}
}

func pulseDirection(recent, previous int) string {
	if recent < marketPulseMinAnonymity && previous < marketPulseMinAnonymity {
		return "insufficient_sample"
	}
	if recent >= marketPulseMinAnonymity && float64(recent) >= math.Max(float64(marketPulseMinAnonymity), float64(previous)*1.5) {
		return "accelerating"
	}
	if previous >= marketPulseMinAnonymity && float64(previous) >= math.Max(float64(marketPulseMinAnonymity), float64(recent)*1.5) {
		return "cooling"
	}
	return "steady"
}

func nextP2CRate(blocks int, base, decay, floor float64) float64 {
	rate := base * math.Pow(decay, float64(max(blocks, 0)))
	return math.Max(floor, rate)
}

// MarketPulse returns exact state for the caller's team and delayed, bucketed
// field activity. It is deliberately read-only: the response derives from the
// existing Ledger and solve records and cannot create a second scoring truth.
func (h *EconomyHandler) MarketPulse(c *gin.Context) {
	enabled, err := boolSettingOrDefault(c.Request.Context(), h.db, "market_pulse_enabled", false)
	if err != nil {
		h.logger.Error("market pulse: read feature setting", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "market pulse unavailable"})
		return
	}
	if !enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "market pulse is not enabled"})
		return
	}

	teamID, ok := h.econContext(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), marketPulseQueryTimeout)
	defer cancel()
	now := time.Now().UTC()
	publicationTime := now.Truncate(marketPulseCadence)
	sourceCutoff := publicationTime.Add(-marketPulseDelay)
	frozen, err := boolSettingOrDefault(ctx, h.db, "scoreboard_frozen", false)
	if err != nil {
		h.logger.Error("market pulse: read freeze setting", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "market pulse unavailable"})
		return
	}

	response := pulseResponse{
		Policy: pulsePolicy{
			GeneratedAt: publicationTime, SourceCutoff: sourceCutoff,
			CadenceSeconds: int(marketPulseCadence.Seconds()),
			DelaySeconds:   int(marketPulseDelay.Seconds()),
			MinAnonymity:   marketPulseMinAnonymity,
			FieldHidden:    frozen,
			Economy:        h.config.EconomyPolicyDescriptor(),
		},
		Field:   []pulseFieldChallenge{},
		Notices: []pulseNotice{},
	}
	if frozen {
		response.Policy.FieldHiddenReason = "Field signals are unavailable while standings are blind. Your team state remains live."
	}

	team, err := h.marketPulseTeam(ctx, teamID.String(), now)
	if err != nil {
		h.logger.Error("market pulse: team state", zap.Error(err))
		status := http.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"error": "market pulse unavailable"})
		return
	}
	response.Team = team
	response.Notices = h.marketPulseNotices(team, frozen, now)

	if !frozen {
		// The public side uses only cadence-aligned cutoffs so every team sees
		// the same challenge set and field snapshot for the publication window.
		response.Field, err = h.marketPulseField(ctx, sourceCutoff, publicationTime)
		if err != nil {
			h.logger.Error("market pulse: field state", zap.Error(err))
			status := http.StatusInternalServerError
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			c.JSON(status, gin.H{"error": "market pulse unavailable"})
			return
		}
	}

	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

func (h *EconomyHandler) marketPulseTeam(ctx context.Context, teamID string, now time.Time) (pulseTeam, error) {
	team := pulseTeam{
		Credits:        h.config.Economy.Grant,
		OpenSlotsTotal: h.config.Economy.ConcurrencyCap,
		Affordability:  []pulseAffordability{},
		Open:           []pulseOpen{},
	}
	err := h.db.Pool.QueryRow(ctx,
		`SELECT credits, points, grant_issued, bailout_used, p2c_blocks
		 FROM economy_team_score WHERE team_id = $1`, teamID,
	).Scan(&team.Credits, &team.Points, &team.GrantIssued, &team.BailoutUsed, &team.P2CBlocksUsed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pulseTeam{}, err
	}

	rows, err := h.db.Pool.Query(ctx,
		`SELECT c.slug, c.name, c.difficulty::text, e.expires_at, e.wrong_subs,
		        e.current_value, e.extensions_used
		 FROM economy_challenge_state e
		 JOIN challenges c ON c.id = e.challenge_id
		 WHERE e.team_id = $1 AND e.status = 'open'
		   AND (e.expires_at IS NULL OR e.expires_at > $2)
		 ORDER BY e.expires_at NULLS LAST, c.name`, teamID, now)
	if err != nil {
		return pulseTeam{}, err
	}
	for rows.Next() {
		var open pulseOpen
		if err := rows.Scan(&open.Slug, &open.Name, &open.Difficulty, &open.ExpiresAt,
			&open.WrongSubmissions, &open.CurrentValue, &open.ExtensionsUsed); err != nil {
			rows.Close()
			return pulseTeam{}, err
		}
		if open.ExtensionsUsed < h.config.Economy.MaxExtensions && open.ExtensionsUsed < len(h.config.Economy.ExtCostFracs) {
			cost := launchCost(h.config.Economy, open.Difficulty) * h.config.Economy.ExtCostFracs[open.ExtensionsUsed]
			open.NextExtension = &cost
		}
		team.Open = append(team.Open, open)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return pulseTeam{}, err
	}
	rows.Close()

	team.OpenSlotsUsed = len(team.Open)
	team.NextP2CRate = nextP2CRate(team.P2CBlocksUsed, h.config.Economy.P2CBase,
		h.config.Economy.P2CRateDecay, h.config.Economy.P2CMinRate)
	team.SettlementExposure = team.Credits * h.config.Economy.C2PRate
	for _, difficulty := range []string{"easy", "medium", "hard", "insane"} {
		cost := launchCost(h.config.Economy, difficulty)
		count := 0
		if cost > 0 {
			count = int(math.Floor(team.Credits / cost))
		}
		team.Affordability = append(team.Affordability, pulseAffordability{
			Difficulty: difficulty, Cost: cost, Affordable: cost > 0 && team.Credits >= cost, Count: count,
		})
	}
	return team, nil
}

func (h *EconomyHandler) marketPulseField(ctx context.Context, sourceCutoff, releaseCutoff time.Time) ([]pulseFieldChallenge, error) {
	query := `
		WITH public_users AS (
			SELECT u.id, u.team_id
			FROM users u
			JOIN teams t ON t.id = u.team_id
			WHERE u.status = 'active' AND u.role NOT IN ('admin', 'author')
			  AND ` + publicTeamSQL("t") + `
		), first_capture AS (
			SELECT pu.team_id, s.challenge_id, MIN(s.solved_at) AS captured_at
			FROM solves s
			JOIN public_users pu ON pu.id = s.user_id
			WHERE s.solved_at <= $1
			GROUP BY pu.team_id, s.challenge_id
		), activity AS (
			SELECT challenge_id,
			       COUNT(*)::int AS captures,
			       COUNT(*) FILTER (WHERE captured_at > $1 - $2::interval)::int AS recent,
			       COUNT(*) FILTER (WHERE captured_at > $1 - ($2::interval * 2)
			                         AND captured_at <= $1 - $2::interval)::int AS previous
			FROM first_capture
			GROUP BY challenge_id
		)
		SELECT c.slug, c.name, COALESCE(cat.name, 'Uncategorized'), c.difficulty::text,
		       c.scoring_mode, c.release_date,
		       COALESCE(a.captures, 0), COALESCE(a.recent, 0), COALESCE(a.previous, 0)
		FROM challenges c
		LEFT JOIN categories cat ON cat.id = c.category_id
		LEFT JOIN activity a ON a.challenge_id = c.id
		WHERE c.status = 'published'
		  AND c.arena_mode = 'per_team'
		  AND (c.release_date IS NULL OR c.release_date <= $3)
		ORDER BY c.name`

	rows, err := h.db.Pool.Query(ctx, query, sourceCutoff, marketPulseTrendWindow.String(), releaseCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	field := []pulseFieldChallenge{}
	for rows.Next() {
		var challenge pulseFieldChallenge
		var captures, recent, previous int
		if err := rows.Scan(&challenge.Slug, &challenge.Name, &challenge.Category,
			&challenge.Difficulty, &challenge.ScoringMode, &challenge.ReleasedAt,
			&captures, &recent, &previous); err != nil {
			return nil, err
		}
		challenge.SignalQuality = "team_first_capture"
		if challenge.ScoringMode == "graded" {
			challenge.SignalQuality = "limited_for_graded"
		}
		challenge.SolveBand = pulseSolveBand(captures)
		challenge.Heat = pulseHeat(recent)
		challenge.Direction = pulseDirection(recent, previous)
		field = append(field, challenge)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(field, func(i, j int) bool {
		rank := map[string]int{"hot": 0, "active": 1, "warming": 2, "quiet": 3}
		if rank[field[i].Heat] != rank[field[j].Heat] {
			return rank[field[i].Heat] < rank[field[j].Heat]
		}
		return field[i].Name < field[j].Name
	})
	return field, nil
}

func (h *EconomyHandler) marketPulseNotices(team pulseTeam, frozen bool, now time.Time) []pulseNotice {
	notices := []pulseNotice{}
	if frozen {
		notices = append(notices, pulseNotice{
			Kind: "freeze", Severity: "info",
			Message: "Standings and field signals are blind. Your team state remains live while play continues.",
		})
	}
	if team.OpenSlotsTotal > 0 && team.OpenSlotsUsed >= team.OpenSlotsTotal {
		notices = append(notices, pulseNotice{
			Kind: "slots_full", Severity: "warning",
			Message: "Every challenge slot is occupied. Solve, abandon, or wait for an expiry before opening another.",
			Href:    "/team",
		})
	}
	minCost := math.Inf(1)
	for _, item := range team.Affordability {
		if item.Cost > 0 && item.Cost < minCost {
			minCost = item.Cost
		}
	}
	if !math.IsInf(minCost, 1) && team.Credits < minCost {
		message := "Your team cannot currently afford the cheapest launch. Review conversion options."
		if !team.BailoutUsed {
			message = "Your team cannot currently afford the cheapest launch. Review conversion or bailout options."
		}
		notices = append(notices, pulseNotice{
			Kind: "low_runway", Severity: "warning", Message: message, Href: "/team",
		})
	}
	for _, open := range team.Open {
		if open.ExpiresAt == nil {
			continue
		}
		remaining := open.ExpiresAt.Sub(now)
		if remaining > 0 && remaining <= 30*time.Minute {
			severity := "info"
			if remaining <= 10*time.Minute {
				severity = "warning"
			}
			notices = append(notices, pulseNotice{
				Kind: "timer", Severity: severity,
				Message: open.Name + " expires soon. Review the exact extension or abandon cost before acting.",
				Href:    "/challenges/" + open.Slug,
			})
		}
	}
	return notices
}
