package database

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationLockKey int64 = 0x416e76696c

type migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// DB wraps the database connection pool
type DB struct {
	Pool *pgxpool.Pool
}

// New creates a new database connection
func New(cfg config.DatabaseConfig) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Connection pool settings for better performance
	poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	poolConfig.MinConns = int32(cfg.MaxIdleConns)
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{Pool: pool}, nil
}

// Close closes the database connection
func (db *DB) Close() {
	db.Pool.Close()
}

// Migrate runs database migrations
func (db *DB) Migrate() error {
	ctx := context.Background()
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("failed to lock migrations: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	// Create migrations table if not exists
	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT,
			checksum TEXT,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	if _, err := conn.Exec(ctx, `
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS name TEXT;
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT;
	`); err != nil {
		return fmt.Errorf("failed to update migrations table: %w", err)
	}

	migrations, err := readMigrations(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	type appliedMigration struct {
		name     *string
		checksum *string
	}
	applied := make(map[int]appliedMigration)
	rows, err := conn.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("failed to read applied migrations: %w", err)
	}
	for rows.Next() {
		var version int
		var item appliedMigration
		if err := rows.Scan(&version, &item.name, &item.checksum); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan applied migration: %w", err)
		}
		applied[version] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("failed while reading applied migrations: %w", err)
	}
	rows.Close()

	known := make(map[int]migration, len(migrations))
	for _, item := range migrations {
		known[item.Version] = item
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("database contains unknown migration version %d", version)
		}
	}
	foundGap := false
	for _, item := range migrations {
		_, isApplied := applied[item.Version]
		if !isApplied {
			foundGap = true
			continue
		}
		if foundGap {
			return fmt.Errorf("database migration history is not contiguous before version %d", item.Version)
		}
	}

	for _, item := range migrations {
		if existing, ok := applied[item.Version]; ok {
			if existing.name != nil && *existing.name != item.Name {
				return fmt.Errorf("migration %d was renamed from %q to %q", item.Version, *existing.name, item.Name)
			}
			if existing.checksum != nil && *existing.checksum != item.Checksum {
				return fmt.Errorf("migration %d checksum mismatch", item.Version)
			}
			// Older Anvil releases recorded only the version. Pin the current
			// embedded name and checksum the first time the upgraded runner sees it.
			if existing.name == nil || existing.checksum == nil {
				if _, err := conn.Exec(ctx,
					`UPDATE schema_migrations SET name = $1, checksum = $2 WHERE version = $3`,
					item.Name, item.Checksum, item.Version); err != nil {
					return fmt.Errorf("failed to fingerprint migration %d: %w", item.Version, err)
				}
			}
			continue
		}

		// Execute migration in transaction
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin transaction for migration %d: %w", item.Version, err)
		}

		if _, err := tx.Exec(ctx, item.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("failed to execute migration %d: %w", item.Version, err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
			item.Version, item.Name, item.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("failed to record migration %d: %w", item.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", item.Version, err)
		}
	}

	return nil
}

func readMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []migration
	versions := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(entry.Name(), ".sql"), "_", 2)
		if len(parts) != 2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		content, err := fs.ReadFile(fsys, dir+"/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(content)
		migrations = append(migrations, migration{
			Version:  version,
			Name:     entry.Name(),
			SQL:      string(content),
			Checksum: hex.EncodeToString(digest[:]),
		})
		versions[version] = entry.Name()
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for index, item := range migrations {
		expected := index + 1
		if item.Version != expected {
			return nil, fmt.Errorf("missing migration version %d before %q", expected, item.Name)
		}
	}
	return migrations, nil
}
