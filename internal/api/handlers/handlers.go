package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler struct definitions and constructors
// Method implementations are in their respective files:
// - challenge.go: ChallengeHandler methods
// - user.go: UserHandler methods
// - scoreboard.go: ScoreboardHandler methods
// - instance.go: InstanceHandler methods
// - vpn.go: VPNHandler methods
// - admin.go: Admin handler methods

// PlatformHandler handles platform info requests
type PlatformHandler struct {
	config *config.Config
	db     *database.DB
}

func NewPlatformHandler(cfg *config.Config, db *database.DB) *PlatformHandler {
	return &PlatformHandler{config: cfg, db: db}
}

func (h *PlatformHandler) GetInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":               h.config.Platform.Name,
		"description":        h.config.Platform.Description,
		"registration_mode":  h.config.Platform.RegistrationMode,
		"scoring_enabled":    h.config.Platform.ScoringEnabled,
		"scoreboard_enabled": h.config.Platform.ScoreboardEnabled,
	})
}

// ChallengeHandler - methods implemented in challenge.go
type ChallengeHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewChallengeHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *ChallengeHandler {
	return &ChallengeHandler{config: cfg, db: db, logger: logger}
}

// ScoreboardHandler - methods implemented in scoreboard.go
type ScoreboardHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewScoreboardHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *ScoreboardHandler {
	return &ScoreboardHandler{config: cfg, db: db, logger: logger}
}

// UserHandler - methods implemented in user.go
type UserHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewUserHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *UserHandler {
	return &UserHandler{config: cfg, db: db, logger: logger}
}

// InstanceHandler - methods implemented in instance.go
type InstanceHandler struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	vmSvc        *vm.Service
	logger       *zap.Logger
}

func NewInstanceHandler(cfg *config.Config, db *database.DB, containerSvc *container.Service, vmSvc *vm.Service, logger *zap.Logger) *InstanceHandler {
	return &InstanceHandler{config: cfg, db: db, containerSvc: containerSvc, vmSvc: vmSvc, logger: logger}
}

// VPNHandler - methods implemented in vpn.go
type VPNHandler struct {
	config *config.Config
	db     *database.DB
	vpnSvc *vpn.Service
	logger *zap.Logger
}

func NewVPNHandler(cfg *config.Config, db *database.DB, vpnSvc *vpn.Service, logger *zap.Logger) *VPNHandler {
	return &VPNHandler{config: cfg, db: db, vpnSvc: vpnSvc, logger: logger}
}

// AdminUserHandler - methods implemented in admin.go
type AdminUserHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewAdminUserHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *AdminUserHandler {
	return &AdminUserHandler{config: cfg, db: db, logger: logger}
}

// AdminChallengeHandler - methods implemented in admin.go
type AdminChallengeHandler struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	logger       *zap.Logger
}

func NewAdminChallengeHandler(cfg *config.Config, db *database.DB, containerSvc *container.Service, logger *zap.Logger) *AdminChallengeHandler {
	return &AdminChallengeHandler{config: cfg, db: db, containerSvc: containerSvc, logger: logger}
}

// CategoryHandler for challenge categories
type CategoryHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewCategoryHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *CategoryHandler {
	return &CategoryHandler{config: cfg, db: db, logger: logger}
}

func (h *CategoryHandler) List(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"categories": []interface{}{}})
}
func (h *CategoryHandler) Create(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}
func (h *CategoryHandler) Update(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}
func (h *CategoryHandler) Delete(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}

// AdminInstanceHandler for admin instance management
type AdminInstanceHandler struct {
	config       *config.Config
	db           *database.DB
	containerSvc interface{}
	vmSvc        interface{}
	logger       *zap.Logger
}

func NewAdminInstanceHandler(cfg *config.Config, db *database.DB, containerSvc interface{}, vmSvc interface{}, logger *zap.Logger) *AdminInstanceHandler {
	return &AdminInstanceHandler{config: cfg, db: db, containerSvc: containerSvc, vmSvc: vmSvc, logger: logger}
}

