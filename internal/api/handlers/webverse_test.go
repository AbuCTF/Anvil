package handlers

import (
	"encoding/json"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad time %q: %v", s, err)
	}
	return tt.UTC()
}

// the real API today: "solves":[] (nobody solved yet) -> nothing parsed.
func TestParseSolvesEmpty(t *testing.T) {
	if got := parseSolves(json.RawMessage(`[]`), nil); len(got) != 0 {
		t.Fatalf("empty array: got %v", got)
	}
	if got := parseSolves(json.RawMessage(`null`), json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null: got %v", got)
	}
	if got := parseSolves(nil, nil); len(got) != 0 {
		t.Fatalf("nil: got %v", got)
	}
}

// bare slug strings: ["flux","splice"].
func TestParseSolvesBareStrings(t *testing.T) {
	got := parseSolves(json.RawMessage(`["Flux","SPLICE"," merged "]`), nil)
	want := []string{"flux", "splice", "merged"}
	if len(got) != len(want) {
		t.Fatalf("got %d solves, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].slug != w {
			t.Errorf("solve[%d].slug = %q, want %q", i, got[i].slug, w)
		}
		if got[i].solvedAt != nil {
			t.Errorf("solve[%d].solvedAt should be nil for a bare string", i)
		}
	}
}

// object elements with slug + solved_at, and the near-future shape variants.
func TestParseSolvesObjects(t *testing.T) {
	raw := json.RawMessage(`[
		{"slug":"flux","solved_at":"2026-09-26T10:00:00Z"},
		{"challenge":"splice","solvedAt":"2026-09-26T11:30:00Z"},
		{"challenge_slug":"merged","timestamp":1758880800},
		{"lab":"justified"}
	]`)
	got := parseSolves(raw, nil)
	if len(got) != 4 {
		t.Fatalf("got %d, want 4: %v", len(got), got)
	}
	bySlug := map[string]wvSolve{}
	for _, s := range got {
		bySlug[s.slug] = s
	}
	if s, ok := bySlug["flux"]; !ok || s.solvedAt == nil || !s.solvedAt.Equal(mustTime(t, "2026-09-26T10:00:00Z")) {
		t.Errorf("flux solved_at wrong: %+v", s)
	}
	if s, ok := bySlug["splice"]; !ok || s.solvedAt == nil || !s.solvedAt.Equal(mustTime(t, "2026-09-26T11:30:00Z")) {
		t.Errorf("splice solvedAt wrong: %+v", s)
	}
	if s, ok := bySlug["merged"]; !ok || s.solvedAt == nil { // epoch seconds
		t.Errorf("merged epoch time not parsed: %+v", s)
	}
	if s, ok := bySlug["justified"]; !ok || s.solvedAt != nil {
		t.Errorf("justified should have no solved_at: %+v", s)
	}
}

// legacy fallback key: solved_slugs when solves is absent/empty.
func TestParseSolvesFallbackKey(t *testing.T) {
	got := parseSolves(json.RawMessage(`[]`), json.RawMessage(`["flux"]`))
	if len(got) != 1 || got[0].slug != "flux" {
		t.Fatalf("fallback solved_slugs not used: %v", got)
	}
	// primary present wins over fallback
	got = parseSolves(json.RawMessage(`["splice"]`), json.RawMessage(`["flux"]`))
	if len(got) != 1 || got[0].slug != "splice" {
		t.Fatalf("primary should win: %v", got)
	}
}

func TestParseSolvesGarbageAndDedup(t *testing.T) {
	got := parseSolves(json.RawMessage(`["flux", 123, null, {"nope":1}, "flux", {"slug":""}]`), nil)
	if len(got) != 1 || got[0].slug != "flux" {
		t.Fatalf("garbage/dedup handling wrong: %v", got)
	}
}

func TestParseSolvesNonArray(t *testing.T) {
	if got := parseSolves(json.RawMessage(`"flux"`), nil); len(got) != 1 || got[0].slug != "flux" {
		t.Fatalf("single bare string: %v", got)
	}
	if got := parseSolves(json.RawMessage(`{"slug":"flux"}`), nil); len(got) != 1 || got[0].slug != "flux" {
		t.Fatalf("single object: %v", got)
	}
}

