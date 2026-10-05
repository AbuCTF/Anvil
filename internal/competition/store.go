package competition

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrIdempotencyConflict = errors.New("competition event idempotency conflict")

type StoredEvent struct {
	Sequence   int64
	ID         uuid.UUID
	RecordedAt time.Time
	Replayed   bool
}

func Append(ctx context.Context, tx pgx.Tx, event Event) (StoredEvent, error) {
	if tx == nil {
		return StoredEvent{}, errors.New("transaction is required")
	}
	prepared, err := Prepare(event)
	if err != nil {
		return StoredEvent{}, err
	}
	var stored StoredEvent
	err = tx.QueryRow(ctx, `
		INSERT INTO competition_events (
			id, event_slug, idempotency_key, payload_checksum, kind, stream,
			subject_type, subject_id, user_id, team_id, challenge_id, instance_id,
			delta, value_after, policy_name, policy_revision, policy_checksum,
			source, actor_id, causation_id, correlation_id, request_id,
			occurred_at, effective_at, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13::numeric, $14::numeric, $15, $16, $17,
			$18, $19, $20, $21, $22,
			$23, $24, $25::jsonb
		)
		ON CONFLICT (event_slug, idempotency_key) DO NOTHING
		RETURNING sequence, id, recorded_at
	`,
		prepared.ID, prepared.EventSlug, prepared.IdempotencyKey, prepared.PayloadChecksum,
		prepared.Kind, prepared.Stream, prepared.SubjectType, prepared.SubjectID,
		prepared.UserID, prepared.TeamID, prepared.ChallengeID, prepared.InstanceID,
		prepared.Delta.Decimal(), decimalOrNil(prepared.ValueAfter), prepared.PolicyName,
		prepared.PolicyRevision, prepared.PolicyChecksum, prepared.Source, prepared.ActorID,
		prepared.CausationID, prepared.CorrelationID, emptyAsNil(prepared.RequestID),
		prepared.OccurredAt, prepared.EffectiveAt, string(prepared.Metadata),
	).Scan(&stored.Sequence, &stored.ID, &stored.RecordedAt)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StoredEvent{}, fmt.Errorf("append competition event: %w", err)
	}

	var checksum string
	err = tx.QueryRow(ctx, `
		SELECT sequence, id, recorded_at, payload_checksum
		FROM competition_events
		WHERE event_slug = $1 AND idempotency_key = $2
	`, prepared.EventSlug, prepared.IdempotencyKey).Scan(
		&stored.Sequence, &stored.ID, &stored.RecordedAt, &checksum,
	)
	if err != nil {
		return StoredEvent{}, fmt.Errorf("read replayed competition event: %w", err)
	}
	if checksum != prepared.PayloadChecksum {
		return StoredEvent{}, fmt.Errorf("%w for %s/%s", ErrIdempotencyConflict, prepared.EventSlug, prepared.IdempotencyKey)
	}
	stored.Replayed = true
	return stored, nil
}

func decimalOrNil(amount *Amount) any {
	if amount == nil {
		return nil
	}
	return amount.Decimal()
}

func emptyAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
