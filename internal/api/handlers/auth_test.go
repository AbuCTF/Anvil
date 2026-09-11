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
