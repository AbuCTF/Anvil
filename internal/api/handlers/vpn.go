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
	"github.com/anvil-lab/anvil/internal/game"
	"github.com/anvil-lab/anvil/internal/services/mailer"
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
	return strings.EqualFold(strings.TrimSpace(h.config.Environment), "production") &&
		!h.usesExternalPeerControl()
}

func (h *VPNHandler) usesExternalPeerControl() bool {
	return strings.EqualFold(strings.TrimSpace(h.config.VPN.PeerControl), "external")
}

type vpnTeam struct {
	ID     uuid.UUID
	Number int
}

var errVPNTeamRequired = errors.New("active finals team required")

func (h *VPNHandler) teamForVPN(ctx context.Context, uid uuid.UUID) (vpnTeam, error) {
	teamID, ok, err := game.TeamForUser(ctx, h.db, uid)
	if err != nil {
		return vpnTeam{}, err
	}
	if !ok {
		return vpnTeam{}, errVPNTeamRequired
	}
	var number *int
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT team_number FROM game_teams WHERE id = $1 AND status = 'active' AND is_nop = FALSE`, teamID,
	).Scan(&number); err != nil {
		return vpnTeam{}, err
	}
	if number == nil || *number < 1 || *number > h.config.Game.Scoring.MaxTeamNumber() {
		return vpnTeam{}, vpn.ErrInvalidTeamNumber
	}
	return vpnTeam{ID: teamID, Number: *number}, nil
}

func (h *VPNHandler) requestVPNTeam(c *gin.Context, uid uuid.UUID) (vpnTeam, bool) {
	if !h.usesExternalPeerControl() {
		return vpnTeam{}, true
	}
	team, err := h.teamForVPN(c.Request.Context(), uid)
	if err == nil {
		return team, true
	}
	if errors.Is(err, errVPNTeamRequired) {
		c.JSON(http.StatusForbidden, gin.H{"error": "active finals team required for VPN access"})
	} else if errors.Is(err, vpn.ErrInvalidTeamNumber) {
		c.JSON(http.StatusConflict, gin.H{"error": "team has no valid VPN address pool"})
	} else {
		h.logger.Error("failed to resolve VPN team", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve VPN team"})
	}
	return vpnTeam{}, false
}

func vpnHistoryTeam(team vpnTeam) (any, any) {
	if team.ID == uuid.Nil {
		return nil, nil
	}
	return team.ID, team.Number
}

func (h *VPNHandler) privateKeyCipher() (*mailer.Cipher, error) {
	return mailer.NewScopedCipher(strings.TrimSpace(h.config.Secrets.EncryptionKey), "vpn-config")
}

func (h *VPNHandler) encryptPrivateKey(privateKey string) ([]byte, error) {
	cipher, err := h.privateKeyCipher()
	if err != nil {
		return nil, err
	}
	return cipher.Encrypt(privateKey)
}

func (h *VPNHandler) decryptPrivateKey(ciphertext []byte) (string, error) {
	cipher, err := h.privateKeyCipher()
	if err != nil {
		return "", err
	}
	return cipher.Decrypt(ciphertext)
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
		IPAddress            string
		PublicKey            string
		PrivateKey           string
		PrivateKeyCiphertext []byte
		CreatedAt            time.Time
	}

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT assigned_ip, public_key, private_key, private_key_ciphertext, created_at
		 FROM vpn_configs WHERE user_id = $1 AND is_active = TRUE`, uid).Scan(
		&vpnConfig.IPAddress, &vpnConfig.PublicKey, &vpnConfig.PrivateKey, &vpnConfig.PrivateKeyCiphertext, &vpnConfig.CreatedAt,
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
	if len(vpnConfig.PrivateKeyCiphertext) > 0 {
		vpnConfig.PrivateKey, err = h.decryptPrivateKey(vpnConfig.PrivateKeyCiphertext)
		if err != nil {
			h.logger.Error("failed to decrypt VPN config", zap.String("user_id", uid.String()), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load VPN config"})
			return
		}
	} else {
		if strings.TrimSpace(vpnConfig.PrivateKey) == "" {
			h.logger.Error("VPN config has no private key", zap.String("user_id", uid.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load VPN config"})
			return
		}
		ciphertext, encryptErr := h.encryptPrivateKey(vpnConfig.PrivateKey)
		if encryptErr != nil {
			h.logger.Error("failed to encrypt legacy VPN config", zap.String("user_id", uid.String()), zap.Error(encryptErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load VPN config"})
			return
		}
		if _, err := h.db.Pool.Exec(c.Request.Context(), `
			UPDATE vpn_configs SET private_key = '', private_key_ciphertext = $2, updated_at = NOW()
			WHERE user_id = $1 AND private_key_ciphertext IS NULL`, uid, ciphertext); err != nil {
			h.logger.Error("failed to migrate legacy VPN config", zap.String("user_id", uid.String()), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load VPN config"})
			return
		}
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
	team, ok := h.requestVPNTeam(c, uid)
	if !ok {
		return
	}

	var existingIP string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT assigned_ip FROM vpn_configs WHERE user_id = $1 AND is_active = TRUE`, uid).Scan(&existingIP)
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
	privateKeyCiphertext, err := h.encryptPrivateKey(privateKey)
	if err != nil {
		h.logger.Error("failed to encrypt VPN private key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to secure VPN config"})
		return
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to begin VPN config creation", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	defer rollbackVPNTransaction(tx)

	var ipAddress string
	releaseIPAddress := false
	if h.usesExternalPeerControl() {
		ipAddress, err = h.vpnSvc.AllocateTeamIP(c.Request.Context(), tx, team.Number, h.config.Game.Scoring.MaxTeamNumber())
	} else {
		ipAddress, err = h.vpnSvc.AllocateIP()
		releaseIPAddress = err == nil
	}
	if err != nil {
		if errors.Is(err, vpn.ErrTeamPoolExhausted) {
			c.JSON(http.StatusConflict, gin.H{"error": "team VPN address pool exhausted"})
		} else {
			h.logger.Error("failed to allocate IP", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate IP address"})
		}
		return
	}
	defer func() {
		if releaseIPAddress {
			h.vpnSvc.ReleaseIP(ipAddress)
		}
	}()

	configID := uuid.New()
	createdAt := time.Now().UTC()
	result, err := tx.Exec(c.Request.Context(), `
		INSERT INTO vpn_configs (
			id, user_id, assigned_ip, public_key, private_key, private_key_ciphertext,
			is_active, last_handshake, bytes_sent, bytes_received, created_at, updated_at
		) VALUES ($1, $2, $3, $4, '', $5, TRUE, NULL, 0, 0, $6, $6)
		ON CONFLICT (user_id) WHERE user_id IS NOT NULL DO UPDATE SET
			assigned_ip = EXCLUDED.assigned_ip,
			public_key = EXCLUDED.public_key,
			private_key = '',
			private_key_ciphertext = EXCLUDED.private_key_ciphertext,
			is_active = TRUE,
			last_handshake = NULL,
			bytes_sent = 0,
			bytes_received = 0,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
		WHERE vpn_configs.is_active = FALSE`,
		configID, uid, ipAddress, publicKey, privateKeyCiphertext, createdAt)
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
	teamID, teamNumber := vpnHistoryTeam(team)
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO vpn_peer_history (user_id, team_id, team_number, action, public_key, assigned_ip, occurred_at)
		VALUES ($1, $2, $3, 'issued', $4, $5, $6)`,
		uid, teamID, teamNumber, publicKey, ipAddress, createdAt); err != nil {
		h.logger.Error("failed to record VPN peer issue", zap.String("user_id", uid.String()), zap.Error(err))
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
	if !h.usesExternalPeerControl() {
		releaseIPAddress = false
	}

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
		 FROM vpn_configs WHERE user_id = $1 AND is_active = TRUE`, uid).Scan(&ipAddress, &lastHandshake, &bytesSent, &bytesReceived)
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
	team, ok := h.requestVPNTeam(c, uid)
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
		`SELECT public_key, assigned_ip FROM vpn_configs WHERE user_id = $1 AND is_active = TRUE FOR UPDATE`, uid).
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
	privateKeyCiphertext, err := h.encryptPrivateKey(privateKey)
	if err != nil {
		h.logger.Error("failed to encrypt VPN private key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to secure VPN config"})
		return
	}

	var ipAddress string
	newIPAddressAllocated := false
	if h.usesExternalPeerControl() {
		ipAddress, err = h.vpnSvc.AllocateTeamIP(c.Request.Context(), tx, team.Number, h.config.Game.Scoring.MaxTeamNumber())
	} else {
		ipAddress, err = h.vpnSvc.AllocateIP()
		newIPAddressAllocated = err == nil
	}
	if err != nil {
		if errors.Is(err, vpn.ErrTeamPoolExhausted) {
			c.JSON(http.StatusConflict, gin.H{"error": "team VPN address pool exhausted"})
		} else {
			h.logger.Error("failed to allocate IP", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate IP address"})
		}
		return
	}
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
			private_key = '',
			private_key_ciphertext = $4,
			is_active = TRUE,
			last_handshake = NULL,
			bytes_sent = 0,
			bytes_received = 0,
			created_at = $5,
			updated_at = $5
		WHERE user_id = $1 AND is_active = TRUE
	`, uid, ipAddress, publicKey, privateKeyCiphertext, createdAt)
	if err != nil {
		h.logger.Error("failed to replace VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save VPN config"})
		return
	}
	teamID, teamNumber := vpnHistoryTeam(team)
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO vpn_peer_history (user_id, team_id, team_number, action, public_key, assigned_ip, occurred_at)
		VALUES
			($1, $2, $3, 'rotated', $4, $5, $8),
			($1, $2, $3, 'issued', $6, $7, $8)`,
		uid, teamID, teamNumber, oldPublicKey, oldIPAddress, publicKey, ipAddress, createdAt); err != nil {
		h.logger.Error("failed to record VPN peer rotation", zap.String("user_id", uid.String()), zap.Error(err))
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
	if !h.usesExternalPeerControl() {
		newIPAddressAllocated = false
		h.vpnSvc.ReleaseIP(oldIPAddress)
	}

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

func (h *VPNHandler) RevokeConfig(c *gin.Context) {
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
		h.logger.Error("failed to begin VPN config revocation", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke VPN config"})
		return
	}
	defer rollbackVPNTransaction(tx)

	var publicKey, ipAddress string
	err = tx.QueryRow(c.Request.Context(), `
		SELECT public_key, assigned_ip FROM vpn_configs
		WHERE user_id = $1 AND is_active = TRUE FOR UPDATE`, uid,
	).Scan(&publicKey, &ipAddress)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "VPN config not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load VPN config for revocation", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke VPN config"})
		return
	}

	revokedAt := time.Now().UTC()
	if _, err := tx.Exec(c.Request.Context(), `
		UPDATE vpn_configs SET
			is_active = FALSE,
			private_key = '',
			private_key_ciphertext = NULL,
			last_handshake = NULL,
			bytes_sent = 0,
			bytes_received = 0,
			updated_at = $2
		WHERE user_id = $1 AND is_active = TRUE`, uid, revokedAt); err != nil {
		h.logger.Error("failed to deactivate VPN config", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke VPN config"})
		return
	}
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO vpn_peer_history (user_id, team_id, team_number, action, public_key, assigned_ip, occurred_at)
		VALUES (
			$1,
			(SELECT team_id FROM vpn_peer_history WHERE user_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1),
			(SELECT team_number FROM vpn_peer_history WHERE user_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1),
			'revoked', $2, $3, $4
		)`, uid, publicKey, ipAddress, revokedAt); err != nil {
		h.logger.Error("failed to record VPN peer revocation", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke VPN config"})
		return
	}

	peerRemoved := false
	if h.managesWireGuardPeers() {
		peerRemoved = true
		if err := h.vpnSvc.RemovePeer(c.Request.Context(), publicKey); err != nil {
			h.addPeerForCleanup(publicKey, ipAddress)
			peerRemoved = false
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke VPN config"})
			return
		}
	}
	if err := commitVPNTransaction(tx); err != nil {
		if peerRemoved {
			h.addPeerForCleanup(publicKey, ipAddress)
		}
		h.logger.Error("failed to commit VPN config revocation", zap.String("user_id", uid.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke VPN config"})
		return
	}
	if !h.usesExternalPeerControl() {
		h.vpnSvc.ReleaseIP(ipAddress)
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
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

	allowedIPs := normalizedAllowedIPs(h.config.VPN.AllowedIPs)
	if allowedIPs == "" {
		allowedIPs = "10.100.0.0/16"
		if h.config.Container.NetworkSubnet != "" {
			allowedIPs += ", " + h.config.Container.NetworkSubnet
		}
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

func normalizedAllowedIPs(raw string) string {
	var routes []string
	for _, route := range strings.Split(raw, ",") {
		if route = strings.TrimSpace(route); route != "" {
			routes = append(routes, route)
		}
	}
	return strings.Join(routes, ", ")
}
