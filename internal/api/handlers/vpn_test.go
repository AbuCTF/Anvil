package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/gin-gonic/gin"
)

func TestVPNHandlersRejectInvalidUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &VPNHandler{config: &config.Config{VPN: config.VPNConfig{Enabled: true}}}
	tests := []struct {
		name   string
		handle func(*gin.Context)
	}{
		{name: "get config", handle: handler.GetConfig},
		{name: "generate config", handle: handler.GenerateConfig},
		{name: "get status", handle: handler.GetStatus},
		{name: "regenerate config", handle: handler.RegenerateConfig},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, invalidUserID := range []any{nil, "not-a-uuid"} {
				response := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(response)
				ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/vpn/config", nil)
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

func TestVPNHandlerManagesWireGuardPeersOnlyInProduction(t *testing.T) {
	for _, test := range []struct {
		environment string
		want        bool
	}{
		{environment: "production", want: true},
		{environment: " Production ", want: true},
		{environment: "development", want: false},
	} {
		handler := &VPNHandler{config: &config.Config{Environment: test.environment}}
		if got := handler.managesWireGuardPeers(); got != test.want {
			t.Fatalf("managesWireGuardPeers() for %q = %v, want %v", test.environment, got, test.want)
		}
	}
}
