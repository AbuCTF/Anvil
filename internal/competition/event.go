package competition

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	AmountScale Amount = 1_000_000
	MaxAmount   Amount = 999_999_999_999_999_999
)

var (
	eventSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,118}[a-z0-9])?$`)
	namePattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,118}[a-z0-9])?$`)
)

type Amount int64

type SubjectType string

const (
	SubjectUser   SubjectType = "user"
	SubjectTeam   SubjectType = "team"
	SubjectSystem SubjectType = "system"
)

type Event struct {
	ID             uuid.UUID
	EventSlug      string
	IdempotencyKey string
	Kind           string
	Stream         string
	SubjectType    SubjectType
	SubjectID      *uuid.UUID
	UserID         *uuid.UUID
	TeamID         *uuid.UUID
	ChallengeID    *uuid.UUID
	InstanceID     *uuid.UUID
	Delta          Amount
	ValueAfter     *Amount
	PolicyName     string
	PolicyRevision int
	PolicyChecksum string
	Source         string
	ActorID        *uuid.UUID
	CausationID    *uuid.UUID
	CorrelationID  uuid.UUID
	RequestID      string
	OccurredAt     time.Time
	EffectiveAt    time.Time
	Metadata       json.RawMessage
}

type PreparedEvent struct {
	Event
	PayloadChecksum string
}

