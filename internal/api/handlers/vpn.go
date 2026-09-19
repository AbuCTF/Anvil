package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type VPNService struct {
	config *config.Config
	db     *database.DB
	vpnSvc *vpn.Service
	logger *zap.Logger
}

func NewVPNService(cfg *config.Config, db *database.DB, vpnSvc *vpn.Service, logger *zap.Logger) *VPNService {
	return &VPNService{config: cfg, db: db, vpnSvc: vpnSvc, logger: logger}
}

const vpnCleanupTimeout = 5 * time.Second

func vpnUserID(c *gin.Context) (uuid.UUID, bool) {
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
	}
	return uid, ok
}

func rollbackVPNTransaction(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), vpnCleanupTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func commitVPNTransaction(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), vpnCleanupTimeout)
	defer cancel()
	return tx.Commit(ctx)
}

func (h *VPNHandler) managesWireGuardPeers() bool {
	return strings.EqualFold(strings.TrimSpace(h.config.Environment), "production")
}

func (h *VPNHandler) removePeerForCleanup(publicKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), vpnCleanupTimeout)
	defer cancel()
	_ = h.vpnSvc.RemovePeer(ctx, publicKey)
}

func (h *VPNHandler) addPeerForCleanup(publicKey, ipAddress string) {
	ctx, cancel := context.WithTimeout(context.Background(), vpnCleanupTimeout)
	defer cancel()
	_ = h.vpnSvc.AddPeer(ctx, publicKey, ipAddress)
}

type VPNConfigResponse struct {
	HasConfig       bool    `json:"has_config"`
	IPAddress       *string `json:"ip_address,omitempty"`
	PublicKey       *string `json:"public_key,omitempty"`
	ServerPublicKey string  `json:"server_public_key,omitempty"`
	Endpoint        string  `json:"endpoint,omitempty"`
	CreatedAt       *int64  `json:"created_at,omitempty"`
	ConfigFile      *string `json:"config_file,omitempty"` // full wireguard config
}

type VPNStatusResponse struct {
	Connected     bool   `json:"connected"`
	IPAddress     string `json:"ip_address,omitempty"`
	LastHandshake *int64 `json:"last_handshake,omitempty"`
	BytesSent     int64  `json:"bytes_sent,omitempty"`
	BytesReceived int64  `json:"bytes_received,omitempty"`
}

func (h *VPNHandler) GetConfig(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	uid, ok := vpnUserID(c)
	if !ok {
		return
	}

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

	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, VPNConfigResponse{
			HasConfig: false,
			Endpoint:  h.config.VPN.PublicEndpoint,
		})
		return
	}
	if err != nil {
		h.logger.Error("failed to load VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load VPN config"})
		return
	}

	createdAt := vpnConfig.CreatedAt.Unix()

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

