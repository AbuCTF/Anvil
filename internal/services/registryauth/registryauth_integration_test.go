//go:build integration

package registryauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/google/uuid"
)

func TestCredentialStorageAndDockerAuth(t *testing.T) {
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
	adminID := uuid.New()
	adminName := "registry-admin-" + strings.ReplaceAll(adminID.String(), "-", "")[:12]
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, username, role, status) VALUES ($1, $2, 'admin', 'active')`, adminID, adminName); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM registry_credentials WHERE updated_by = $1`, adminID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, adminID)
	}()
	service, err := NewService("registry-integration-secret-0123456789", db)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	secret := "ghp_read_only_package_token"
	if err := service.Save(ctx, "ghcr.io", "AbuCTF", secret, adminID); err != nil {
		t.Fatalf("save: %v", err)
	}
	var ciphertext []byte
	if err := db.Pool.QueryRow(ctx, `SELECT secret_ciphertext FROM registry_credentials WHERE registry = 'ghcr.io'`).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte(secret)) {
		t.Fatalf("ciphertext leaked secret=%t error=%v", bytes.Contains(ciphertext, []byte(secret)), err)
	}
	credential, err := service.Get(ctx, "ghcr.io")
	if err != nil || credential == nil || credential.Username != "AbuCTF" || credential.Secret != secret {
		t.Fatalf("credential=%+v error=%v", credential, err)
	}
	encoded := service.EncodedAuth(ctx, "ghcr.io/abuctf/token-overflow:latest")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode auth: %v", err)
	}
	var auth map[string]string
	if json.Unmarshal(decoded, &auth) != nil || auth["username"] != "AbuCTF" || auth["password"] != secret || auth["serveraddress"] != "ghcr.io" {
		t.Fatalf("auth=%v", auth)
	}
	deleted, err := service.Delete(ctx, "ghcr.io")
	if err != nil || !deleted {
		t.Fatalf("deleted=%t error=%v", deleted, err)
	}
}
