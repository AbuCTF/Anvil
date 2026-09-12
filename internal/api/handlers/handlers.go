package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/storage"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/anvil-lab/anvil/internal/services/vpn"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	config         *config.Config
	db             *database.DB
	containerSvc   *container.Service
	vmSvc          *vm.Service
	logger         *zap.Logger
	attachmentHdlr *AttachmentHandler
}

func NewChallengeHandler(cfg *config.Config, db *database.DB, containerSvc *container.Service, vmSvc *vm.Service, logger *zap.Logger) *ChallengeHandler {
	return &ChallengeHandler{config: cfg, db: db, containerSvc: containerSvc, vmSvc: vmSvc, logger: logger}
}

// NewChallengeHandlerWithAttachments creates a ChallengeHandler with attachment support.
func NewChallengeHandlerWithAttachments(cfg *config.Config, db *database.DB, containerSvc *container.Service, vmSvc *vm.Service, logger *zap.Logger, ah *AttachmentHandler) *ChallengeHandler {
	return &ChallengeHandler{config: cfg, db: db, containerSvc: containerSvc, vmSvc: vmSvc, logger: logger, attachmentHdlr: ah}
}

// ScoreboardHandler - methods implemented in scoreboard.go
type ScoreboardHandler struct {
	config            *config.Config
	db                *database.DB
	logger            *zap.Logger
	cacheMu           sync.Mutex
	cache             map[string]scoreboardCacheEntry
	availability      bool
	scoreboardPublic  bool
	availabilityUntil time.Time
	availabilityMu    sync.Mutex
	flightMu          sync.Mutex
	flights           map[string]*scoreboardCacheFlight
}

func NewScoreboardHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *ScoreboardHandler {
	return &ScoreboardHandler{
		config:  cfg,
		db:      db,
		logger:  logger,
		cache:   make(map[string]scoreboardCacheEntry),
		flights: make(map[string]*scoreboardCacheFlight),
	}
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
	rows, err := h.db.Pool.Query(c.Request.Context(),
		`SELECT id, name, slug, COALESCE(description, ''), COALESCE(color, ''), sort_order
		 FROM categories ORDER BY sort_order, name`)
	if err != nil {
		h.logger.Error("failed to list categories", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch categories"})
		return
	}
	defer rows.Close()

	var categories []gin.H
	for rows.Next() {
		var id, name, catSlug, description, color string
		var sortOrder int
		if err := rows.Scan(&id, &name, &catSlug, &description, &color, &sortOrder); err != nil {
			h.logger.Error("failed to scan category row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch categories"})
			return
		}
		categories = append(categories, gin.H{
			"id":          id,
			"name":        name,
			"slug":        catSlug,
			"description": description,
			"color":       color,
			"sort_order":  sortOrder,
		})
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing categories", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch categories"})
		return
	}

	if categories == nil {
		categories = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"categories": categories})
}
func (h *CategoryHandler) Create(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := uuid.New().String()
	safeSlug := buildCategorySlug(req.Name)
	if safeSlug == "" {
		safeSlug = id[:8]
	}

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO categories (id, name, slug, description, color)
		 VALUES ($1, $2, $3, $4, $5)`,
		id, req.Name, safeSlug, req.Description, req.Color,
	)
	if err != nil {
		h.logger.Error("failed to create category", zap.Error(err))
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "category name or slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create category"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          id,
		"name":        req.Name,
		"slug":        safeSlug,
		"description": req.Description,
		"color":       req.Color,
	})
}

// buildCategorySlug converts a human-readable name into a URL-safe slug.
// Non-alphanumeric characters are replaced by dashes; consecutive dashes are
// collapsed and leading/trailing dashes are trimmed.
func buildCategorySlug(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
func (h *CategoryHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.db.Pool.Exec(c.Request.Context(),
		`UPDATE categories SET name=$1, description=$2, color=$3 WHERE id=$4`,
		req.Name, req.Description, req.Color, id,
	)
	if err != nil {
		h.logger.Error("failed to update category", zap.Error(err))
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "category name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update category"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "category updated"})
}
func (h *CategoryHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin category deletion", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete category"})
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE challenges SET category_id = NULL WHERE category_id = $1`, id); err != nil {
		h.logger.Error("failed to detach category challenges", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete category"})
		return
	}

	result, err := tx.Exec(ctx,
		`DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		h.logger.Error("failed to delete category", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete category"})
		return
	}
	if result.RowsAffected() != 1 {
		c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit category deletion", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete category"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "category deleted"})
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
		       i.created_at, i.expires_at, COALESCE(u.username, tt.team_name, 'Team session'), c.name as challenge_name,
		       i.resource_type
		FROM instances i
		LEFT JOIN users u ON i.user_id = u.id
		LEFT JOIN sessions s ON i.session_id = s.id
		LEFT JOIN team_tokens tt ON s.token_id = tt.id
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
		var id, challengeID, status, username, challengeName, resourceType string
		var userID *string
		var containerID, ipAddress *string
		var createdAt time.Time
		var expiresAt *time.Time

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
		}
		if expiresAt != nil {
			inst["expires_at"] = expiresAt.Unix()
		} else {
			inst["expires_at"] = nil
		}

		if containerID != nil {
			inst["container_id"] = *containerID
		}
		if ipAddress != nil {
			inst["ip_address"] = *ipAddress
		}

		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}

	if instances == nil {
		instances = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"instances": instances, "total": len(instances)})
}

func (h *AdminInstanceHandler) Stats(c *gin.Context) {
	var stats struct {
		TotalInstances    int
		RunningVMs        int
		RunningContainers int
		UsedVCPU          int
		UsedMemoryMB      int
		ActiveVMs         int
	}

	if err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM instances WHERE status = 'running'
	`).Scan(&stats.TotalInstances); err != nil {
		h.logger.Error("failed to count running instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance stats"})
		return
	}

	if err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.status = 'running' AND i.resource_type = 'vm'
	`).Scan(&stats.RunningVMs); err != nil {
		h.logger.Error("failed to count running VMs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance stats"})
		return
	}

	if err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COUNT(*) FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.status = 'running' AND i.resource_type = 'docker'
	`).Scan(&stats.RunningContainers); err != nil {
		h.logger.Error("failed to count running containers", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance stats"})
		return
	}

	if err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT COALESCE(SUM(used_vcpu), 0), COALESCE(SUM(used_memory_mb), 0), COALESCE(SUM(active_vms), 0)
		FROM vm_nodes
	`).Scan(&stats.UsedVCPU, &stats.UsedMemoryMB, &stats.ActiveVMs); err != nil {
		h.logger.Error("failed to load VM node usage", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func (h *AdminInstanceHandler) ForceStop(c *gin.Context) {
	parsedID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid instance ID"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	var inst struct {
		RuntimeID        *string
		ResourceType     string
		VMNodeID         *uuid.UUID
		ReservedVCPU     int
		ReservedMemoryMB int
	}
	err = h.db.Pool.QueryRow(ctx, `
		SELECT container_id, resource_type, vm_node_id,
		       COALESCE(reserved_vcpu, 0), COALESCE(reserved_memory_mb, 0)
		FROM instances WHERE id = $1`, parsedID).Scan(
		&inst.RuntimeID, &inst.ResourceType, &inst.VMNodeID,
		&inst.ReservedVCPU, &inst.ReservedMemoryMB,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}
	if err != nil {
		h.logger.Error("failed to load instance for admin stop", zap.Error(err), zap.String("instance_id", parsedID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load instance"})
		return
	}

	runtimeID := ""
	if inst.RuntimeID != nil {
		runtimeID = *inst.RuntimeID
	}
	if inst.ResourceType == "vm" && runtimeID == "" && inst.VMNodeID != nil {
		runtimeID = parsedID.String()
	}
	if err := h.stopInstanceRuntime(ctx, runtimeID, inst.ResourceType, inst.VMNodeID); err != nil {
		h.logger.Error("failed to stop instance runtime", zap.Error(err), zap.String("instance_id", parsedID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance runtime"})
		return
	}

	tx, err := h.db.Pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `DELETE FROM instances WHERE id = $1`, parsedID)
	if err != nil || result.RowsAffected() != 1 {
		h.logger.Error("failed to delete stopped instance", zap.Error(err), zap.Int64("rows_affected", result.RowsAffected()), zap.String("instance_id", parsedID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	if inst.ResourceType == "vm" {
		if err := releaseVMNodeCapacity(ctx, tx, inst.VMNodeID, inst.ReservedVCPU, inst.ReservedMemoryMB); err != nil {
			h.logger.Error("failed to release VM capacity", zap.Error(err), zap.String("instance_id", parsedID.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to release instance capacity"})
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop instance"})
		return
	}
	if uid, ok := contextUserID(c); ok {
		if err := logAdminAction(h.db, c, uid.String(), "instance_force_stopped", "instance", parsedID.String(), nil); err != nil {
			h.logger.Warn("failed to audit forced instance stop", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "instance stopped"})
}

func (h *AdminInstanceHandler) stopInstanceRuntime(
	ctx context.Context,
	runtimeID string,
	resourceType string,
	nodeID *uuid.UUID,
) error {
	if runtimeID == "" {
		return nil
	}
	switch resourceType {
	case "vm":
		if h.vmSvc == nil {
			return errors.New("VM service unavailable")
		}
		if nodeID != nil {
			node, err := loadAssignedVMNode(ctx, h.db.Pool, *nodeID)
			if err != nil {
				return fmt.Errorf("load assigned VM node: %w", err)
			}
			service, ok := h.vmSvc.(interface {
				DestroyInstanceByNameOnNode(context.Context, string, *vm.NodeInfo) error
			})
			if !ok {
				return errors.New("VM service does not support assigned-node cleanup")
			}
			return service.DestroyInstanceByNameOnNode(ctx, runtimeID, node)
		}
		service, ok := h.vmSvc.(interface {
			DestroyInstanceByName(context.Context, string) error
		})
		if !ok {
			return errors.New("VM service unavailable")
		}
		return service.DestroyInstanceByName(ctx, runtimeID)
	case "docker":
		service, ok := h.containerSvc.(interface {
			StopInstance(context.Context, string) error
		})
		if !ok {
			return errors.New("container service unavailable")
		}
		return service.StopInstance(ctx, runtimeID)
	default:
		return fmt.Errorf("unsupported resource type %q", resourceType)
	}
}

func (h *AdminInstanceHandler) ForceDelete(c *gin.Context) {
	h.ForceStop(c) // Same implementation
}

func (h *AdminInstanceHandler) Cleanup(c *gin.Context) {
	ctx := c.Request.Context()

	// Failed VM creation can leave a durable reservation when remote cleanup was
	// uncertain, so retry those alongside normally expired instances.
	rows, err := h.db.Pool.Query(ctx, `
		SELECT i.id, i.container_id, i.resource_type, i.vm_node_id,
		       COALESCE(i.reserved_vcpu, 0), COALESCE(i.reserved_memory_mb, 0)
		FROM instances i
		WHERE (i.expires_at < NOW() AND i.status IN ('running', 'pending', 'creating'))
		   OR (i.status = 'failed' AND i.vm_node_id IS NOT NULL
		       AND i.updated_at < NOW() - INTERVAL '1 minute')
		ORDER BY i.updated_at
		LIMIT 100
	`)
	if err != nil {
		h.logger.Error("failed to query expired instances for cleanup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query expired instances"})
		return
	}

	type expiredInst struct {
		ID               uuid.UUID
		RuntimeID        *string
		ResourceType     string
		VMNodeID         *uuid.UUID
		ReservedVCPU     int
		ReservedMemoryMB int
	}
	toStop := make([]expiredInst, 0)
	for rows.Next() {
		var inst expiredInst
		if scanErr := rows.Scan(
			&inst.ID, &inst.RuntimeID, &inst.ResourceType, &inst.VMNodeID,
			&inst.ReservedVCPU, &inst.ReservedMemoryMB,
		); scanErr != nil {
			rows.Close()
			h.logger.Error("failed to scan expired instance", zap.Error(scanErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query expired instances"})
			return
		}
		toStop = append(toStop, inst)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		h.logger.Error("failed while reading expired instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query expired instances"})
		return
	}
	rows.Close()

	var stoppedCount, expiredCount, failedCount int64
	for _, inst := range toStop {
		runtimeID := ""
		if inst.RuntimeID != nil {
			runtimeID = *inst.RuntimeID
		}
		if inst.ResourceType == "vm" && runtimeID == "" && inst.VMNodeID != nil {
			runtimeID = inst.ID.String()
		}
		if err := h.stopInstanceRuntime(ctx, runtimeID, inst.ResourceType, inst.VMNodeID); err != nil {
			h.logger.Warn("cleanup: failed to stop runtime", zap.Error(err), zap.String("instance_id", inst.ID.String()))
			failedCount++
			continue
		}
		if runtimeID != "" {
			stoppedCount++
		}

		tx, err := h.db.Pool.Begin(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark expired instances"})
			return
		}
		result, err := tx.Exec(ctx, `
			UPDATE instances
			SET status = 'expired', vm_node_id = NULL, reserved_vcpu = NULL,
			    reserved_memory_mb = NULL, updated_at = NOW()
			WHERE id = $1 AND (
				(expires_at < NOW() AND status IN ('running', 'pending', 'creating'))
				OR (status = 'failed' AND vm_node_id IS NOT NULL)
			)`, inst.ID)
		if err == nil && result.RowsAffected() != 1 {
			err = fmt.Errorf("expiry update affected %d rows", result.RowsAffected())
		}
		if err == nil && inst.ResourceType == "vm" {
			err = releaseVMNodeCapacity(ctx, tx, inst.VMNodeID, inst.ReservedVCPU, inst.ReservedMemoryMB)
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if err != nil {
			h.logger.Error("cleanup: failed to expire instance", zap.Error(err), zap.String("instance_id", inst.ID.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark expired instances"})
			return
		}
		expiredCount++
	}

	// Delete old failed/stopped/expired instances
	result, err := h.db.Pool.Exec(ctx, `
		DELETE FROM instances
		WHERE status IN ('failed', 'stopped', 'expired')
		  AND vm_node_id IS NULL
		  AND created_at < NOW() - INTERVAL '1 hour'
	`)
	if err != nil {
		h.logger.Error("failed to cleanup old instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cleanup old instances"})
		return
	}

	deletedCount := result.RowsAffected()

	c.JSON(http.StatusOK, gin.H{
		"message":           "cleanup completed",
		"resources_stopped": stoppedCount,
		"marked_expired":    expiredCount,
		"cleanup_failed":    failedCount,
		"deleted":           deletedCount,
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
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, team_name, RIGHT(token, 4), COALESCE(max_uses, 1),
		       COALESCE(current_uses, 0), expires_at, created_by, created_at
		FROM team_tokens ORDER BY created_at DESC
	`)
	if err != nil {
		h.logger.Error("failed to list team tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list team tokens"})
		return
	}
	defer rows.Close()

	tokens := make([]gin.H, 0)
	for rows.Next() {
		var id, teamName, suffix string
		var maxUses, currentUses int
		var expiresAt *time.Time
		var createdBy *uuid.UUID
		var createdAt time.Time
		if err := rows.Scan(&id, &teamName, &suffix, &maxUses, &currentUses, &expiresAt, &createdBy, &createdAt); err != nil {
			h.logger.Error("failed to scan team token", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list team tokens"})
			return
		}
		tokens = append(tokens, tokenSummary(id, teamName, suffix, maxUses, currentUses, expiresAt, createdBy, createdAt))
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing team tokens", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list team tokens"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tokens": tokens})
}
func (h *TokenHandler) CreateTeamToken(c *gin.Context) {
	var req struct {
		TeamName string     `json:"team_name" binding:"required"`
		MaxUses  int        `json:"max_uses"`
		Expires  *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.TeamName = strings.TrimSpace(req.TeamName)
	if req.TeamName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_name is required"})
		return
	}
	if len(req.TeamName) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team_name must be at most 100 characters"})
		return
	}
	if req.MaxUses == 0 {
		req.MaxUses = 1
	}
	if err := validateTokenLifetime(req.MaxUses, req.Expires); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Expires != nil {
		expiresUTC := req.Expires.UTC()
		req.Expires = &expiresUTC
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	token, err := generateOpaqueToken("anvil_team_")
	if err != nil {
		h.logger.Error("failed to generate team token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team token"})
		return
	}
	id := uuid.New()
	var createdAt time.Time
	err = h.db.Pool.QueryRow(c.Request.Context(), `
		INSERT INTO team_tokens (id, token, team_name, max_uses, current_uses, expires_at, created_by)
		VALUES ($1, $2, $3, $4, 0, $5, $6) RETURNING created_at
	`, id, token, req.TeamName, req.MaxUses, req.Expires, uid).Scan(&createdAt)
	if err != nil {
		h.logger.Error("failed to create team token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team token"})
		return
	}
	if err := logAdminAction(h.db, c, uid.String(), "team_token_created", "team_token", id.String(), map[string]interface{}{
		"team_name": req.TeamName, "max_uses": req.MaxUses, "expires_at": req.Expires,
	}); err != nil {
		h.logger.Warn("failed to audit team token creation", zap.Error(err))
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "token": token, "team_name": req.TeamName, "max_uses": req.MaxUses, "expires_at": req.Expires, "created_at": createdAt.UTC()})
}
func (h *TokenHandler) DeleteTeamToken(c *gin.Context) {
	h.deleteToken(c, "team_tokens", "team token")
}
func (h *TokenHandler) ListInviteCodes(c *gin.Context) {
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, RIGHT(code, 4), COALESCE(max_uses, 1), COALESCE(current_uses, 0),
		       expires_at, created_by, created_at
		FROM invite_codes ORDER BY created_at DESC
	`)
	if err != nil {
		h.logger.Error("failed to list invite codes", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list invite codes"})
		return
	}
	defer rows.Close()

	codes := make([]gin.H, 0)
	for rows.Next() {
		var id, suffix string
		var maxUses, currentUses int
		var expiresAt *time.Time
		var createdBy *uuid.UUID
		var createdAt time.Time
		if err := rows.Scan(&id, &suffix, &maxUses, &currentUses, &expiresAt, &createdBy, &createdAt); err != nil {
			h.logger.Error("failed to scan invite code", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list invite codes"})
			return
		}
		summary := tokenSummary(id, "", suffix, maxUses, currentUses, expiresAt, createdBy, createdAt)
		summary["code_suffix"] = suffix
		delete(summary, "token_suffix")
		codes = append(codes, summary)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing invite codes", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list invite codes"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"codes": codes})
}
func (h *TokenHandler) CreateInviteCode(c *gin.Context) {
	var req struct {
		MaxUses int        `json:"max_uses"`
		Expires *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MaxUses == 0 {
		req.MaxUses = 1
	}
	if err := validateTokenLifetime(req.MaxUses, req.Expires); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Expires != nil {
		expiresUTC := req.Expires.UTC()
		req.Expires = &expiresUTC
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	code, err := generateOpaqueToken("anvil_inv_")
	if err != nil {
		h.logger.Error("failed to generate invite code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invite code"})
		return
	}
	id := uuid.New()
	var createdAt time.Time
	err = h.db.Pool.QueryRow(c.Request.Context(), `
		INSERT INTO invite_codes (id, code, max_uses, current_uses, expires_at, created_by)
		VALUES ($1, $2, $3, 0, $4, $5) RETURNING created_at
	`, id, code, req.MaxUses, req.Expires, uid).Scan(&createdAt)
	if err != nil {
		h.logger.Error("failed to create invite code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invite code"})
		return
	}
	if err := logAdminAction(h.db, c, uid.String(), "invite_code_created", "invite_code", id.String(), map[string]interface{}{
		"max_uses": req.MaxUses, "expires_at": req.Expires,
	}); err != nil {
		h.logger.Warn("failed to audit invite code creation", zap.Error(err))
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "code": code, "max_uses": req.MaxUses, "expires_at": req.Expires, "created_at": createdAt.UTC()})
}
func (h *TokenHandler) DeleteInviteCode(c *gin.Context) {
	h.deleteToken(c, "invite_codes", "invite code")
}

func (h *TokenHandler) deleteToken(c *gin.Context, table, label string) {
	query := "DELETE FROM " + table + " WHERE id = $1"
	result, err := h.db.Pool.Exec(c.Request.Context(), query, c.Param("id"))
	if err != nil {
		h.logger.Error("failed to delete "+label, zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete " + label})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": label + " not found"})
		return
	}
	if uid, ok := contextUserID(c); ok {
		action := strings.ReplaceAll(label, " ", "_") + "_deleted"
		if err := logAdminAction(h.db, c, uid.String(), action, strings.ReplaceAll(label, " ", "_"), c.Param("id"), nil); err != nil {
			h.logger.Warn("failed to audit "+label+" deletion", zap.Error(err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": label + " deleted"})
}

func generateOpaqueToken(prefix string) (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(bytes), nil
}

func validateTokenLifetime(maxUses int, expiresAt *time.Time) error {
	if maxUses < 1 {
		return errors.New("max_uses must be positive")
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return errors.New("expires_at must be in the future")
	}
	return nil
}

func contextUserID(c *gin.Context) (uuid.UUID, bool) {
	value, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := value.(uuid.UUID)
	return uid, ok && uid != uuid.Nil
}

func tokenSummary(id, teamName, suffix string, maxUses, currentUses int, expiresAt *time.Time, createdBy *uuid.UUID, createdAt time.Time) gin.H {
	var expiresAtUTC *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC()
		expiresAtUTC = &value
	}
	result := gin.H{
		"id": id, "token_suffix": suffix, "max_uses": maxUses, "current_uses": currentUses,
		"expires_at": expiresAtUTC, "created_by": createdBy, "created_at": createdAt.UTC(),
		"active": currentUses < maxUses && (expiresAt == nil || expiresAt.After(time.Now())),
	}
	if teamName != "" {
		result["team_name"] = teamName
	}
	return result
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
		var key string
		var rawValue json.RawMessage
		if err := rows.Scan(&key, &rawValue); err != nil {
			h.logger.Error("Failed to scan setting", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load settings"})
			return
		}
		var value interface{}
		if err := json.Unmarshal(rawValue, &value); err != nil {
			h.logger.Error("Failed to decode setting", zap.String("key", key), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load settings"})
			return
		}
		settings[key] = value
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("Failed while loading settings", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

func (h *SettingsHandler) Update(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var req struct {
		Settings map[string]interface{} `json:"settings"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	if len(req.Settings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No settings provided"})
		return
	}
	if len(req.Settings) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Too many settings provided"})
		return
	}
	uid, ok := contextUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
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
		if strings.TrimSpace(key) == "" || len(key) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid setting key"})
			return
		}
		if err := validatePlatformSetting(key, value); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		valueJSON, err := json.Marshal(value)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid setting value: " + key})
			return
		}

		result, updateErr := tx.Exec(c.Request.Context(), `
			UPDATE platform_settings
			SET value = $2::jsonb, updated_at = NOW(), updated_by = $3
			WHERE key = $1
		`, key, string(valueJSON), uid)
		if updateErr != nil {
			h.logger.Error("Failed to update setting", zap.String("key", key), zap.Error(updateErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update setting: " + key})
			return
		}
		if result.RowsAffected() != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown setting: " + key})
			return
		}
	}

	metadata, _ := json.Marshal(map[string]interface{}{"settings_count": len(req.Settings)})
	if _, err := tx.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, new_values, ip_address, user_agent)
		VALUES ($1, 'settings_updated', 'platform_settings', $2::jsonb, $3, $4)
	`, uid, string(metadata), c.ClientIP(), c.Request.UserAgent()); err != nil {
		h.logger.Error("Failed to audit settings update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update settings"})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func validatePlatformSetting(key string, value interface{}) error {
	intRange := func(minimum, maximum int) error {
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number || number < float64(minimum) || number > float64(maximum) {
			return fmt.Errorf("Invalid value for %s: expected an integer from %d to %d", key, minimum, maximum)
		}
		return nil
	}
	boolValue := func() error {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("Invalid value for %s: expected true or false", key)
		}
		return nil
	}

	switch key {
	case "instance.max_per_user":
		return intRange(1, 100)
	case "instance.max_extensions":
		return intRange(0, 10)
	case "instance.extension_minutes":
		return intRange(1, 24*60)
	case "vm_default_timeout_easy", "vm_default_timeout_medium", "vm_default_timeout_hard", "vm_default_timeout_insane":
		return intRange(30, 480)
	case "cooldown.easy_minutes", "cooldown.medium_minutes", "cooldown.hard_minutes", "cooldown.insane_minutes":
		return intRange(0, 120)
	case "platform.require_vpn", "scoreboard_enabled":
		return boolValue()
	case "registration_mode":
		mode, ok := value.(string)
		if !ok || !isRegistrationMode(strings.ToLower(strings.TrimSpace(mode))) {
			return errors.New("Invalid value for registration_mode")
		}
	}
	return nil
}

// AuditHandler for audit logs
type AuditHandler struct {
	db     *database.DB
	logger *zap.Logger
}

func NewAuditHandler(db *database.DB, logger *zap.Logger) *AuditHandler {
	return &AuditHandler{db: db, logger: logger}
}

func (h *AuditHandler) List(c *gin.Context) {
	limit, offset, err := parsePage(c.Query("limit"), c.Query("offset"), 100, 200)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var userID *uuid.UUID
	if raw := c.Query("user_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
			return
		}
		userID = &parsed
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT a.id, a.user_id, COALESCE(u.username, ''), a.action,
		       COALESCE(a.entity_type, ''), COALESCE(a.entity_id::text, ''),
		       COALESCE(a.old_values, 'null'::jsonb), COALESCE(a.new_values, 'null'::jsonb),
		       COALESCE(a.ip_address::text, ''), COALESCE(a.user_agent, ''), a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		WHERE ($1 = '' OR a.action = $1) AND ($2::uuid IS NULL OR a.user_id = $2)
		ORDER BY a.created_at DESC LIMIT $3 OFFSET $4
	`, c.Query("action"), userID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list audit entries", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit entries"})
		return
	}
	defer rows.Close()

	entries := make([]gin.H, 0)
	for rows.Next() {
		var id, username, action, entityType, entityID, ipAddress, userAgent string
		var actorID *uuid.UUID
		var oldRaw, newRaw json.RawMessage
		var createdAt time.Time
		if err := rows.Scan(&id, &actorID, &username, &action, &entityType, &entityID,
			&oldRaw, &newRaw, &ipAddress, &userAgent, &createdAt); err != nil {
			h.logger.Error("failed to scan audit entry", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit entries"})
			return
		}
		var oldValues, newValues interface{}
		if err := json.Unmarshal(oldRaw, &oldValues); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode audit entry"})
			return
		}
		if err := json.Unmarshal(newRaw, &newValues); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode audit entry"})
			return
		}
		entries = append(entries, gin.H{
			"id": id, "user_id": actorID, "username": username, "action": action,
			"entity_type": entityType, "entity_id": entityID, "old_values": oldValues,
			"new_values": newValues, "ip_address": ipAddress, "user_agent": userAgent,
			"created_at": createdAt.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed while listing audit entries", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit entries"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "limit": limit, "offset": offset})
}

