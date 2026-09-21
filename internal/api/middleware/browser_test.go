package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func TestAPIError_BrowserGetsHTML_ClientGetsJSON(t *testing.T) {
	cases := []struct {
		name       string
		headers    map[string]string
		wantHTML   bool
	}{
		{"address-bar navigation", map[string]string{"Sec-Fetch-Mode": "navigate"}, true},
		{"old browser accept html", map[string]string{"Accept": "text/html,application/xhtml+xml"}, true},
		{"frontend fetch (cors)", map[string]string{"Sec-Fetch-Mode": "cors", "Accept": "*/*"}, false},
		{"json client", map[string]string{"Accept": "application/json"}, false},
		{"curl no headers", map[string]string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/economy/me", nil)
			for k, v := range tc.headers {
				c.Request.Header.Set(k, v)
			}
			APIError(c, http.StatusUnauthorized, "Authorization header required")

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			ct := w.Header().Get("Content-Type")
			gotHTML := strings.Contains(ct, "text/html")
			if gotHTML != tc.wantHTML {
				t.Fatalf("content-type %q: gotHTML=%v want=%v", ct, gotHTML, tc.wantHTML)
			}
			if tc.wantHTML && !strings.Contains(w.Body.String(), "Back to the platform") {
				t.Fatalf("browser body missing landing page")
			}
			if !tc.wantHTML && !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("api body should be json error, got %q", w.Body.String())
			}
		})
	}
}
