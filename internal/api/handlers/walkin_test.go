package handlers

import (
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

func stateHandler(secret string) *AuthHandler {
	cfg := &config.Config{}
	cfg.JWT.Secret = secret
	return &AuthHandler{config: cfg}
}

func TestOAuthState(t *testing.T) {
	h := stateHandler("test-secret-please-change-32bytes-min")

	state, err := h.signOAuthState()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !h.verifyOAuthState(state) {
		t.Fatal("fresh state should verify")
	}

	// tampered token must fail
	if h.verifyOAuthState(state + "x") {
		t.Fatal("tampered state must not verify")
	}

	// state signed with a different secret must fail
	if h.verifyOAuthState(mustState(t, "another-secret", time.Now().Add(time.Minute), "discord_oauth")) {
		t.Fatal("foreign-secret state must not verify")
	}

	// expired state must fail
	if h.verifyOAuthState(mustState(t, "test-secret-please-change-32bytes-min", time.Now().Add(-time.Minute), "discord_oauth")) {
		t.Fatal("expired state must not verify")
	}

	// wrong subject must fail
	if h.verifyOAuthState(mustState(t, "test-secret-please-change-32bytes-min", time.Now().Add(time.Minute), "not_discord")) {
		t.Fatal("wrong-subject state must not verify")
	}
}

func mustState(t *testing.T, secret string, exp time.Time, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   sub,
		ExpiresAt: jwt.NewNumericDate(exp),
	})
	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("mustState: %v", err)
	}
	return s
}
