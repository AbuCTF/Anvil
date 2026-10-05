//go:build integration

package database

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/competition"
	"github.com/anvil-lab/anvil/internal/config"
	"github.com/google/uuid"
)

func TestMigrateIsRepeatableAndFingerprintsEveryMigration(t *testing.T) {
	if os.Getenv("ANVIL_DATABASE_INTEGRATION") != "1" {
		t.Skip("set ANVIL_DATABASE_INTEGRATION=1 to run database migration integration tests")
	}
	databaseName := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("ANVIL_TEST_DB_NAME must end in _test, got %q", databaseName)
	}
	port, err := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	if err != nil {
		t.Fatalf("invalid ANVIL_TEST_DB_PORT: %v", err)
	}

	db, err := New(config.DatabaseConfig{
		Host:         os.Getenv("ANVIL_TEST_DB_HOST"),
		Port:         port,
		User:         os.Getenv("ANVIL_TEST_DB_USER"),
		Password:     os.Getenv("ANVIL_TEST_DB_PASSWORD"),
		Database:     databaseName,
		SSLMode:      "disable",
		MaxOpenConns: 4,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("first migration run: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		ALTER TABLE schema_migrations DROP COLUMN name;
		ALTER TABLE schema_migrations DROP COLUMN checksum;
	`); err != nil {
		t.Fatalf("simulate legacy migration history: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("legacy history upgrade: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("repeat migration run: %v", err)
	}

	plan, err := readMigrations(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var count, incomplete int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE name IS NULL OR checksum IS NULL)
		FROM schema_migrations
	`).Scan(&count, &incomplete); err != nil {
		t.Fatalf("inspect migration history: %v", err)
	}
	if count != len(plan) || incomplete != 0 {
		t.Fatalf("migration history count=%d incomplete=%d, want count=%d incomplete=0", count, incomplete, len(plan))
	}

	tx, err := db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin competition event transaction: %v", err)
	}
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	event := competition.Event{
		EventSlug: "integration", IdempotencyKey: "store:1", Kind: "flag.captured",
		Stream: "jeopardy", SubjectType: competition.SubjectSystem,
		Delta: 100 * competition.AmountScale, PolicyName: "static", PolicyRevision: 1,
		PolicyChecksum: "sha256:test", Source: "integration-test",
		CorrelationID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		OccurredAt:    now, EffectiveAt: now,
	}
	first, err := competition.Append(context.Background(), tx, event)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("append competition event through store: %v", err)
	}
	replayed, err := competition.Append(context.Background(), tx, event)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("replay competition event: %v", err)
	}
	if !replayed.Replayed || replayed.ID != first.ID || replayed.Sequence != first.Sequence {
		_ = tx.Rollback(context.Background())
		t.Fatalf("replayed event = %#v, first = %#v", replayed, first)
	}
	changed := event
	changed.Delta++
	if _, err := competition.Append(context.Background(), tx, changed); !errors.Is(err, competition.ErrIdempotencyConflict) {
		_ = tx.Rollback(context.Background())
		t.Fatalf("expected semantic idempotency conflict, got %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit competition event transaction: %v", err)
	}

	var eventID string
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO competition_events (
			event_slug, idempotency_key, payload_checksum, kind, stream, subject_type,
			delta, policy_name, policy_revision, policy_checksum, source,
			correlation_id, occurred_at, effective_at
		) VALUES (
			'integration', 'solve:1', repeat('a', 64), 'flag.captured', 'jeopardy', 'system',
			100, 'static', 1, 'sha256:test', 'integration-test',
			uuid_generate_v4(), NOW(), NOW()
		) RETURNING id
	`).Scan(&eventID); err != nil {
		t.Fatalf("insert competition event: %v", err)
	}
	if eventID == "" {
		t.Fatal("competition event id is empty")
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO competition_events (
			event_slug, idempotency_key, payload_checksum, kind, stream, subject_type,
			delta, policy_name, policy_revision, policy_checksum, source,
			correlation_id, occurred_at, effective_at
		) VALUES (
			'integration', 'solve:1', repeat('a', 64), 'flag.captured', 'jeopardy', 'system',
			100, 'static', 1, 'sha256:test', 'integration-test',
			uuid_generate_v4(), NOW(), NOW()
		)
	`); err == nil {
		t.Fatal("duplicate competition event was accepted")
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE competition_events SET delta = 1 WHERE id = $1`, eventID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected append-only update error, got %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `DELETE FROM competition_events WHERE id = $1`, eventID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected append-only delete error, got %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO competition_projection_checkpoints (event_slug, projection, last_sequence)
		VALUES ('integration', 'shadow-scoreboard', 1)
		ON CONFLICT (event_slug, projection) DO UPDATE
		SET last_sequence = EXCLUDED.last_sequence, updated_at = NOW()
	`); err != nil {
		t.Fatalf("upsert competition projection checkpoint: %v", err)
	}

	projectionSubject := uuid.MustParse("77777777-7777-4777-8777-777777777777")
	projectionTx, err := db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin projection fixture transaction: %v", err)
	}
	projectionBase := competition.Event{
		EventSlug: "projection", Kind: "flag.captured", Stream: "jeopardy",
		SubjectType: competition.SubjectSystem, Delta: 0,
		PolicyName: "static", PolicyRevision: 1, PolicyChecksum: "sha256:test",
		Source: "integration-test", CorrelationID: uuid.New(), OccurredAt: now, EffectiveAt: now,
	}
	projectionBase.IdempotencyKey = "capture:1"
	if _, err := competition.Append(context.Background(), projectionTx, projectionBase); err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("append projection capture: %v", err)
	}
	award := projectionBase
	award.ID = uuid.Nil
	award.IdempotencyKey = "score:1"
	award.Kind = "score.awarded"
	award.Stream = "standard-user-score"
	award.SubjectType = competition.SubjectUser
	award.SubjectID = &projectionSubject
	award.UserID = &projectionSubject
	award.Delta = 100 * competition.AmountScale
	if _, err := competition.Append(context.Background(), projectionTx, award); err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("append projected score: %v", err)
	}
	adjustment := award
	adjustment.ID = uuid.Nil
	adjustment.IdempotencyKey = "score:2"
	adjustment.Kind = "score.adjusted"
	adjustment.Delta = -25 * competition.AmountScale
	if _, err := competition.Append(context.Background(), projectionTx, adjustment); err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("append projected adjustment: %v", err)
	}
	if err := projectionTx.Commit(context.Background()); err != nil {
		t.Fatalf("commit projection fixtures: %v", err)
	}

	projectionTx, err = db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin first projection advance: %v", err)
	}
	firstAdvance, err := competition.AdvanceScoreProjection(context.Background(), projectionTx, "projection", 2)
	if err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("first projection advance: %v", err)
	}
	if firstAdvance.Events != 2 || firstAdvance.ScoreEvents != 1 || !firstAdvance.Pending {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("first projection advance = %#v", firstAdvance)
	}
	if err := projectionTx.Commit(context.Background()); err != nil {
		t.Fatalf("commit first projection advance: %v", err)
	}

	projectionTx, err = db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin resumed projection advance: %v", err)
	}
	resumed, err := competition.AdvanceScoreProjection(context.Background(), projectionTx, "projection", 2)
	if err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("resumed projection advance: %v", err)
	}
	projected, err := competition.ReadScoreProjection(context.Background(), projectionTx, "projection", "standard-user-score", competition.SubjectUser, projectionSubject)
	if err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("read score projection: %v", err)
	}
	if resumed.Events != 1 || resumed.ScoreEvents != 1 || resumed.Pending || projected.Score != 75*competition.AmountScale {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("resumed=%#v projected=%#v", resumed, projected)
	}
	if err := projectionTx.Commit(context.Background()); err != nil {
		t.Fatalf("commit resumed projection advance: %v", err)
	}

	projectionTx, err = db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin idempotent projection advance: %v", err)
	}
	idle, err := competition.AdvanceScoreProjection(context.Background(), projectionTx, "projection", 100)
	if err != nil {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("idempotent projection advance: %v", err)
	}
	if idle.Events != 0 || idle.ScoreEvents != 0 || idle.Pending {
		_ = projectionTx.Rollback(context.Background())
		t.Fatalf("idle projection advance = %#v", idle)
	}
	if err := projectionTx.Commit(context.Background()); err != nil {
		t.Fatalf("commit idempotent projection advance: %v", err)
	}

	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1`); err != nil {
		t.Fatalf("tamper with migration fingerprint: %v", err)
	}
	if err := db.Migrate(); err == nil || !strings.Contains(err.Error(), "migration 1 checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}
