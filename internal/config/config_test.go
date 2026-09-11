package config

import "testing"

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
