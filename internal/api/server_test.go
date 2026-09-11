package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestServerUsesConfiguredTrustedProxies(t *testing.T) {
	g := newTestServer(t, &config.Config{
		Server: config.ServerConfig{TrustedProxies: []string{"10.0.0.0/8"}},
	})
	g.router.GET("/test-client-ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{
			name:       "untrusted client cannot spoof forwarding header",
			remoteAddr: "192.0.2.10:1234",
			forwarded:  "203.0.113.20",
			want:       "192.0.2.10",
		},
		{
			name:       "trusted proxy forwarding header is honored",
			remoteAddr: "10.0.0.10:1234",
			forwarded:  "203.0.113.20",
			want:       "203.0.113.20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test-client-ip", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", tt.forwarded)
			res := httptest.NewRecorder()

			g.Router().ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
			}
			if got := res.Body.String(); got != tt.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoginRouteUsesEndpointLimiter(t *testing.T) {
	g := newTestServer(t, &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 100,
			BurstSize:         100,
			Login: config.RateLimit{
				Requests: 1,
				Window:   time.Minute,
			},
		},
	})

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.11:1234"
		res := httptest.NewRecorder()
		g.Router().ServeHTTP(res, req)
		return res
	}

	if got := request().Code; got != http.StatusBadRequest {
		t.Fatalf("first login status = %d, want %d", got, http.StatusBadRequest)
	}
	if got := request().Code; got != http.StatusTooManyRequests {
		t.Fatalf("second login status = %d, want %d", got, http.StatusTooManyRequests)
	}
}

func newTestServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return NewServer(cfg, nil, nil, nil, nil, nil, nil, zap.NewNop())
}