func Prepare(event Event) (PreparedEvent, error) {
	event.EventSlug = strings.TrimSpace(event.EventSlug)
	event.IdempotencyKey = strings.TrimSpace(event.IdempotencyKey)
	event.Kind = strings.TrimSpace(event.Kind)
	event.Stream = strings.TrimSpace(event.Stream)
	event.PolicyName = strings.TrimSpace(event.PolicyName)
	event.PolicyChecksum = strings.TrimSpace(event.PolicyChecksum)
	event.Source = strings.TrimSpace(event.Source)
	event.RequestID = strings.TrimSpace(event.RequestID)

	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if !eventSlugPattern.MatchString(event.EventSlug) {
		return PreparedEvent{}, errors.New("event slug is invalid")
	}
	if event.IdempotencyKey == "" || len(event.IdempotencyKey) > 255 {
		return PreparedEvent{}, errors.New("idempotency key must contain 1 to 255 characters")
	}
	for field, value := range map[string]string{
		"kind": event.Kind, "stream": event.Stream, "policy name": event.PolicyName, "source": event.Source,
	} {
		if !namePattern.MatchString(value) {
			return PreparedEvent{}, fmt.Errorf("%s is invalid", field)
		}
	}
	if event.SubjectType != SubjectUser && event.SubjectType != SubjectTeam && event.SubjectType != SubjectSystem {
		return PreparedEvent{}, errors.New("subject type is invalid")
	}
	if event.SubjectType != SubjectSystem && nilUUID(event.SubjectID) {
		return PreparedEvent{}, errors.New("user and team subjects require a subject id")
	}
	if event.SubjectType == SubjectSystem && !nilUUID(event.SubjectID) {
		return PreparedEvent{}, errors.New("system subjects cannot have a subject id")
	}
	for field, value := range map[string]*uuid.UUID{
		"user id": event.UserID, "team id": event.TeamID, "challenge id": event.ChallengeID,
		"instance id": event.InstanceID, "actor id": event.ActorID, "causation id": event.CausationID,
	} {
		if value != nil && *value == uuid.Nil {
			return PreparedEvent{}, fmt.Errorf("%s cannot be a nil UUID", field)
		}
	}
	if event.PolicyRevision < 1 {
		return PreparedEvent{}, errors.New("policy revision must be positive")
	}
	if event.PolicyChecksum == "" || len(event.PolicyChecksum) > 128 {
		return PreparedEvent{}, errors.New("policy checksum must contain 1 to 128 characters")
	}
	if event.CorrelationID == uuid.Nil {
		return PreparedEvent{}, errors.New("correlation id is required")
	}
	if event.OccurredAt.IsZero() || event.EffectiveAt.IsZero() {
		return PreparedEvent{}, errors.New("occurred and effective times are required")
	}
	if len(event.RequestID) > 255 {
		return PreparedEvent{}, errors.New("request id cannot exceed 255 characters")
	}
	if event.Delta < -MaxAmount || event.Delta > MaxAmount {
		return PreparedEvent{}, errors.New("delta exceeds numeric storage range")
	}
	if event.ValueAfter != nil && (*event.ValueAfter < -MaxAmount || *event.ValueAfter > MaxAmount) {
		return PreparedEvent{}, errors.New("value after exceeds numeric storage range")
	}

	metadata, err := canonicalMetadata(event.Metadata)
	if err != nil {
		return PreparedEvent{}, err
	}
	event.Metadata = metadata

	payload, err := json.Marshal(struct {
		EventSlug      string          `json:"event_slug"`
		IdempotencyKey string          `json:"idempotency_key"`
		Kind           string          `json:"kind"`
		Stream         string          `json:"stream"`
		SubjectType    SubjectType     `json:"subject_type"`
		SubjectID      *uuid.UUID      `json:"subject_id,omitempty"`
		UserID         *uuid.UUID      `json:"user_id,omitempty"`
		TeamID         *uuid.UUID      `json:"team_id,omitempty"`
		ChallengeID    *uuid.UUID      `json:"challenge_id,omitempty"`
		InstanceID     *uuid.UUID      `json:"instance_id,omitempty"`
		Delta          Amount          `json:"delta_micros"`
		ValueAfter     *Amount         `json:"value_after_micros,omitempty"`
		PolicyName     string          `json:"policy_name"`
		PolicyRevision int             `json:"policy_revision"`
		PolicyChecksum string          `json:"policy_checksum"`
		Source         string          `json:"source"`
		ActorID        *uuid.UUID      `json:"actor_id,omitempty"`
		CausationID    *uuid.UUID      `json:"causation_id,omitempty"`
		CorrelationID  uuid.UUID       `json:"correlation_id"`
		RequestID      string          `json:"request_id,omitempty"`
		OccurredAt     time.Time       `json:"occurred_at"`
		EffectiveAt    time.Time       `json:"effective_at"`
		Metadata       json.RawMessage `json:"metadata"`
	}{
		EventSlug: event.EventSlug, IdempotencyKey: event.IdempotencyKey,
		Kind: event.Kind, Stream: event.Stream, SubjectType: event.SubjectType,
		SubjectID: event.SubjectID, UserID: event.UserID, TeamID: event.TeamID,
		ChallengeID: event.ChallengeID, InstanceID: event.InstanceID,
		Delta: event.Delta, ValueAfter: event.ValueAfter,
		PolicyName: event.PolicyName, PolicyRevision: event.PolicyRevision,
		PolicyChecksum: event.PolicyChecksum, Source: event.Source,
		ActorID: event.ActorID, CausationID: event.CausationID,
		CorrelationID: event.CorrelationID, RequestID: event.RequestID,
		OccurredAt: event.OccurredAt.UTC(), EffectiveAt: event.EffectiveAt.UTC(),
		Metadata: event.Metadata,
	})
	if err != nil {
		return PreparedEvent{}, fmt.Errorf("encode event payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return PreparedEvent{Event: event, PayloadChecksum: hex.EncodeToString(sum[:])}, nil
}

func (amount Amount) Decimal() string {
	sign := ""
	value := int64(amount)
	magnitude := uint64(value)
	if value < 0 {
		sign = "-"
		magnitude = uint64(-(value + 1)) + 1
	}
	return fmt.Sprintf("%s%d.%06d", sign, magnitude/uint64(AmountScale), magnitude%uint64(AmountScale))
}

func nilUUID(value *uuid.UUID) bool {
	return value == nil || *value == uuid.Nil
}

func canonicalMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > 64*1024 {
		return nil, errors.New("metadata cannot exceed 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("metadata must be a JSON object")
	}
	if value == nil {
		return nil, errors.New("metadata must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("metadata must contain one JSON object")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("metadata must be a JSON object")
	}
	if len(canonical) > 64*1024 {
		return nil, errors.New("metadata cannot exceed 64 KiB")
	}
	return canonical, nil
}