func (h *AdminInstanceHandler) List(c *gin.Context) {
	query := `
		SELECT i.id, i.user_id, i.challenge_id, i.status, i.container_id, i.ip_address,
		       i.created_at, i.expires_at, u.username, c.name as challenge_name,
		       c.resource_type
		FROM instances i
		JOIN users u ON i.user_id = u.id
		JOIN challenges c ON i.challenge_id = c.id
		ORDER BY i.created_at DESC
		LIMIT 100
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to list instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}
	defer rows.Close()

	var instances []gin.H
	for rows.Next() {
		var id, userID, challengeID, status, username, challengeName, resourceType string
		var containerID, ipAddress *string
		var createdAt, expiresAt time.Time

		if err := rows.Scan(&id, &userID, &challengeID, &status, &containerID, &ipAddress,
			&createdAt, &expiresAt, &username, &challengeName, &resourceType); err != nil {
			h.logger.Error("failed to scan instance", zap.Error(err))
			continue
		}

		inst := gin.H{
			"id":             id,
			"user_id":        userID,
			"username":       username,
			"challenge_id":   challengeID,
			"challenge_name": challengeName,
			"resource_type":  resourceType,
			"status":         status,
			"created_at":     createdAt.Unix(),
			"expires_at":     expiresAt.Unix(),
		}

		if containerID != nil {
			inst["container_id"] = *containerID
		}
		if ipAddress != nil {
			inst["ip_address"] = *ipAddress
		}

		instances = append(instances, inst)
	}

	if instances == nil {
		instances = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"instances": instances, "total": len(instances)})
}

func (h *AdminInstanceHandler) Stats(c *gin.Context) {
	var stats struct {
		TotalInstances  int
		RunningVMs      int
		RunningContainers int
		UsedVCPU        int
		UsedMemoryMB    int
		ActiveVMs       int
	}

	h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM instances WHERE status = 'running'
	`).Scan(&stats.TotalInstances)

	h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.status = 'running' AND c.resource_type = 'vm'
	`).Scan(&stats.RunningVMs)

	h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.status = 'running' AND c.resource_type = 'docker'
	`).Scan(&stats.RunningContainers)

	h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COALESCE(used_vcpu, 0), COALESCE(used_memory_mb, 0), COALESCE(active_vms, 0)
		FROM vm_nodes WHERE name = 'core'
	`).Scan(&stats.UsedVCPU, &stats.UsedMemoryMB, &stats.ActiveVMs)

	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func (h *AdminInstanceHandler) ForceStop(c *gin.Context) {
	instanceID := c.Param("id")

	var containerID *string
	var resourceType string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT i.container_id, c.resource_type FROM instances i
		 JOIN challenges c ON i.challenge_id = c.id WHERE i.id = $1`,
		instanceID).Scan(&containerID, &resourceType)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	// Stop the resource
	if containerID != nil && *containerID != "" {
		if resourceType == "vm" && h.vmSvc != nil {
			if vmSvc, ok := h.vmSvc.(interface{ DestroyInstanceByName(context.Context, string) error }); ok {
				vmSvc.DestroyInstanceByName(c.Request.Context(), *containerID)
			}
		} else if containerSvc, ok := h.containerSvc.(interface{ StopInstance(context.Context, string) error }); ok {
			containerSvc.StopInstance(c.Request.Context(), *containerID)
		}
	}

	// Delete from database
	h.db.Pool.Exec(c.Request.Context(), `DELETE FROM instances WHERE id = $1`, instanceID)

	// Update node counters if VM
	if resourceType == "vm" {
		h.db.Pool.Exec(c.Request.Context(),
			`UPDATE vm_nodes SET 
			 used_vcpu = GREATEST(0, used_vcpu - 1),
			 used_memory_mb = GREATEST(0, used_memory_mb - 1024),
			 active_vms = GREATEST(0, active_vms - 1),
			 updated_at = NOW()
			 WHERE name = 'core'`)
	}

	c.JSON(http.StatusOK, gin.H{"message": "instance stopped"})
}

func (h *AdminInstanceHandler) ForceDelete(c *gin.Context) {
	h.ForceStop(c) // Same implementation
}

