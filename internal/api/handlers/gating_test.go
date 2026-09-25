package handlers

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
)

func TestEconomyViewAndAct(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	live, gone := now.Add(time.Minute), now.Add(-time.Minute)
	for _, tc := range []struct {
		name       string
		st         economyState
		view, act  bool
		deniedText string
	}{
		{"never opened", economyState{}, false, false, "open this challenge first"},
		{"unopened row", economyState{status: "unopened"}, false, false, "open this challenge first"},
		{"open, live timer", economyState{"open", &live}, true, true, ""},
		{"open, timer ran out (not yet swept)", economyState{"open", &gone}, true, false, "your timer on this challenge ran out; open it again first"},
		{"open, no timer", economyState{status: "open"}, true, false, "your timer on this challenge ran out; open it again first"},
		{"expired: paid once, read-only", economyState{"expired", &gone}, true, false, "your timer on this challenge ran out; open it again first"},
		{"solved", economyState{"solved", &gone}, true, true, ""},
		{"abandoned", economyState{"abandoned", &live}, false, false, "open this challenge first"},
	} {
		if got := economyCanView(tc.st); got != tc.view {
			t.Errorf("%s: view = %v, want %v", tc.name, got, tc.view)
		}
		if got := economyCanAct(tc.st, now); got != tc.act {
			t.Errorf("%s: act = %v, want %v", tc.name, got, tc.act)
		}
		if !tc.act {
			if got := economyDenied(tc.st); got != tc.deniedText {
				t.Errorf("%s: denied = %q, want %q", tc.name, got, tc.deniedText)
			}
		}
	}
}

func TestEconomyGateWhoSeesWhat(t *testing.T) {
	now := time.Now()
	gone := now.Add(-time.Minute)
	states := map[string]economyState{"expired": {"open", &gone}, "abandoned": {status: "abandoned"}}
	for _, tc := range []struct {
		name      string
		gate      economyGate
		id        string
		view, act bool
	}{
		{"economy off", economyGate{}, "anything", true, true},
		{"player, never opened", economyGate{on: true, states: states}, "other", false, false},
		{"player, timer ran out", economyGate{on: true, states: states}, "expired", true, false},
		{"player, abandoned", economyGate{on: true, states: states}, "abandoned", false, false},
		{"teamless or anonymous", economyGate{on: true}, "expired", false, false},
		{"staff", economyGate{on: true, staff: true}, "other", true, true},
	} {
		if got := tc.gate.canView(tc.id); got != tc.view {
			t.Errorf("%s: view = %v, want %v", tc.name, got, tc.view)
		}
		if got := tc.gate.canAct(tc.id, now); got != tc.act {
			t.Errorf("%s: act = %v, want %v", tc.name, got, tc.act)
		}
	}
}

func TestLoadEconomyGateStaffSkipsDatabase(t *testing.T) {
	for _, role := range []string{"admin", "author"} {
		ctx, _ := testHandlerContext(http.MethodGet, "/api/v1/challenges")
		ctx.Set("role", role)
		gate, err := loadEconomyGate(ctx, nil) // nil db: staff must not query
		if err != nil || !gate.canView("anything") || !gate.canAct("anything", time.Now()) {
			t.Errorf("%s: gate %+v err=%v, want open", role, gate, err)
		}
	}
}

func TestRedactLockedKeepsOnlyTheCard(t *testing.T) {
	desc, teaser, cat := "full brief", "one-liner", "web"
	timeout := 30
	ch := ChallengeDetailResponse{
		ChallengeListResponse: ChallengeListResponse{
			Name: "n", Difficulty: "hard", Category: &cat, Description: &desc,
			SubDescription: &teaser, TotalSolves: 4, TotalFlags: 2,
		},
		Flags:           []FlagResponse{{Name: "rce via upload"}},
		Hints:           []HintResponse{{ID: "h"}},
		Attachments:     []AttachmentResponse{{ID: "a", URL: "https://bucket/x"}},
		ExposedPorts:    []models.ExposedPort{{}},
		InstanceTimeout: &timeout,
	}
	ch.redactLocked()
	if ch.Description != nil || len(ch.Flags) != 0 || len(ch.Hints) != 0 || len(ch.Attachments) != 0 ||
		len(ch.ExposedPorts) != 0 || ch.InstanceTimeout != nil {
		t.Fatalf("locked content survived redaction: %+v", ch)
	}
	if ch.Flags == nil || ch.Hints == nil || ch.Attachments == nil || ch.ExposedPorts == nil {
		t.Fatal("redacted lists must encode as [] not null")
	}
	if ch.Name != "n" || ch.Difficulty != "hard" || *ch.Category != "web" || *ch.SubDescription != "one-liner" ||
		ch.TotalSolves != 4 || ch.TotalFlags != 2 {
		t.Fatalf("card fields were dropped: %+v", ch.ChallengeListResponse)
	}
}

func TestAttachmentTicket(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_800_000_000, 0)
	id := "5f0c2d4e-0000-4000-8000-000000000001"
	ticket := attachmentTicket(key, id, now.Add(time.Hour))

	if !validAttachmentTicket(key, id, ticket, now) {
		t.Fatal("fresh ticket rejected")
	}
	if validAttachmentTicket(key, "5f0c2d4e-0000-4000-8000-000000000002", ticket, now) {
		t.Error("ticket accepted for another attachment")
	}
	if validAttachmentTicket(key, id, ticket, now.Add(2*time.Hour)) {
		t.Error("expired ticket accepted")
	}
	if validAttachmentTicket([]byte("another-key-another-key-another!!"), id, ticket, now) {
		t.Error("ticket accepted under a different key")
	}
	exp, sig, _ := strings.Cut(ticket, ".")
	for _, bad := range []string{"", "garbage", exp, exp + ".", "." + sig, "9999999999." + sig, exp + "." + strings.Repeat("0", len(sig))} {
		if validAttachmentTicket(key, id, bad, now) {
			t.Errorf("forged ticket %q accepted", bad)
		}
	}
	if validAttachmentTicket(nil, id, attachmentTicket(nil, id, now.Add(time.Hour)), now) {
		t.Error("an unkeyed ticket must never validate")
	}
}
