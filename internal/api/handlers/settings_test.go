package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestChallengeReleaseAccess(t *testing.T) {
	now := time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC)
	start, end := now.Add(time.Hour), now.Add(25*time.Hour)
	for _, test := range []struct {
		name       string
		role       string
		start, end *time.Time
		err        error
		wantAllow  bool
		wantStatus int
	}{
		{name: "admin can preview", role: "admin", start: &start, end: &end, wantAllow: true},
		{name: "author cannot preview", role: "author", start: &start, end: &end, wantStatus: http.StatusForbidden},
		{name: "participant cannot preview", role: "user", start: &start, end: &end, wantStatus: http.StatusForbidden},
		{name: "anonymous must authenticate", start: &start, end: &end, wantStatus: http.StatusUnauthorized},
		{name: "missing window fails closed", role: "user", wantStatus: http.StatusServiceUnavailable},
		{name: "invalid window fails closed", role: "user", err: errors.New("invalid window"), wantStatus: http.StatusServiceUnavailable},
		{name: "released catalog is public", start: timePtr(now.Add(-time.Minute)), end: &end, wantAllow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed, status := challengeReleaseAccess(test.role, test.start, test.end, test.err, now)
			if allowed != test.wantAllow || status != test.wantStatus {
				t.Fatalf("access = (%v, %d), want (%v, %d)", allowed, status, test.wantAllow, test.wantStatus)
			}
		})
	}
}

func TestChallengeReleaseGateFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	start, end := now.Add(time.Hour), now.Add(25*time.Hour)
	loadErr := errors.New("settings unavailable")

	tests := []struct {
		name       string
		role       string
		load       eventWindowLoader
		wantStatus int
		wantCalled bool
	}{
		{
			name: "missing window",
			role: "user",
			load: func(context.Context) (*time.Time, *time.Time, error) {
				return nil, nil, nil
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "invalid window",
			role: "user",
			load: func(context.Context) (*time.Time, *time.Time, error) {
				return nil, nil, errors.New("event.end_at must be after event.start_at")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "unreadable window",
			role: "user",
			load: func(context.Context) (*time.Time, *time.Time, error) {
				return nil, nil, loadErr
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "author cannot preview",
			role: "author",
			load: func(context.Context) (*time.Time, *time.Time, error) {
				return &start, &end, nil
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "admin bypasses unreadable window",
			role: "admin",
			load: func(context.Context) (*time.Time, *time.Time, error) {
				t.Fatal("admin gate loaded event window")
				return nil, nil, loadErr
			},
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			router := gin.New()
			router.GET("/guarded", func(c *gin.Context) {
				c.Set("role", test.role)
				c.Next()
			}, challengeReleaseGate(test.load, zap.NewNop()), func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/guarded", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if called != test.wantCalled {
				t.Fatalf("downstream called = %v, want %v", called, test.wantCalled)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time { return &value }

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
		{name: "scoreboard source", key: "scoreboard.score_source", value: "ledger"},
		{name: "bad scoreboard source", key: "scoreboard.score_source", value: "points", wantErr: true},
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
		{name: "event slug", key: "event.slug", value: "sample-cyber-challenge-2026"},
		{name: "uppercase event slug", key: "event.slug", value: "SAMPLE-2026", wantErr: true},
		{name: "edge hyphen event slug", key: "event.slug", value: "-sample", wantErr: true},
		{name: "event timezone", key: "event.timezone", value: "Asia/Kolkata"},
		{name: "bad event timezone", key: "event.timezone", value: "India/Somewhere", wantErr: true},
		{name: "event contact", key: "event.contact_email", value: "ctf@example.com"},
		{name: "flag format", key: "event.flag_format", value: "SAMPLECTF{...}"},
		{name: "flag format missing variable", key: "event.flag_format", value: "SAMPLECTF", wantErr: true},
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
		{name: "allowed email domains", key: "participants.allowed_email_domains", value: "company.example, *.division.example"},
		{name: "bad email domains", key: "participants.allowed_email_domains", value: "https://company.example", wantErr: true},
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
