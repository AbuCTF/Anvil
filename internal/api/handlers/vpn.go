package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// VPNService handles VPN operations
type VPNService struct {
	config *config.Config
	db     *database.DB
	vpnSvc *vpn.Service
	logger *zap.Logger
}

// NewVPNService creates a new VPN service handler
func NewVPNService(cfg *config.Config, db *database.DB, vpnSvc *vpn.Service, logger *zap.Logger) *VPNService {
	return &VPNService{config: cfg, db: db, vpnSvc: vpnSvc, logger: logger}
}

// VPNConfigResponse represents the VPN configuration response
type VPNConfigResponse struct {
	HasConfig       bool    `json:"has_config"`
	IPAddress       *string `json:"ip_address,omitempty"`
	PublicKey       *string `json:"public_key,omitempty"`
	ServerPublicKey string  `json:"server_public_key,omitempty"`
	Endpoint        string  `json:"endpoint,omitempty"`
	CreatedAt       *int64  `json:"created_at,omitempty"`
	ConfigFile      *string `json:"config_file,omitempty"` // Full WireGuard config
}

// VPNStatusResponse represents VPN connection status
type VPNStatusResponse struct {
	Connected     bool   `json:"connected"`
	IPAddress     string `json:"ip_address,omitempty"`
	LastHandshake *int64 `json:"last_handshake,omitempty"`
	BytesSent     int64  `json:"bytes_sent,omitempty"`
	BytesReceived int64  `json:"bytes_received,omitempty"`
}

// GetConfig returns the user's VPN configuration
func (h *VPNHandler) GetConfig(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	// Check if user has a VPN config
	var vpnConfig struct {
		IPAddress  string
		PublicKey  string
		PrivateKey string
		CreatedAt  time.Time
	}

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT assigned_ip, public_key, private_key, created_at
		 FROM vpn_configs WHERE user_id = $1`, uid).Scan(
		&vpnConfig.IPAddress, &vpnConfig.PublicKey, &vpnConfig.PrivateKey, &vpnConfig.CreatedAt,
	)

	if err != nil {
		// No config exists
		c.JSON(http.StatusOK, VPNConfigResponse{
			HasConfig: false,
			Endpoint:  h.config.VPN.PublicEndpoint,
		})
		return
	}

	createdAt := vpnConfig.CreatedAt.Unix()

	// Generate config file
	configFile := h.generateWireGuardConfig(vpnConfig.PrivateKey, vpnConfig.IPAddress)

	c.JSON(http.StatusOK, VPNConfigResponse{
		HasConfig:       true,
		IPAddress:       &vpnConfig.IPAddress,
		PublicKey:       &vpnConfig.PublicKey,
		ServerPublicKey: h.config.VPN.PublicKey,
		Endpoint:        h.config.VPN.PublicEndpoint,
		CreatedAt:       &createdAt,
		ConfigFile:      &configFile,
	})
}

// GenerateConfig generates a new VPN configuration for the user
func (h *VPNHandler) GenerateConfig(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	// Check if user already has a config
	var existingIP string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT assigned_ip FROM vpn_configs WHERE user_id = $1`, uid).Scan(&existingIP)
	if err == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "VPN config already exists",
			"ip_address": existingIP,
			"hint":       "Use DELETE /api/v1/vpn/config to regenerate",
		})
		return
	}

	// Generate key pair
	privateKey, publicKey, err := h.vpnSvc.GenerateKeyPair()
	if err != nil {
		h.logger.Error("failed to generate key pair", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate VPN keys"})
		return
	}

	// Allocate IP address
	ipAddress, err := h.vpnSvc.AllocateIP()
	if err != nil {
		h.logger.Error("failed to allocate IP", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate IP address"})
		return
	}

	// Store VPN config
	configID := uuid.New()
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO vpn_configs (id, user_id, assigned_ip, public_key, private_key, created_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())`,
		configID, uid, ipAddress, publicKey, privateKey)
	if err != nil {
		h.logger.Error("failed to store VPN config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}

	// Add peer to WireGuard server (if in production)
	if h.config.Environment == "production" {
		if err := h.vpnSvc.AddPeer(c.Request.Context(), publicKey, ipAddress); err != nil {
			h.logger.Warn("failed to add peer to WireGuard", zap.Error(err))
		}
	}

	// Generate config file
	configFile := h.generateWireGuardConfig(privateKey, ipAddress)
	createdAt := time.Now().Unix()

	c.JSON(http.StatusCreated, VPNConfigResponse{
		HasConfig:       true,
		IPAddress:       &ipAddress,
		PublicKey:       &publicKey,
		ServerPublicKey: h.config.VPN.PublicKey,
		Endpoint:        h.config.VPN.PublicEndpoint,
		CreatedAt:       &createdAt,
		ConfigFile:      &configFile,
	})
}

// GetStatus returns the VPN connection status
func (h *VPNHandler) GetStatus(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	// Get user's VPN config
	var publicKey, ipAddress string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT public_key, assigned_ip FROM vpn_configs WHERE user_id = $1`, uid).Scan(&publicKey, &ipAddress)
	if err != nil {
		c.JSON(http.StatusOK, VPNStatusResponse{Connected: false})
		return
	}

	// Check peer status
	status, err := h.vpnSvc.GetPeerStatus(publicKey)
	if err != nil {
		c.JSON(http.StatusOK, VPNStatusResponse{
			Connected: false,
			IPAddress: ipAddress,
		})
		return
	}

	var lastHandshake *int64
	if status.LastHandshake > 0 {
		lastHandshake = &status.LastHandshake
	}

	// Consider connected if handshake within last 3 minutes
	connected := status.LastHandshake > 0 && time.Now().Unix()-status.LastHandshake < 180

	c.JSON(http.StatusOK, VPNStatusResponse{
		Connected:     connected,
		IPAddress:     ipAddress,
		LastHandshake: lastHandshake,
		BytesSent:     status.TransferTx,
		BytesReceived: status.TransferRx,
	})
}

// generateWireGuardConfig generates a WireGuard client configuration
func (h *VPNHandler) generateWireGuardConfig(privateKey, ipAddress string) string {
	// Include port in endpoint if not already present
	endpoint := h.config.VPN.PublicEndpoint
	if !strings.Contains(endpoint, ":") {
		endpoint = fmt.Sprintf("%s:%d", endpoint, h.config.VPN.ListenPort)
	}

	// DNS is optional - only include if configured
	dnsLine := ""
	if h.config.VPN.DNS != "" {
		dnsLine = fmt.Sprintf("DNS = %s\n", h.config.VPN.DNS)
	}

	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/24
%s
[Peer]
PublicKey = %s
AllowedIPs = %s
Endpoint = %s
PersistentKeepalive = 25
`,
		privateKey,
		ipAddress,
		dnsLine,
		h.config.VPN.PublicKey,
		h.config.VPN.AddressRange,
		endpoint,
	)
}
