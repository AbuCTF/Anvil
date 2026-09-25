package handlers

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/models"
)

func TestEconomyLocked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		on     bool
		staff  bool
		status string
		want   bool
	}{
		{"economy off shows everything", false, false, "", false},
		{"never opened", true, false, "", true},
		{"unopened row", true, false, "unopened", true},
		{"open", true, false, "open", false},
		{"solved stays visible", true, false, "solved", false},
		{"abandoned closes it again", true, false, "abandoned", true},
		{"expired closes it again", true, false, "expired", true},
		{"staff bypass", true, true, "", false},
		{"staff bypass abandoned", true, true, "abandoned", false},
	} {
		if got := economyLocked(tc.on, tc.staff, tc.status); got != tc.want {
			t.Errorf("%s: economyLocked(%v, %v, %q) = %v, want %v", tc.name, tc.on, tc.staff, tc.status, got, tc.want)
		}
	}
}

func TestEconomyGateByChallenge(t *testing.T) {
	gate := economyGate{on: true, statuses: map[string]string{"a": "open", "b": "abandoned", "c": "solved"}}
	for id, want := range map[string]bool{"a": false, "b": true, "c": false, "unknown": true} {
		if got := gate.locked(id); got != want {
			t.Errorf("locked(%q) = %v, want %v", id, got, want)
		}
	}
	// teamless / anonymous: no statuses at all
	if !(economyGate{on: true}).locked("a") {
		t.Error("a caller with no team must see everything locked")
	}
	if (economyGate{}).locked("a") {
		t.Error("economy off must not lock")
	}
}

func TestLoadEconomyGateStaffSkipsDatabase(t *testing.T) {
	for _, role := range []string{"admin", "author"} {
		ctx, _ := testHandlerContext(http.MethodGet, "/api/v1/challenges")
		ctx.Set("role", role)
		gate, err := loadEconomyGate(ctx, nil) // nil db: staff must not query
		if err != nil || gate.locked("anything") {
			t.Errorf("%s: gate locked=%v err=%v, want open", role, gate.locked("anything"), err)
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
