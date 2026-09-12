package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
