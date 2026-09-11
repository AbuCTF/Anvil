package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateRejectsUnsafeProductionJWTSecrets(t *testing.T) {
	for _, secret := range []string{"", "   ", "change-me", defaultJWTSecret, "short-custom-secret"} {
		cfg := Config{Environment: "production"}
		cfg.JWT.Secret = secret
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() accepted unsafe production JWT secret %q", secret)
		}
	}
}

func TestValidateAllowsConfiguredProductionJWTSecret(t *testing.T) {
	cfg := Config{Environment: "production"}
	cfg.JWT.Secret = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected configured production JWT secret: %v", err)
	}
}

func TestValidateAllowsDevelopmentDefaultJWTSecret(t *testing.T) {
	cfg := Config{Environment: "development"}
	cfg.JWT.Secret = defaultJWTSecret
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected development JWT secret: %v", err)
	}
}

func TestLoadWithoutConfigUsesValidDurations(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if cfg.JWT.RefreshExpiry != 168*time.Hour {
		t.Fatalf("JWT refresh expiry = %s, want 168h", cfg.JWT.RefreshExpiry)
	}
}

func TestLoadEnvironmentOverridesConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
environment: production
server:
  port: 8080
database:
  host: postgres
  port: 5432
jwt:
  secret: from-config
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("ANVIL_ENVIRONMENT", "development")
	t.Setenv("ANVIL_SERVER_PORT", "18080")
	t.Setenv("ANVIL_DATABASE_HOST", "127.0.0.1")
	t.Setenv("ANVIL_DATABASE_PORT", "55432")
	t.Setenv("ANVIL_JWT_SECRET", "from-environment")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}
	if cfg.Environment != "development" {
		t.Errorf("environment = %q, want development", cfg.Environment)
	}
	if cfg.Server.Port != 18080 {
		t.Errorf("server port = %d, want 18080", cfg.Server.Port)
	}
	if cfg.Database.Host != "127.0.0.1" || cfg.Database.Port != 55432 {
		t.Errorf("database address = %s:%d, want 127.0.0.1:55432", cfg.Database.Host, cfg.Database.Port)
	}
	if cfg.JWT.Secret != "from-environment" {
		t.Errorf("JWT secret = %q, want from-environment", cfg.JWT.Secret)
	}
}
