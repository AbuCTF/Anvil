package competition

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anvil-lab/anvil/internal/scoring"
	"github.com/google/uuid"
)

type StandardFlagCapture struct {
	EventSlug   string
	SolveID     uuid.UUID
	AttemptID   uuid.UUID
	UserID      uuid.UUID
	TeamID      *uuid.UUID
	ChallengeID uuid.UUID
	FlagID      uuid.UUID
	Points      int
	TeamsMode   bool
	TeamAwarded bool
	RequestID   string
	OccurredAt  time.Time
}

func StandardFlagEvents(input StandardFlagCapture) ([]Event, error) {
	if input.SolveID == uuid.Nil || input.AttemptID == uuid.Nil || input.UserID == uuid.Nil || input.ChallengeID == uuid.Nil || input.FlagID == uuid.Nil {
		return nil, errors.New("solve, attempt, user, challenge and flag identities are required")
	}
	if input.Points < 0 || input.Points > 1_000_000 {
		return nil, errors.New("points must be between 0 and 1000000")
	}
	if input.TeamsMode && input.TeamAwarded && nilUUID(input.TeamID) {
		return nil, errors.New("a team award requires a team id")
	}
	if input.OccurredAt.IsZero() {
		return nil, errors.New("occurred time is required")
	}
	definition := scoring.Definition{Provider: "static", Revision: 1}
	policyChecksum, err := scoring.Builtins().Fingerprint(definition)
	if err != nil {
		return nil, fmt.Errorf("fingerprint static scoring policy: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"flag_id":    input.FlagID,
		"points":     input.Points,
		"teams_mode": input.TeamsMode,
	})
	if err != nil {
		return nil, fmt.Errorf("encode standard flag metadata: %w", err)
	}
	base := Event{
		EventSlug: input.EventSlug, ChallengeID: &input.ChallengeID,
		UserID: &input.UserID, TeamID: input.TeamID,
		PolicyName: definition.Provider, PolicyRevision: definition.Revision,
		PolicyChecksum: policyChecksum, Source: "flag-submission",
		ActorID: &input.UserID, CausationID: &input.AttemptID,
		CorrelationID: input.SolveID, RequestID: input.RequestID,
		OccurredAt: input.OccurredAt, EffectiveAt: input.OccurredAt,
		Metadata: metadata,
	}
	capture := base
	capture.IdempotencyKey = "flag-capture:" + input.SolveID.String()
	capture.Kind = "flag.captured"
	capture.Stream = "jeopardy"
	capture.SubjectType = SubjectUser
	capture.SubjectID = &input.UserID

	userAward := base
	userAward.IdempotencyKey = "user-score:" + input.SolveID.String()
	userAward.Kind = "score.awarded"
	userAward.Stream = "standard-user-score"
	userAward.SubjectType = SubjectUser
	userAward.SubjectID = &input.UserID
	userAward.Delta = Amount(input.Points) * AmountScale

	events := []Event{capture, userAward}
	if input.TeamsMode && input.TeamAwarded {
		teamAward := base
		teamAward.IdempotencyKey = "team-score:" + input.SolveID.String()
		teamAward.Kind = "score.awarded"
		teamAward.Stream = "standard-team-score"
		teamAward.SubjectType = SubjectTeam
		teamAward.SubjectID = input.TeamID
		teamAward.Delta = Amount(input.Points) * AmountScale
		events = append(events, teamAward)
	}
	return events, nil
}
