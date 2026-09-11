package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRateLimitEndpointAcceptsUUIDSessionContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	sessionID := uuid.New()
	router.GET(
		"/test-team-session-limit",
		func(c *gin.Context) {
			c.Set("session_id", sessionID)
			c.Next()
		},
		RateLimitEndpoint(config.RateLimit{Requests: 1, Window: time.Minute}),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)

	request := func() int {
		req := httptest.NewRequest(http.MethodGet, "/test-team-session-limit", nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res.Code
	}

	if got := request(); got != http.StatusNoContent {
		t.Fatalf("first request status = %d, want %d", got, http.StatusNoContent)
	}
	if got := request(); got != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d", got, http.StatusTooManyRequests)
	}
}
