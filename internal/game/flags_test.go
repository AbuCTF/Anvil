package game

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestMintFlagFormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^H7CTF\{[0-9a-f]{32}\}$`)
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

func TestMintFlagPanicsWhenEntropyUnavailable(t *testing.T) {
	original := flagRandRead
	flagRandRead = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	defer func() {
		flagRandRead = original
		if recover() == nil {
			t.Fatal("mintFlag did not panic when entropy failed")
		}
	}()
	mintFlag("H7CTF")
}

func TestGateFlagFormat(t *testing.T) {
	re := regexp.MustCompile(`^H7CTF\{[0-9a-f]{32}\}$`)
	if flag := gateFlag("H7CTF", 1, 0); !re.MatchString(flag) {
		t.Fatalf("gate flag %q does not match expected format", flag)
	}
}

func TestMintFlagPrefix(t *testing.T) {
	if f := mintFlag("ANVIL"); !strings.HasPrefix(f, "ANVIL{") {
		t.Fatalf("expected ANVIL prefix, got %q", f)
	}
}
