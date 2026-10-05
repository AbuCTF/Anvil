//go:build integration

package handlers

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
)

func TestDynamicChallengeRepricing(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 4, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	teamA, teamB := uuid.New(), uuid.New()
	userA, userB, userC := uuid.New(), uuid.New(), uuid.New()
	challengeID, flagA, flagB := uuid.New(), uuid.New(), uuid.New()
	insert := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM challenges WHERE id = $1`, challengeID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{userA, userB, userC})
		_, _ = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = ANY($1)`, []uuid.UUID{teamA, teamB})
	}()

	insert(`INSERT INTO teams (id, name, join_code, total_score) VALUES ($1, $2, $3, 520), ($4, $5, $6, 300)`,
		teamA, "dynamic-a-"+teamA.String(), "a-"+teamA.String(), teamB, "dynamic-b-"+teamB.String(), "b-"+teamB.String())
	insert(`INSERT INTO users (id, username, email, role, status, team_id, total_score) VALUES
		($1, $2, $3, 'user', 'active', $4, 510),
		($5, $6, $7, 'user', 'active', $4, 200),
		($8, $9, $10, 'user', 'active', $11, 300)`,
		userA, "dynamic-a1-"+userA.String(), userA.String()+"@example.test", teamA,
		userB, "dynamic-a2-"+userB.String(), userB.String()+"@example.test",
		userC, "dynamic-b1-"+userC.String(), userC.String()+"@example.test", teamB)
	insert(`INSERT INTO challenges (id, name, slug, difficulty, status, container_image, base_points, score_type, score_minimum, score_decay)
		VALUES ($1, $2, $3, 'medium', 'published', '', 500, 'dynamic', 100, 20)`,
		challengeID, "Dynamic "+challengeID.String(), "dynamic-"+challengeID.String())
	insert(`INSERT INTO flags (id, challenge_id, name, flag_hash, points, sort_order) VALUES
		($1, $3, 'First', 'a', 200, 1), ($2, $3, 'Second', 'b', 300, 2)`, flagA, flagB, challengeID)
	insert(`INSERT INTO solves (user_id, challenge_id, flag_id, points_awarded, solved_at) VALUES
		($1, $4, $5, 200, NOW() - INTERVAL '5 minutes'),
		($1, $4, $6, 300, NOW() - INTERVAL '4 minutes'),
		($2, $4, $5, 200, NOW() - INTERVAL '3 minutes'),
		($3, $4, $6, 300, NOW() - INTERVAL '2 minutes')`, userA, userB, userC, challengeID, flagA, flagB)

	apply := func() {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := repriceDynamicChallenge(ctx, tx, challengeID.String(), 400, true); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("reprice: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	apply()
	apply()

	assertScore := func(table string, id uuid.UUID, want int) {
		var got int
		if err := db.Pool.QueryRow(ctx, `SELECT total_score FROM `+table+` WHERE id = $1`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("%s %s score=%d error=%v, want %d", table, id, got, err, want)
		}
	}
	assertScore("users", userA, 410)
	assertScore("users", userB, 160)
	assertScore("users", userC, 240)
	assertScore("teams", teamA, 420)
	assertScore("teams", teamB, 240)

	var first, second int
	if err := db.Pool.QueryRow(ctx, `SELECT
		MIN(points_awarded) FILTER (WHERE flag_id = $1),
		MIN(points_awarded) FILTER (WHERE flag_id = $2)
		FROM solves WHERE challenge_id = $3`, flagA, flagB, challengeID).Scan(&first, &second); err != nil {
		t.Fatalf("load repriced solves: %v", err)
	}
	if first != 160 || second != 240 {
		t.Fatalf("repriced solves=%d/%d, want 160/240", first, second)
	}
}
