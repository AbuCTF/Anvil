package game

import (
	"strings"
	"testing"

	"github.com/anvil-lab/anvil/internal/models"
)

func TestSanitizeCheckerDiagnosticDropsSecrets(t *testing.T) {
	secrets := []string{
		"H7CTF{0123456789abcdef0123456789abcdef}",
		"eyJhbGciOiJIUzI1NiJ9.hidden.signature",
		"team-token-hidden-sentinel",
		"PRIVATE_HIDDEN_SENTINEL",
	}
	for _, secret := range secrets {
		code, message := SanitizeCheckerDiagnostic(models.SLADown, "", "BERTH-HAVOC-04: "+secret)
		if code != "BERTH-HAVOC-04" {
			t.Fatalf("code = %q, want BERTH-HAVOC-04", code)
		}
		if strings.Contains(message, secret) {
			t.Fatalf("diagnostic leaked %q in %q", secret, message)
		}
		if len(message) > maxCheckerDiagnosticMessage {
			t.Fatalf("diagnostic length = %d, want at most 160", len(message))
		}
		code, message = SanitizeCheckerDiagnostic(models.SLADown, "", secret)
		if strings.Contains(code, secret) || strings.Contains(message, secret) {
			t.Fatalf("diagnostic leaked %q as code %q or message %q", secret, code, message)
		}
	}
}

func TestSanitizeCheckerDiagnosticAcceptsOnlyStrictCodes(t *testing.T) {
	tests := []struct {
		name, code, message, want string
	}{
		{name: "explicit", code: "BERTH-HAVOC-04", want: "BERTH-HAVOC-04"},
		{name: "message prefix", message: "BERTH-HAVOC-04: hidden", want: "BERTH-HAVOC-04"},
		{name: "lowercase", code: "berth-havoc-04"},
		{name: "punctuation", code: "BERTH.HAVOC.04"},
		{name: "too long", code: "A" + strings.Repeat("B", maxCheckerDiagnosticCode)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := SanitizeCheckerDiagnostic(models.SLAFaulty, tt.code, tt.message)
			if got != tt.want {
				t.Fatalf("code = %q, want %q", got, tt.want)
			}
		})
	}
}
