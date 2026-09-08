package game

import (
	"regexp"
	"strings"
	"testing"
)

func TestMintFlagFormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^H7CTF\{[A-Z2-7]+\}$`)
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		f := mintFlag("H7CTF")
		if !re.MatchString(f) {
			t.Fatalf("flag %q does not match expected format", f)
		}
		if seen[f] {
			t.Fatalf("duplicate flag minted: %q", f)
		}
		seen[f] = true
	}
}

func TestMintFlagPrefix(t *testing.T) {
	if f := mintFlag("ANVIL"); !strings.HasPrefix(f, "ANVIL{") {
		t.Fatalf("expected ANVIL prefix, got %q", f)
	}
}
