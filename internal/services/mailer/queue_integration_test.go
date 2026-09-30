//go:build integration

package mailer

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func TestQueueClaimRecoveryAndProviderHalfOpen(t *testing.T) {
	name := os.Getenv("ANVIL_TEST_DB_NAME")
	if !strings.HasSuffix(name, "_test") {
		t.Skip("set ANVIL_TEST_DB_HOST/PORT/USER/PASSWORD/NAME (name ending _test)")
	}
	port, _ := strconv.Atoi(os.Getenv("ANVIL_TEST_DB_PORT"))
	db, err := database.New(config.DatabaseConfig{
		Host: os.Getenv("ANVIL_TEST_DB_HOST"), Port: port, User: os.Getenv("ANVIL_TEST_DB_USER"),
		Password: os.Getenv("ANVIL_TEST_DB_PASSWORD"), Database: name, SSLMode: "disable",
		MaxOpenConns: 6, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	service, err := NewService("queue-integration-secret-0123456789abcdef", db, zap.NewNop())
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	createdBy := uuid.New()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, role, status) VALUES ($1, $2, 'admin', 'active')`, createdBy, "queue-admin-"+createdBy.String()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_deliveries WHERE created_by = $1`, createdBy)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_providers WHERE created_by = $1`, createdBy)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, createdBy)
	}()
	deliveryID, err := service.Enqueue(ctx, "player@example.test", "welcome", map[string]string{
		"participant_name": "Player", "event_name": "Event", "login_url": "https://ctf.example.test/login",
	}, &createdBy)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimErr := service.claim(ctx)
			results <- claimErr
		}()
	}
	wg.Wait()
	close(results)
	claimed, empty := 0, 0
	for claimErr := range results {
		if claimErr == nil {
			claimed++
		} else if errors.Is(claimErr, pgx.ErrNoRows) {
			empty++
		} else {
			t.Fatalf("claim: %v", claimErr)
		}
	}
	if claimed != 1 || empty != 1 {
		t.Fatalf("claimed=%d empty=%d", claimed, empty)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE mail_deliveries SET updated_at = NOW() - INTERVAL '6 minutes' WHERE id = $1`, deliveryID); err != nil {
		t.Fatalf("age claim: %v", err)
	}
	recovered, err := service.claim(ctx)
	if err != nil || recovered.ID != deliveryID || recovered.Attempts != 1 {
		t.Fatalf("recovered=%+v error=%v", recovered, err)
	}

	ciphertext, err := service.cipher.Encrypt("provider-password")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	halfOpenID, untestedID := uuid.New(), uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO mail_providers (id, name, host, port, username, password_ciphertext, security, from_name, from_address, priority, is_active, is_healthy, failure_count, circuit_open_until, created_by)
		VALUES
		($1, 'Half open', 'smtp.example.test', 587, 'user', $3, 'starttls', 'Event', 'ctf@example.test', 1, TRUE, FALSE, 3, NOW() - INTERVAL '1 minute', $4),
		($2, 'Untested', 'smtp.example.test', 587, 'user', $3, 'starttls', 'Event', 'ctf@example.test', 2, TRUE, FALSE, 0, NULL, $4)
	`, halfOpenID, untestedID, ciphertext, createdBy); err != nil {
		t.Fatalf("seed providers: %v", err)
	}
	providers, err := service.availableProviders(ctx)
	if err != nil {
		t.Fatalf("available providers: %v", err)
	}
	if len(providers) != 1 || providers[0].ID != halfOpenID {
		t.Fatalf("available providers=%+v", providers)
	}
}
