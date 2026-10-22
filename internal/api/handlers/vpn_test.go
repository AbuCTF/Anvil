package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		{name: "revoke config", handle: handler.RevokeConfig},
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

func TestVPNPrivateKeyEncryption(t *testing.T) {
	handler := &VPNHandler{config: &config.Config{
		Secrets: config.SecretsConfig{EncryptionKey: strings.Repeat("k", 32)},
	}}
	ciphertext, err := handler.encryptPrivateKey("client-private-key")
	if err != nil {
		t.Fatalf("encrypt private key: %v", err)
	}
	if strings.Contains(string(ciphertext), "client-private-key") {
		t.Fatal("ciphertext contains plaintext private key")
	}
	plaintext, err := handler.decryptPrivateKey(ciphertext)
	if err != nil || plaintext != "client-private-key" {
		t.Fatalf("decrypt private key = %q, %v", plaintext, err)
	}
}

func TestVPNHandlerManagesWireGuardPeersOnlyInProduction(t *testing.T) {
	for _, test := range []struct {
		environment, peerControl string
		want                     bool
	}{
		{environment: "production", want: true},
		{environment: " Production ", want: true},
		{environment: "development", want: false},
		{environment: "production", peerControl: "external", want: false},
	} {
		handler := &VPNHandler{config: &config.Config{Environment: test.environment, VPN: config.VPNConfig{PeerControl: test.peerControl}}}
		if got := handler.managesWireGuardPeers(); got != test.want {
			t.Fatalf("managesWireGuardPeers() for %q/%q = %v, want %v", test.environment, test.peerControl, got, test.want)
		}
	}
}

func TestGenerateWireGuardConfigUsesConfiguredRoutes(t *testing.T) {
	handler := &VPNHandler{config: &config.Config{
		VPN: config.VPNConfig{
			PublicEndpoint: "vpn.h7tex.com:443",
			PublicKey:      "server-key",
			AllowedIPs:     " 10.60.0.0/15,10.80.0.0/16 ",
		},
	}}
	generated := handler.generateWireGuardConfig("private-key", "10.80.1.2")
	if !strings.Contains(generated, "AllowedIPs = 10.60.0.0/15, 10.80.0.0/16") {
		t.Fatalf("generated config does not contain configured routes:\n%s", generated)
	}
}