func parsePage(rawLimit, rawOffset string, defaultLimit, maxLimit int) (int, int, error) {
	limit := defaultLimit
	offset := 0
	var err error
	if rawLimit != "" {
		limit, err = strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxLimit {
			return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxLimit)
		}
	}
	if rawOffset != "" {
		offset, err = strconv.Atoi(rawOffset)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be non-negative")
		}
	}
	return limit, offset, nil
}

func postgresErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// StatsHandler for platform statistics
type StatsHandler struct {
	db     *database.DB
	logger *zap.Logger
}

func NewStatsHandler(db *database.DB, logger *zap.Logger) *StatsHandler {
	return &StatsHandler{db: db, logger: logger}
}

// Get method is implemented in admin.go

// AttachmentHandler handles challenge file attachment operations
type AttachmentHandler struct {
	db         *database.DB
	storageSvc storage.StorageBackend
	logger     *zap.Logger
}

func NewAttachmentHandler(db *database.DB, storageSvc storage.StorageBackend, logger *zap.Logger) *AttachmentHandler {
	return &AttachmentHandler{db: db, storageSvc: storageSvc, logger: logger}
}

// logAdminAction logs an admin action to the audit log
func logAdminAction(db *database.DB, c *gin.Context, userID, action, resourceType, resourceID string, metadata map[string]interface{}) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse audit user id: %w", err)
	}

	// audit_log uses entity_type/entity_id/new_values (the initial schema), not
	// the resource_* / metadata names used by an older handler.
	var entityID interface{}
	if resourceID != "" {
		parsedID, err := uuid.Parse(resourceID)
		if err != nil {
			return fmt.Errorf("parse audit entity id: %w", err)
		}
		entityID = parsedID
	}

	_, err = db.Pool.Exec(c.Request.Context(), `
		INSERT INTO audit_log (user_id, action, entity_type, entity_id, new_values, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
	`, parsedUserID, action, resourceType, entityID, string(metadataJSON), c.ClientIP(), c.Request.UserAgent())
	return err
}
