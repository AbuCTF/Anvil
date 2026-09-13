package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNormalizeProfileUpdate(t *testing.T) {
	displayName := "  Anvil User  "
	bio := "  building things  "
	update, err := normalizeProfileUpdate(UpdateProfileRequest{
		DisplayName: &displayName,
		Bio:         &bio,
	})
	if err != nil {
		t.Fatalf("normalizeProfileUpdate() error = %v", err)
	}
	if !update.setDisplayName || update.displayName != "Anvil User" {
		t.Fatalf("display name update = %#v", update)
	}
	if !update.setBio || update.bio != "building things" {
		t.Fatalf("bio update = %#v", update)
	}

	empty := "   "
	update, err = normalizeProfileUpdate(UpdateProfileRequest{Bio: &empty})
	if err != nil {
		t.Fatalf("empty bio should be accepted as a clear operation: %v", err)
	}
	if !update.setBio || update.bio != "" {
		t.Fatalf("empty bio update = %#v", update)
	}
}

func TestNormalizeProfileUpdateRejectsInvalidUpdates(t *testing.T) {
	tooLongDisplayName := strings.Repeat("界", maxDisplayNameRunes+1)
	tooLongBio := strings.Repeat("x", maxBioRunes+1)
	for _, test := range []struct {
		name string
		req  UpdateProfileRequest
	}{
		{name: "no fields", req: UpdateProfileRequest{}},
		{name: "long display name", req: UpdateProfileRequest{DisplayName: &tooLongDisplayName}},
		{name: "long bio", req: UpdateProfileRequest{Bio: &tooLongBio}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeProfileUpdate(test.req); err == nil {
				t.Fatal("normalizeProfileUpdate() error = nil")
			}
		})
	}
}

func TestUserHandlersRejectInvalidUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &UserHandler{}
	for _, test := range []struct {
		name   string
		handle func(*gin.Context)
	}{
		{name: "get profile", handle: handler.GetProfile},
		{name: "get rank", handle: handler.GetRank},
		{name: "update profile", handle: handler.UpdateProfile},
		{name: "get stats", handle: handler.GetStats},
		{name: "get solves", handle: handler.GetSolves},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, invalidUserID := range []any{nil, "not-a-uuid"} {
				response := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(response)
				ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/me", nil)
				if invalidUserID != nil {
					ctx.Set("user_id", invalidUserID)
				}

				test.handle(ctx)

				if response.Code != http.StatusUnauthorized {
					t.Fatalf("invalid user ID %#v returned status %d, want %d", invalidUserID, response.Code, http.StatusUnauthorized)
				}
			}
		})
	}
}

func TestUserRankETag(t *testing.T) {
	if got := userRankETag(1234); got != `"rank-1234"` {
		t.Fatalf("userRankETag(1234) = %q", got)
	}
}
