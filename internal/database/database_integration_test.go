//go:build integration

package database

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
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

	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1`); err != nil {
		t.Fatalf("tamper with migration fingerprint: %v", err)
	}
	if err := db.Migrate(); err == nil || !strings.Contains(err.Error(), "migration 1 checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}
