package handlers

import (
	"testing"
	"time"
)

func TestValidatePlatformSetting(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   interface{}
		wantErr bool
	}{
		{name: "instance limit", key: "instance.max_per_user", value: float64(5)},
		{name: "fractional instance limit", key: "instance.max_per_user", value: 1.5, wantErr: true},
		{name: "zero instance limit", key: "instance.max_per_user", value: float64(0), wantErr: true},
		{name: "extension duration", key: "instance.extension_minutes", value: float64(30)},
		{name: "oversized extension", key: "instance.extension_minutes", value: float64(1441), wantErr: true},
		{name: "scoreboard boolean", key: "scoreboard_enabled", value: true},
		{name: "scoreboard string", key: "scoreboard_enabled", value: "true", wantErr: true},
		{name: "market pulse boolean", key: "market_pulse_enabled", value: true},
		{name: "market pulse string", key: "market_pulse_enabled", value: "true", wantErr: true},
		{name: "registration mode", key: "registration_mode", value: "invite"},
		{name: "bad registration mode", key: "registration_mode", value: "yes", wantErr: true},
		{name: "event timestamp", key: "event.start_at", value: "2026-09-13T12:30:00+05:30"},
		{name: "empty event timestamp", key: "event.end_at", value: ""},
		{name: "event timestamp wrong type", key: "event.start_at", value: float64(42), wantErr: true},
		{name: "invalid event timestamp", key: "event.end_at", value: "tomorrow", wantErr: true},
		{name: "score history cutoff", key: "scoreboard.history_end_at", value: "2026-09-27T18:30:00Z"},
		{name: "empty score history cutoff", key: "scoreboard.history_end_at", value: ""},
		{name: "invalid score history cutoff", key: "scoreboard.history_end_at", value: "midnight", wantErr: true},
		{name: "legacy setting remains writable", key: "platform_name", value: "Anvil"},
		{name: "empty event name", key: "platform_name", value: "", wantErr: true},
		{name: "event slug", key: "event.slug", value: "kpmg-cyber-challenge-2026"},
		{name: "uppercase event slug", key: "event.slug", value: "KPMG-2026", wantErr: true},
		{name: "edge hyphen event slug", key: "event.slug", value: "-kpmg", wantErr: true},
		{name: "event timezone", key: "event.timezone", value: "Asia/Kolkata"},
		{name: "bad event timezone", key: "event.timezone", value: "India/Somewhere", wantErr: true},
		{name: "event contact", key: "event.contact_email", value: "ctf@example.com"},
		{name: "empty event contact", key: "event.contact_email", value: ""},
		{name: "bad event contact", key: "event.contact_email", value: "hello", wantErr: true},
		{name: "rules url", key: "event.rules_url", value: "https://example.com/rules"},
		{name: "empty rules url", key: "event.rules_url", value: ""},
		{name: "insecure rules url", key: "event.rules_url", value: "http://example.com/rules", wantErr: true},
		{name: "setup completion", key: "event.setup_completed", value: true},
		{name: "managed event profile", key: "event.profile_managed", value: true},
		{name: "branding storage key is protected", key: "branding.logo_key", value: "other/object", wantErr: true},
		{name: "team creation policy", key: "participants.team_creation", value: "admin"},
		{name: "bad team creation policy", key: "participants.team_creation", value: "captain", wantErr: true},
		{name: "team size policy", key: "participants.default_team_size", value: float64(4)},
		{name: "allowed email domains", key: "participants.allowed_email_domains", value: "kpmg.com, *.kpmg.co.uk"},
		{name: "bad email domains", key: "participants.allowed_email_domains", value: "https://kpmg.com", wantErr: true},
		{name: "brand accent", key: "branding.accent", value: "violet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePlatformSetting(tt.key, tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePlatformSetting(%q, %#v) error = %v, wantErr %v", tt.key, tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestParseOptionalRFC3339(t *testing.T) {
	if parsed, err := parseOptionalRFC3339("  "); err != nil || parsed != nil {
		t.Fatalf("empty cutoff = (%v, %v), want (nil, nil)", parsed, err)
	}
	parsed, err := parseOptionalRFC3339("2026-09-28T00:00:00+05:30")
	if err != nil {
		t.Fatalf("valid cutoff: %v", err)
	}
	if got := parsed.Format(time.RFC3339); got != "2026-09-27T18:30:00Z" {
		t.Fatalf("normalized cutoff = %s", got)
	}
	if _, err := parseOptionalRFC3339("midnight"); err == nil {
		t.Fatal("invalid cutoff error = nil")
	}
}

func TestParseEventWindow(t *testing.T) {
	if startAt, endAt, err := parseEventWindow("", ""); err != nil || startAt != nil || endAt != nil {
		t.Fatalf("empty event window = (%v, %v, %v), want nil values", startAt, endAt, err)
	}

	startAt, endAt, err := parseEventWindow("2026-09-13T10:00:00+05:30", "2026-09-13T12:00:00+05:30")
	if err != nil {
		t.Fatalf("parseEventWindow() error = %v", err)
	}
	if startAt.Location() != time.UTC || endAt.Location() != time.UTC {
		t.Fatalf("event window was not normalized to UTC: %v, %v", startAt, endAt)
	}

	for _, test := range []struct {
		name  string
		start string
		end   string
	}{
		{name: "missing end", start: "2026-09-13T10:00:00Z"},
		{name: "invalid start", start: "invalid", end: "2026-09-13T12:00:00Z"},
		{name: "equal times", start: "2026-09-13T12:00:00Z", end: "2026-09-13T12:00:00Z"},
		{name: "reversed times", start: "2026-09-13T13:00:00Z", end: "2026-09-13T12:00:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := parseEventWindow(test.start, test.end); err == nil {
				t.Fatal("parseEventWindow() error = nil")
			}
		})
	}
}

func TestEventPhase(t *testing.T) {
	startAt := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	endAt := startAt.Add(24 * time.Hour)
	for _, test := range []struct {
		name string
		now  time.Time
		want string
	}{
		{name: "scheduled", now: startAt.Add(-time.Second), want: "scheduled"},
		{name: "starts inclusively", now: startAt, want: "live"},
		{name: "live", now: startAt.Add(time.Hour), want: "live"},
		{name: "ends exclusively", now: endAt, want: "ended"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := eventPhase(test.now, startAt, endAt); got != test.want {
				t.Fatalf("eventPhase() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEventClockVisibleForTwoDaysAfterEnd(t *testing.T) {
	endAt := time.Date(2026, time.September, 14, 10, 0, 0, 0, time.UTC)
	if !eventClockVisible(endAt.Add(eventClockGracePeriod-time.Nanosecond), endAt) {
		t.Fatal("event clock was hidden before the grace period elapsed")
	}
	if eventClockVisible(endAt.Add(eventClockGracePeriod), endAt) {
		t.Fatal("event clock remained visible at the grace-period boundary")
	}
}
