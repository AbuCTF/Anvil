package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestSanitiseFilenameStripsPathsAndHeaderDelimiters(t *testing.T) {
	for name, want := range map[string]string{
		"../../payload.ova":  "payload.ova",
		`..\..\payload.ova`:  "payload.ova",
		"report\"; bad=.txt": "report; bad=.txt",
		"\x00\x01":           "file",
	} {
		if got := sanitiseFilename(name); got != want {
			t.Errorf("sanitiseFilename(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCreateLinkRejectsURLCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Params = gin.Params{{Key: "id", Value: uuid.NewString()}}
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"handout.zip","url":"https://PRIVATE_HIDDEN_SENTINEL@example.com/file"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	new(AttachmentHandler).CreateLink(c)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "PRIVATE_HIDDEN_SENTINEL") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
