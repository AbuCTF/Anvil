package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func testHandlerContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(method, target, nil)
	return ctx, response
}

func TestChallengeProtectedHandlersRejectInvalidUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ChallengeHandler{}
	for _, test := range []struct {
		name   string
		handle func(*gin.Context)
	}{
		{name: "get flags", handle: handler.GetFlags},
		{name: "submit flag", handle: handler.SubmitFlag},
		{name: "unlock hint", handle: handler.UnlockHint},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, invalidUserID := range []any{nil, "not-a-uuid", uuid.Nil} {
				ctx, response := testHandlerContext(http.MethodPost, "/api/v1/challenges/example")
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

func TestSubmitFlagRejectsBlankAndOversizedValuesBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ChallengeHandler{}
	for _, test := range []struct {
		name string
		flag string
	}{
		{name: "blank", flag: "   "},
		{name: "oversized", flag: strings.Repeat("x", maxSubmittedFlagLength+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, response := testHandlerContext(http.MethodPost, "/api/v1/challenges/example/submit")
			ctx.Set("user_id", uuid.New())
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Request.Body = ioNopCloser{Reader: strings.NewReader(`{"flag":"` + test.flag + `"}`)}

			handler.SubmitFlag(ctx)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

type ioNopCloser struct {
	*strings.Reader
}

func (ioNopCloser) Close() error { return nil }

func TestUnlockHintRejectsMalformedHintIDBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &ChallengeHandler{}
	ctx, response := testHandlerContext(http.MethodPost, "/api/v1/challenges/example/hints/bad/unlock")
	ctx.Set("user_id", uuid.New())
	ctx.Params = gin.Params{{Key: "hint_id", Value: "bad"}}

	handler.UnlockHint(ctx)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestSanitiseFilename(t *testing.T) {
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{name: "unix traversal", in: "../../secret.txt", want: "secret.txt"},
		{name: "windows traversal", in: `..\\..\\secret.txt`, want: "secret.txt"},
		{name: "header controls", in: "payload\r\n\".txt", want: "payload.txt"},
		{name: "non ascii", in: "résumé.pdf", want: "rsum.pdf"},
		{name: "empty", in: "\x00", want: "file"},
		{name: "whitespace", in: "   ", want: "file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitiseFilename(test.in); got != test.want {
				t.Fatalf("sanitiseFilename(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestAttachmentHandlersRejectMalformedIDsBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &AttachmentHandler{}
	for _, test := range []struct {
		name   string
		handle func(*gin.Context)
		params gin.Params
	}{
		{name: "list", handle: handler.List, params: gin.Params{{Key: "id", Value: "bad"}}},
		{name: "delete challenge", handle: handler.Delete, params: gin.Params{{Key: "id", Value: "bad"}, {Key: "attachment_id", Value: uuid.NewString()}}},
		{name: "delete attachment", handle: handler.Delete, params: gin.Params{{Key: "id", Value: uuid.NewString()}, {Key: "attachment_id", Value: "bad"}}},
		{name: "download", handle: handler.Download, params: gin.Params{{Key: "attachment_id", Value: "bad"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, response := testHandlerContext(http.MethodGet, "/api/v1/attachments")
			ctx.Params = test.params

			test.handle(ctx)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestUploadAttachmentRejectsInvalidUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &AttachmentHandler{}
	ctx, response := testHandlerContext(http.MethodPost, "/api/v1/admin/challenges/id/attachments")
	ctx.Set("user_id", "not-a-uuid")

	handler.Upload(ctx)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
