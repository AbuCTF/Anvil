package game

import (
	"testing"
)

func TestGenerateTeamToken(t *testing.T) {
	tok, err := GenerateTeamToken()
	if err != nil {
		t.Fatalf("GenerateTeamToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length = %d, want 64", len(tok))
	}
	// two calls should produce distinct tokens
	tok2, _ := GenerateTeamToken()
	if tok == tok2 {
		t.Error("two consecutive tokens are identical")
	}
}

func TestSubmitExtras(t *testing.T) {
	if SubmitNop == SubmitNotLive {
		t.Error("SubmitNop and SubmitNotLive must be distinct constants")
	}
	if string(SubmitNop) != "nop" {
		t.Errorf("SubmitNop = %q, want \"nop\"", SubmitNop)
	}
	if string(SubmitNotLive) != "not_live" {
		t.Errorf("SubmitNotLive = %q, want \"not_live\"", SubmitNotLive)
	}
}
