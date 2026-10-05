package competition

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const scoreProjectionName = "scores"

type ProjectionAdvance struct {
	FromSequence int64
	ToSequence   int64
	Events       int
	ScoreEvents  int
	Pending      bool
}

type ScoreProjection struct {
	EventSlug    string
	Stream       string
	SubjectType  SubjectType
	SubjectID    uuid.UUID
	Score        Amount
	LastSequence int64
}

func AdvanceScoreProjection(ctx context.Context, tx pgx.Tx, eventSlug string, batchSize int) (ProjectionAdvance, error) {
	if tx == nil {
		return ProjectionAdvance{}, errors.New("transaction is required")
	}
	if !eventSlugPattern.MatchString(eventSlug) {
		return ProjectionAdvance{}, errors.New("event slug is invalid")
	}
	if batchSize < 1 || batchSize > 10_000 {
		return ProjectionAdvance{}, errors.New("batch size must be between 1 and 10000")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO competition_projection_checkpoints (event_slug, projection, last_sequence)
		VALUES ($1, $2, 0)
		ON CONFLICT (event_slug, projection) DO NOTHING
	`, eventSlug, scoreProjectionName); err != nil {
		return ProjectionAdvance{}, fmt.Errorf("initialize score projection checkpoint: %w", err)
	}

	var checkpoint int64
	if err := tx.QueryRow(ctx, `
		SELECT last_sequence FROM competition_projection_checkpoints
		WHERE event_slug = $1 AND projection = $2
		FOR UPDATE
	`, eventSlug, scoreProjectionName).Scan(&checkpoint); err != nil {
		return ProjectionAdvance{}, fmt.Errorf("lock score projection checkpoint: %w", err)
	}

	type projectedEvent struct {
		sequence    int64
		kind        string
		stream      string
		subjectType SubjectType
		subjectID   *uuid.UUID
		delta       Amount
	}
	rows, err := tx.Query(ctx, `
		SELECT sequence, kind, stream, subject_type, subject_id,
		       (delta * 1000000)::bigint
		FROM competition_events
		WHERE event_slug = $1 AND sequence > $2
		ORDER BY sequence
		LIMIT $3
	`, eventSlug, checkpoint, batchSize)
	if err != nil {
		return ProjectionAdvance{}, fmt.Errorf("read score projection events: %w", err)
	}
	events := make([]projectedEvent, 0, batchSize)
	for rows.Next() {
		var event projectedEvent
		var delta int64
		if err := rows.Scan(&event.sequence, &event.kind, &event.stream, &event.subjectType, &event.subjectID, &delta); err != nil {
			rows.Close()
			return ProjectionAdvance{}, fmt.Errorf("scan score projection event: %w", err)
		}
		event.delta = Amount(delta)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ProjectionAdvance{}, fmt.Errorf("iterate score projection events: %w", err)
	}
	rows.Close()

	advance := ProjectionAdvance{FromSequence: checkpoint, ToSequence: checkpoint, Events: len(events)}
	for _, event := range events {
		advance.ToSequence = event.sequence
		if event.kind != "score.awarded" && event.kind != "score.adjusted" && event.kind != "score.revoked" {
			continue
		}
		if event.subjectID == nil || (event.subjectType != SubjectUser && event.subjectType != SubjectTeam) {
			return ProjectionAdvance{}, fmt.Errorf("score event %d has an invalid subject", event.sequence)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO competition_score_projections (
				event_slug, stream, subject_type, subject_id, score, last_sequence, updated_at
			) VALUES ($1, $2, $3, $4, $5::numeric, $6, NOW())
			ON CONFLICT (event_slug, stream, subject_type, subject_id) DO UPDATE
			SET score = competition_score_projections.score + EXCLUDED.score,
			    last_sequence = EXCLUDED.last_sequence,
			    updated_at = NOW()
		`, eventSlug, event.stream, event.subjectType, *event.subjectID, event.delta.Decimal(), event.sequence); err != nil {
			return ProjectionAdvance{}, fmt.Errorf("apply score projection event %d: %w", event.sequence, err)
		}
		advance.ScoreEvents++
	}

	if advance.ToSequence != checkpoint {
		if _, err := tx.Exec(ctx, `
			UPDATE competition_projection_checkpoints
			SET last_sequence = $3, updated_at = NOW()
			WHERE event_slug = $1 AND projection = $2
		`, eventSlug, scoreProjectionName, advance.ToSequence); err != nil {
			return ProjectionAdvance{}, fmt.Errorf("advance score projection checkpoint: %w", err)
		}
	}
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM competition_events
			WHERE event_slug = $1 AND sequence > $2
		)
	`, eventSlug, advance.ToSequence).Scan(&advance.Pending); err != nil {
		return ProjectionAdvance{}, fmt.Errorf("inspect score projection backlog: %w", err)
	}
	return advance, nil
}

func ReadScoreProjection(ctx context.Context, tx pgx.Tx, eventSlug, stream string, subjectType SubjectType, subjectID uuid.UUID) (ScoreProjection, error) {
	if tx == nil {
		return ScoreProjection{}, errors.New("transaction is required")
	}
	var projection ScoreProjection
	var score int64
	err := tx.QueryRow(ctx, `
		SELECT event_slug, stream, subject_type, subject_id,
		       (score * 1000000)::bigint, last_sequence
		FROM competition_score_projections
		WHERE event_slug = $1 AND stream = $2 AND subject_type = $3 AND subject_id = $4
	`, eventSlug, stream, subjectType, subjectID).Scan(
		&projection.EventSlug, &projection.Stream, &projection.SubjectType,
		&projection.SubjectID, &score, &projection.LastSequence,
	)
	projection.Score = Amount(score)
	return projection, err
}
