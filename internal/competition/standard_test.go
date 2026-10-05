package competition

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func standardCapture() StandardFlagCapture {
	teamID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	return StandardFlagCapture{
		EventSlug:   "test-event",
		SolveID:     uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		AttemptID:   uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		UserID:      uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		TeamID:      &teamID,
		ChallengeID: uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		FlagID:      uuid.MustParse("66666666-6666-4666-8666-666666666666"),
		Points:      250,
		TeamsMode:   true,
		TeamAwarded: true,
		RequestID:   "request-1",
		OccurredAt:  time.Date(2026, time.October, 5, 17, 0, 0, 0, time.UTC),
	}
}

func TestStandardFlagEventsMirrorUserAndTeamAwards(t *testing.T) {
	events, err := StandardFlagEvents(standardCapture())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Kind != "flag.captured" || events[0].Delta != 0 {
		t.Fatalf("capture event = %#v", events[0])
	}
	if events[1].Stream != "standard-user-score" || events[1].Delta != 250*AmountScale {
		t.Fatalf("user award = %#v", events[1])
	}
	if events[2].Stream != "standard-team-score" || events[2].SubjectType != SubjectTeam || events[2].Delta != 250*AmountScale {
		t.Fatalf("team award = %#v", events[2])
	}
	for _, event := range events {
		if _, err := Prepare(event); err != nil {
			t.Fatalf("prepare %s: %v", event.Stream, err)
		}
	}
}

func TestStandardFlagEventsOmitDuplicateTeamAward(t *testing.T) {
	input := standardCapture()
	input.TeamAwarded = false
	events, err := StandardFlagEvents(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
}

func TestStandardFlagEventsValidateInput(t *testing.T) {
	input := standardCapture()
	input.TeamID = nil
	if _, err := StandardFlagEvents(input); err == nil {
		t.Fatal("expected team identity error")
	}
	input = standardCapture()
	input.Points = -1
	if _, err := StandardFlagEvents(input); err == nil {
		t.Fatal("expected points error")
	}
}