func (h *AdminInstanceHandler) Cleanup(c *gin.Context) {
	// Mark all expired instances
	result, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE instances 
		SET status = 'expired', updated_at = NOW()
		WHERE expires_at < NOW() AND status IN ('running', 'pending', 'creating')
	`)
	if err != nil {
		h.logger.Error("failed to mark expired instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark expired instances"})
		return
	}

	expiredCount := result.RowsAffected()

	// Delete old failed/stopped/expired instances
	result, err = h.db.Pool.Exec(c.Request.Context(), `
		DELETE FROM instances 
		WHERE status IN ('failed', 'stopped', 'expired') 
		  AND created_at < NOW() - INTERVAL '1 hour'
	`)
	if err != nil {
		h.logger.Error("failed to cleanup old instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cleanup old instances"})
		return
	}

	deletedCount := result.RowsAffected()

	c.JSON(http.StatusOK, gin.H{
		"message":        "cleanup completed",
		"marked_expired": expiredCount,
		"deleted":        deletedCount,
	})
}

// TokenHandler for team tokens and invite codes
type TokenHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewTokenHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *TokenHandler {
	return &TokenHandler{config: cfg, db: db, logger: logger}
}

func (h *TokenHandler) ListTeamTokens(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tokens": []interface{}{}})
}
func (h *TokenHandler) CreateTeamToken(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}
func (h *TokenHandler) DeleteTeamToken(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}
func (h *TokenHandler) ListInviteCodes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"codes": []interface{}{}})
}
func (h *TokenHandler) CreateInviteCode(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}
func (h *TokenHandler) DeleteInviteCode(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Not implemented"})
}

// SettingsHandler for platform settings
type SettingsHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

func NewSettingsHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *SettingsHandler {
	return &SettingsHandler{config: cfg, db: db, logger: logger}
}

func (h *SettingsHandler) List(c *gin.Context) {
	settings := make(map[string]interface{})

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT key, value FROM platform_settings
	`)
	if err != nil {
		h.logger.Error("Failed to load settings", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load settings"})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		settings[key] = value
	}

	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

func (h *SettingsHandler) Update(c *gin.Context) {
	var req struct {
		Settings map[string]interface{} `json:"settings"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Start transaction
	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	// Upsert each setting
	for key, value := range req.Settings {
		valueStr, ok := value.(string)
		if !ok {
			// Convert numbers to string
			switch v := value.(type) {
			case float64:
				valueStr = fmt.Sprintf("%.0f", v)
			case int:
				valueStr = fmt.Sprintf("%d", v)
			default:
				valueStr = fmt.Sprintf("%v", v)
			}
		}

		_, err := tx.Exec(c.Request.Context(), `
			INSERT INTO platform_settings (key, value, updated_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = NOW()
		`, key, valueStr)
		if err != nil {
			h.logger.Error("Failed to update setting", zap.String("key", key), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update setting: " + key})
			return
		}
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit settings"})
		return
	}

	// Log the action
	userID, _ := c.Get("userID")
	uid := userID.(uuid.UUID)
	logAdminAction(h.db, c, uid.String(), "settings_updated", "platform_settings", "", map[string]interface{}{
		"settings_count": len(req.Settings),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AuditHandler for audit logs
type AuditHandler struct {
	db     *database.DB
	logger *zap.Logger
}

func NewAuditHandler(db *database.DB, logger *zap.Logger) *AuditHandler {
	return &AuditHandler{db: db, logger: logger}
}

func (h *AuditHandler) List(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"entries": []interface{}{}}) }

// StatsHandler for platform statistics
type StatsHandler struct {
	db     *database.DB
	logger *zap.Logger
}

func NewStatsHandler(db *database.DB, logger *zap.Logger) *StatsHandler {
	return &StatsHandler{db: db, logger: logger}
}

// Get method is implemented in admin.go

// logAdminAction logs an admin action to the audit log
func logAdminAction(db *database.DB, c *gin.Context, userID, action, resourceType, resourceID string, metadata map[string]interface{}) {
	metadataJSON, _ := json.Marshal(metadata)
	_, _ = db.Pool.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, resource_type, resource_id, metadata, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, userID, action, resourceType, resourceID, string(metadataJSON), c.ClientIP(), c.Request.UserAgent())
}
