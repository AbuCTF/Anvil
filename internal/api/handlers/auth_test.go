package handlers

import "testing"

func TestHashTokenUsesFullTokenSHA256(t *testing.T) {
	const token = "refresh-token-value"
	const want = "e65009f6e0ae9fc204adcb73a208f63c582b7839bde7fe836da36a3df6ae7a85"

	if got := hashToken(token); got != want {
		t.Fatalf("hashToken() = %q, want %q", got, want)
	}
}

func TestHashTokenIncludesSuffixBeyondFirst32Characters(t *testing.T) {
	const prefix = "0123456789abcdef0123456789abcdef"
	if hashToken(prefix+"a") == hashToken(prefix+"b") {
		t.Fatal("hashToken() ignored token content after the first 32 characters")
	}
}

func TestIsRegistrationMode(t *testing.T) {
	for _, mode := range []string{"open", "invite", "token", "disabled"} {
		if !isRegistrationMode(mode) {
			t.Errorf("expected %q to be a valid registration mode", mode)
		}
	}
	for _, mode := range []string{"", "enabled", "public", "OPEN"} {
		if isRegistrationMode(mode) {
			t.Errorf("expected %q to be rejected", mode)
		}
	}
}

func TestGenerateSecureToken(t *testing.T) {
	token, err := generateSecureToken(32)
	if err != nil {
		t.Fatalf("generateSecureToken: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("generated token length = %d, want 64 hex characters", len(token))
	}
	if _, err := generateSecureToken(0); err == nil {
		t.Fatal("expected zero-length token request to fail")
	}
}
