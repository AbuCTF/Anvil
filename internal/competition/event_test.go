package competition

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validEvent() Event {
	now := time.Date(2026, time.October, 5, 12, 30, 0, 0, time.FixedZone("IST", 5*60*60+30*60))
	subject := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	return Event{
		EventSlug:      "anvil-demo",
		IdempotencyKey: "solve:22222222-2222-4222-8222-222222222222",
		Kind:           "flag.captured",
		Stream:         "jeopardy",
		SubjectType:    SubjectTeam,
		SubjectID:      &subject,
		TeamID:         &subject,
		Delta:          100 * AmountScale,
		PolicyName:     "static",
		PolicyRevision: 1,
		PolicyChecksum: "sha256:policy",
		Source:         "flag-submission",
		CorrelationID:  uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		OccurredAt:     now,
		EffectiveAt:    now,
		Metadata:       json.RawMessage(`{"flag":"first","weight":1}`),
	}
}

func TestPrepareIsDeterministic(t *testing.T) {
	first := validEvent()
	second := validEvent()
	second.Metadata = json.RawMessage(`{ "weight": 1, "flag": "first" }`)

	preparedFirst, err := Prepare(first)
	if err != nil {
		t.Fatal(err)
	}
	preparedSecond, err := Prepare(second)
	if err != nil {
		t.Fatal(err)
	}
	if preparedFirst.PayloadChecksum != preparedSecond.PayloadChecksum {
		t.Fatalf("equivalent events have different checksums: %s != %s", preparedFirst.PayloadChecksum, preparedSecond.PayloadChecksum)
	}
	if preparedFirst.ID == uuid.Nil || preparedSecond.ID == uuid.Nil {
		t.Fatal("event ids were not generated")
	}
	if string(preparedFirst.Metadata) != `{"flag":"first","weight":1}` {
		t.Fatalf("metadata = %s", preparedFirst.Metadata)
	}
}

func TestPrepareDetectsSemanticChanges(t *testing.T) {
	first, err := Prepare(validEvent())
	if err != nil {
		t.Fatal(err)
	}
	changed := validEvent()
	changed.Delta++
	second, err := Prepare(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.PayloadChecksum == second.PayloadChecksum {
		t.Fatal("changed event retained the same checksum")
	}
}

func TestPrepareRejectsInvalidEvents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Event)
	}{
		{"slug", func(event *Event) { event.EventSlug = "Bad Slug" }},
		{"idempotency", func(event *Event) { event.IdempotencyKey = "" }},
		{"kind", func(event *Event) { event.Kind = "Flag Captured" }},
		{"subject type", func(event *Event) { event.SubjectType = "other" }},
		{"subject id", func(event *Event) { event.SubjectID = nil }},
		{"system subject", func(event *Event) { event.SubjectType = SubjectSystem }},
		{"policy revision", func(event *Event) { event.PolicyRevision = 0 }},
		{"correlation", func(event *Event) { event.CorrelationID = uuid.Nil }},
		{"times", func(event *Event) { event.OccurredAt = time.Time{} }},
		{"delta", func(event *Event) { event.Delta = MaxAmount + 1 }},
		{"metadata", func(event *Event) { event.Metadata = json.RawMessage(`[]`) }},
		{"metadata size", func(event *Event) {
			event.Metadata = json.RawMessage(`{"value":"` + strings.Repeat("x", 64*1024) + `"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validEvent()
			test.mutate(&event)
			if _, err := Prepare(event); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAmountDecimal(t *testing.T) {
	for amount, want := range map[Amount]string{
		0:                   "0.000000",
		1:                   "0.000001",
		100 * AmountScale:   "100.000000",
		-12*AmountScale - 3: "-12.000003",
		-1 << 63:            "-9223372036854.775808",
	} {
		if got := amount.Decimal(); got != want {
			t.Errorf("%d = %s, want %s", amount, got, want)
		}
	}
}
