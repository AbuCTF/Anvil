//go:build integration

package handlers

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestFinalizeProviderTestDelivery(t *testing.T) {
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
	adminID, providerID, deliveryID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, email, role, status) VALUES ($1, $2, $3, 'admin', 'active')`, adminID, "mail-admin-"+adminID.String(), adminID.String()+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_deliveries WHERE id = $1`, deliveryID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM mail_providers WHERE id = $1`, providerID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, adminID)
	}()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO mail_providers (id, name, host, port, password_ciphertext, security, from_name, from_address, created_by, updated_by) VALUES ($1, 'test', 'smtp.example.test', 587, 'secret', 'starttls', 'Anvil', 'mail@example.test', $2, $2)`, providerID, adminID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO mail_deliveries (id, recipient, status, attempts, max_attempts, provider_id, provider_name, created_by) VALUES ($1, 'mail-test@example.test', 'sending', 1, 1, $2, 'test', $3)`, deliveryID, providerID, adminID); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}
	handler := NewMailHandler(&config.Config{JWT: config.JWTConfig{Secret: "mail-integration-secret-0123456789abcdef"}}, db, zap.NewNop())
	if err := handler.finalizeProviderTestDelivery(ctx, deliveryID, "sent", "", "message-id"); err != nil {
		t.Fatalf("finalize delivery: %v", err)
	}
	var status string
	var sentAt *time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT status, sent_at FROM mail_deliveries WHERE id = $1`, deliveryID).Scan(&status, &sentAt); err != nil {
		t.Fatalf("load delivery: %v", err)
	}
	if status != "sent" || sentAt == nil {
		t.Fatalf("delivery status=%q sent_at=%v", status, sentAt)
	}
}
