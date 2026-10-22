package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestSlugifyNormalizesGeneratedAndExplicitSlugs(t *testing.T) {
	tests := []struct {
		name     string
		given    string
		expected string
	}{
		{name: "  Token Overflow  ", expected: "token-overflow"},
		{name: "ignored", given: "  Custom SLUG !! ", expected: "custom-slug"},
		{name: "Web / Pwn", expected: "web-pwn"},
	}

	for _, tt := range tests {
		if got := slugify(tt.name, tt.given); got != tt.expected {
			t.Errorf("slugify(%q, %q) = %q, want %q", tt.name, tt.given, got, tt.expected)
		}
	}
}

func TestArenaValidation(t *testing.T) {
	if _, err := validateArenaText("   ", "name", 100); err == nil {
		t.Fatal("accepted whitespace-only name")
	}
	if _, err := validateArenaText("bad\nname", "name", 100); err == nil {
		t.Fatal("accepted control character")
	}
	if _, err := validateArenaSlug("!!!", ""); err == nil {
		t.Fatal("accepted empty normalized slug")
	}
	if !validArenaCategory("forensics") || validArenaCategory("hardware") {
		t.Fatal("arena category allowlist mismatch")
	}
	if got, err := validateHillBaseURL(" https://hill.internal:7101/ "); err != nil || got != "https://hill.internal:7101" {
		t.Fatalf("hill base URL = %q err=%v", got, err)
	}
	for _, invalid := range []string{"https://user:secret@hill.internal", "file:///tmp/hill", "https://hill.internal/path", "https://hill.internal?q=secret"} {
		if _, err := validateHillBaseURL(invalid); err == nil {
			t.Errorf("accepted invalid hill base URL %q", invalid)
		}
	}
}

func TestHillHolderSlugIsBoundedAndChallengeScoped(t *testing.T) {
	team, first, second := uuid.New(), uuid.New(), uuid.New()
	a := hillHolderSlug(team, first)
	b := hillHolderSlug(team, second)
	identity := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	if a == b || len(a) > 32 || !identity.MatchString(a) || !identity.MatchString(b) {
		t.Fatalf("holder slugs = %q %q", a, b)
	}
	if again := hillHolderSlug(team, first); again != a {
		t.Fatalf("holder slug changed: %q != %q", again, a)
	}
}

func TestDeleteTeamRejectsInvalidIDBeforeDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/admin/arena/teams/not-a-uuid", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}

	(&GameAdminHandler{}).DeleteTeam(ctx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "invalid id") {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}