func (h *VPNHandler) GenerateConfig(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	uid, ok := vpnUserID(c)
	if !ok {
		return
	}

	var existingIP string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT assigned_ip FROM vpn_configs WHERE user_id = $1`, uid).Scan(&existingIP)
	if err == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "VPN config already exists",
			"ip_address": existingIP,
			"hint":       "Use POST /api/v1/vpn/config/regenerate to regenerate",
		})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.logger.Error("failed to check existing VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check existing VPN config"})
		return
	}

	privateKey, publicKey, err := h.vpnSvc.GenerateKeyPair()
	if err != nil {
		h.logger.Error("failed to generate key pair", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate VPN keys"})
		return
	}

	ipAddress, err := h.vpnSvc.AllocateIP()
	if err != nil {
		h.logger.Error("failed to allocate IP", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate IP address"})
		return
	}
	releaseIPAddress := true
	defer func() {
		if releaseIPAddress {
			h.vpnSvc.ReleaseIP(ipAddress)
		}
	}()

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin VPN config creation", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	defer rollbackVPNTransaction(tx)

	configID := uuid.New()
	createdAt := time.Now().UTC()
	result, err := tx.Exec(c.Request.Context(),
		`INSERT INTO vpn_configs (id, user_id, assigned_ip, public_key, private_key, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		configID, uid, ipAddress, publicKey, privateKey, createdAt)
	if err != nil {
		h.logger.Error("failed to store VPN config", zap.Error(err))
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "VPN config already exists or the allocated IP is unavailable"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("VPN config insert affected an unexpected number of rows",
			zap.String("user_id", uid.String()),
			zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}

	// the database row remains uncommitted until the production peer exists.
	// treat AddPeer's outcome as uncertain on error and remove the generated key
	// during cleanup so a failed request cannot leave an orphaned peer.
	peerMayExist := false
	if h.managesWireGuardPeers() {
		peerMayExist = true
		if err := h.vpnSvc.AddPeer(c.Request.Context(), publicKey, ipAddress); err != nil {
			h.removePeerForCleanup(publicKey)
			peerMayExist = false
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to activate VPN config"})
			return
		}
	}

	if err := commitVPNTransaction(tx); err != nil {
		h.logger.Error("failed to commit VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		if peerMayExist {
			h.removePeerForCleanup(publicKey)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	peerMayExist = false
	releaseIPAddress = false

	configFile := h.generateWireGuardConfig(privateKey, ipAddress)
	createdAtUnix := createdAt.Unix()

	c.JSON(http.StatusCreated, VPNConfigResponse{
		HasConfig:       true,
		IPAddress:       &ipAddress,
		PublicKey:       &publicKey,
		ServerPublicKey: h.config.VPN.PublicKey,
		Endpoint:        h.config.VPN.PublicEndpoint,
		CreatedAt:       &createdAtUnix,
		ConfigFile:      &configFile,
	})
}

func (h *VPNHandler) GetStatus(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	uid, ok := vpnUserID(c)
	if !ok {
		return
	}

	// status is updated by the wg-status-sync.sh script running on the host
	var ipAddress string
	var lastHandshake *time.Time
	var bytesSent, bytesReceived int64

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT assigned_ip, last_handshake, COALESCE(bytes_sent, 0), COALESCE(bytes_received, 0) 
		 FROM vpn_configs WHERE user_id = $1`, uid).Scan(&ipAddress, &lastHandshake, &bytesSent, &bytesReceived)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusOK, VPNStatusResponse{Connected: false})
		return
	}
	if err != nil {
		h.logger.Error("failed to load VPN status", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load VPN status"})
		return
	}

	var lastHandshakeUnix *int64
	connected := false

	if lastHandshake != nil && !lastHandshake.IsZero() {
		ts := lastHandshake.Unix()
		lastHandshakeUnix = &ts
		onlineWindow := h.config.VPN.OnlineWindow
		if onlineWindow <= 0 {
			onlineWindow = 45 * time.Second
		}
		connected = time.Since(*lastHandshake) < onlineWindow
	}

	c.JSON(http.StatusOK, VPNStatusResponse{
		Connected:     connected,
		IPAddress:     ipAddress,
		LastHandshake: lastHandshakeUnix,
		BytesSent:     bytesSent,
		BytesReceived: bytesReceived,
	})
}

// atomically replaces the old config with a new one.
func (h *VPNHandler) RegenerateConfig(c *gin.Context) {
	if !h.config.VPN.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VPN is disabled"})
		return
	}

	uid, ok := vpnUserID(c)
	if !ok {
		return
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin VPN config regeneration", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to regenerate VPN config"})
		return
	}
	defer rollbackVPNTransaction(tx)

	// lock the current config so concurrent lifecycle requests cannot replace the
	// same row while its wireguard peer is being transitioned.
	var oldPublicKey, oldIPAddress string
	err = tx.QueryRow(c.Request.Context(),
		`SELECT public_key, assigned_ip FROM vpn_configs WHERE user_id = $1 FOR UPDATE`, uid).
		Scan(&oldPublicKey, &oldIPAddress)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "VPN config not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load VPN config for regeneration", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to regenerate VPN config"})
		return
	}

	// prepare the replacement without releasing the old allocation. that keeps
	// the current config usable if any later step fails.
	privateKey, publicKey, err := h.vpnSvc.GenerateKeyPair()
	if err != nil {
		h.logger.Error("failed to generate key pair", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate VPN keys"})
		return
	}

	ipAddress, err := h.vpnSvc.AllocateIP()
	if err != nil {
		h.logger.Error("failed to allocate IP", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate IP address"})
		return
	}
	newIPAddressAllocated := true
	defer func() {
		if newIPAddressAllocated {
			h.vpnSvc.ReleaseIP(ipAddress)
		}
	}()

	createdAt := time.Now().UTC()
	result, err := tx.Exec(c.Request.Context(), `
		UPDATE vpn_configs
		SET assigned_ip = $2,
			public_key = $3,
			private_key = $4,
			is_active = TRUE,
			last_handshake = NULL,
			bytes_sent = 0,
			bytes_received = 0,
			created_at = $5,
			updated_at = $5
		WHERE user_id = $1
	`, uid, ipAddress, publicKey, privateKey, createdAt)
	if err != nil {
		h.logger.Error("failed to replace VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logger.Error("VPN config disappeared during regeneration",
			zap.String("user_id", uid.String()),
			zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusConflict, gin.H{"error": "VPN config changed during regeneration"})
		return
	}

	// add the replacement before removing the old peer. on any failure, the
	// transaction rolls back and cleanup restores the old server-side state.
	newPeerMayExist := false
	oldPeerMayNeedRestore := false
	peerTransitionComplete := false
	if h.managesWireGuardPeers() {
		defer func() {
			if peerTransitionComplete {
				return
			}
			if newPeerMayExist {
				h.removePeerForCleanup(publicKey)
			}
			if oldPeerMayNeedRestore {
				h.addPeerForCleanup(oldPublicKey, oldIPAddress)
			}
		}()

		newPeerMayExist = true
		if err := h.vpnSvc.AddPeer(c.Request.Context(), publicKey, ipAddress); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to activate replacement VPN config"})
			return
		}

		oldPeerMayNeedRestore = true
		if err := h.vpnSvc.RemovePeer(c.Request.Context(), oldPublicKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retire previous VPN config"})
			return
		}
	}

	if err := commitVPNTransaction(tx); err != nil {
		h.logger.Error("failed to commit regenerated VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	peerTransitionComplete = true
	newPeerMayExist = false
	oldPeerMayNeedRestore = false
	newIPAddressAllocated = false
	h.vpnSvc.ReleaseIP(oldIPAddress)

	configFile := h.generateWireGuardConfig(privateKey, ipAddress)
	createdAtUnix := createdAt.Unix()

	c.JSON(http.StatusOK, VPNConfigResponse{
		HasConfig:       true,
		IPAddress:       &ipAddress,
		PublicKey:       &publicKey,
		ServerPublicKey: h.config.VPN.PublicKey,
		Endpoint:        h.config.VPN.PublicEndpoint,
		CreatedAt:       &createdAtUnix,
		ConfigFile:      &configFile,
	})
}

func (h *VPNHandler) generateWireGuardConfig(privateKey, ipAddress string) string {
	endpoint := h.config.VPN.PublicEndpoint
	if !strings.Contains(endpoint, ":") {
		endpoint = fmt.Sprintf("%s:%d", endpoint, h.config.VPN.ListenPort)
	}

	dnsLine := ""
	if h.config.VPN.DNS != "" {
		dnsLine = fmt.Sprintf("DNS = %s\n", h.config.VPN.DNS)
	}

	allowedIPs := "10.100.0.0/16"
	if h.config.Container.NetworkSubnet != "" {
		allowedIPs += ", " + h.config.Container.NetworkSubnet
	}

	keepaliveSeconds := int(h.config.VPN.KeepaliveInterval / time.Second)
	if keepaliveSeconds <= 0 {
		keepaliveSeconds = 8
	}

	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
%s
[Peer]
PublicKey = %s
AllowedIPs = %s
Endpoint = %s
PersistentKeepalive = %d
`,
		privateKey,
		ipAddress,
		dnsLine,
		h.config.VPN.PublicKey,
		allowedIPs,
		endpoint,
		keepaliveSeconds,
	)
}