func TestParseFlexTime(t *testing.T) {
	if tm := parseFlexTime("2026-09-26T10:00:00Z"); tm == nil || !tm.Equal(mustTime(t, "2026-09-26T10:00:00Z")) {
		t.Errorf("rfc3339 failed: %v", tm)
	}
	if tm := parseFlexTime(float64(1758880800)); tm == nil { // epoch seconds
		t.Errorf("epoch seconds failed")
	}
	if tm := parseFlexTime(float64(1758880800000)); tm == nil || tm.Year() != 2025 && tm.Year() != 2026 {
		t.Errorf("epoch millis failed: %v", tm)
	}
	if tm := parseFlexTime(""); tm != nil {
		t.Errorf("empty string should be nil")
	}
	if tm := parseFlexTime("not-a-time"); tm != nil {
		t.Errorf("garbage should be nil")
	}
}

func TestWindowOK(t *testing.T) {
	opened := mustTime(t, "2026-09-26T10:00:00Z")
	end := mustTime(t, "2026-09-27T15:30:00Z")
	solvedIn := mustTime(t, "2026-09-26T12:00:00Z")
	solvedBeforeOpen := mustTime(t, "2026-09-26T09:00:00Z")
	solvedAfterEnd := mustTime(t, "2026-09-28T00:00:00Z")

	cases := []struct {
		name     string
		solvedAt *time.Time
		openedAt *time.Time
		want     bool
	}{
		{"unknown solved_at defers to caller", nil, &opened, true},
		{"in window", &solvedIn, &opened, true},
		{"never opened here", &solvedIn, nil, false},
		{"solved before opening here still credits (order-free since v0.291)", &solvedBeforeOpen, &opened, true},
		{"solved after event end", &solvedAfterEnd, &opened, false},
	}
	for _, tc := range cases {
		if got := windowOK(tc.solvedAt, tc.openedAt, end); got != tc.want {
			t.Errorf("%s: windowOK = %v, want %v", tc.name, got, tc.want)
		}
	}
	// no configured event end (zero) => no upper bound.
	if !windowOK(&solvedAfterEnd, &opened, time.Time{}) {
		t.Errorf("zero event end should impose no upper bound")
	}
}

func TestEarlier(t *testing.T) {
	a := mustTime(t, "2026-09-26T10:00:00Z")
	b := mustTime(t, "2026-09-26T11:00:00Z")
	if !earlier(&a, &b) {
		t.Errorf("a<b should be earlier")
	}
	if earlier(&b, &a) {
		t.Errorf("b<a should be false")
	}
	if !earlier(&a, nil) {
		t.Errorf("known time is earlier than unknown")
	}
	if earlier(nil, &a) {
		t.Errorf("unknown is not earlier than known")
	}
	if earlier(nil, nil) {
		t.Errorf("nil,nil should be false")
	}
}

// the full real-API envelope decodes and drives the mapping.
func TestExportEnvelopeDecode(t *testing.T) {
	body := `{
      "event": {"name":"H7TEX 2026 CTF", "slug":"YfQq-BkjAX1I9bECP5U9xcsY", "status":"active"},
      "generated_at": "2026-09-26T00:08:32.063789Z",
      "participants": [
        {"username":"Coookie","email":"cookiectf@gmail.com","points":0,"solves":[]},
        {"username":"abu2win","email":"ABURAHMAN918@gmail.com","points":300,"solves":["flux",{"slug":"splice","solved_at":"2026-09-26T12:00:00Z"}]}
      ]
    }`
	var exp wvExport
	if err := json.Unmarshal([]byte(body), &exp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if exp.Event.Slug != "YfQq-BkjAX1I9bECP5U9xcsY" || exp.Event.Status != "active" {
		t.Fatalf("event fields wrong: %+v", exp.Event)
	}
	if len(exp.Participants) != 2 {
		t.Fatalf("want 2 participants, got %d", len(exp.Participants))
	}
	// email normalizes lowercase; solves parse to two entries.
	if normEmail(exp.Participants[1].Email) != "aburahman918@gmail.com" {
		t.Errorf("email normalization: %q", normEmail(exp.Participants[1].Email))
	}
	solves := parseSolves(exp.Participants[1].Solves, exp.Participants[1].SolvedSlugs)
	if len(solves) != 2 {
		t.Fatalf("want 2 solves, got %d: %v", len(solves), solves)
	}
}
