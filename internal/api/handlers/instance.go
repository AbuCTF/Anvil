package handlers

import (
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// InstanceService handles instance operations
type InstanceService struct {
	config       *config.Config
	db           *database.DB
	containerSvc *container.Service
	logger       *zap.Logger
}

// NewInstanceService creates a new instance service
func NewInstanceService(cfg *config.Config, db *database.DB, containerSvc *container.Service, logger *zap.Logger) *InstanceService {
	return &InstanceService{
		config:       cfg,
		db:           db,
		containerSvc: containerSvc,
		logger:       logger,
	}
}

// InstanceResponse represents an instance in the API response
type InstanceResponse struct {
	ID             string         `json:"id"`
	ChallengeID    string         `json:"challenge_id"`
	ChallengeName  string         `json:"challenge_name"`
	ChallengeSlug  string         `json:"challenge_slug"`
	ContainerID    string         `json:"container_id,omitempty"`
	Status         string         `json:"status"`
	IPAddress      string         `json:"ip_address,omitempty"`
	Ports          map[string]int `json:"ports,omitempty"` // service -> port
	CreatedAt      int64          `json:"created_at"`
	ExpiresAt      int64          `json:"expires_at"`
	ExtensionsUsed int            `json:"extensions_used"`
	MaxExtensions  int            `json:"max_extensions"`
}

// CreateInstanceRequest represents the request to create an instance
type CreateInstanceRequest struct {
	ChallengeSlug string `json:"challenge_slug" binding:"required"`
}

// List returns all instances for the current user
func (h *InstanceHandler) List(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	query := `
		SELECT 
			i.id, i.challenge_id, i.container_id, i.status,
			i.ip_address, i.assigned_ports, i.created_at, i.expires_at,
			i.extensions_used, COALESCE(c.max_extensions, 3) as max_extensions,
			c.name as challenge_name, c.slug as challenge_slug
		FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.user_id = $1 AND i.status NOT IN ('stopped', 'failed', 'expired')
		ORDER BY i.created_at DESC
	`

	rows, err := h.db.Pool.Query(c.Request.Context(), query, uid)
	if err != nil {
		h.logger.Error("failed to list instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch instances"})
		return
	}
	defer rows.Close()

	var instances []InstanceResponse
	for rows.Next() {
		var inst InstanceResponse
		var portsJSON []byte
		var createdAt, expiresAt time.Time
		var ipAddress, containerID *string

		if err := rows.Scan(
			&inst.ID, &inst.ChallengeID, &containerID, &inst.Status,
			&ipAddress, &portsJSON, &createdAt, &expiresAt,
			&inst.ExtensionsUsed, &inst.MaxExtensions,
			&inst.ChallengeName, &inst.ChallengeSlug,
		); err != nil {
			h.logger.Error("failed to scan instance", zap.Error(err))
			continue
		}

		if containerID != nil {
			inst.ContainerID = *containerID
		}
		if ipAddress != nil {
			inst.IPAddress = *ipAddress
		}
		inst.CreatedAt = createdAt.Unix()
		inst.ExpiresAt = expiresAt.Unix()

		// Parse ports JSON
		if len(portsJSON) > 0 {
			inst.Ports = make(map[string]int)
			// Would parse JSON here
		}

		instances = append(instances, inst)
	}

	if instances == nil {
		instances = []InstanceResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"instances": instances,
		"total":     len(instances),
	})
}

// Create spawns a new instance for a challenge
func (h *InstanceHandler) Create(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)

	var req CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check existing instances count
	var activeCount int
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM instances WHERE user_id = $1 AND status IN ('running', 'starting')`,
		uid).Scan(&activeCount)
	if err != nil {
		h.logger.Error("failed to count instances", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check instance limit"})
		return
	}

	maxInstances := h.config.Container.MaxPerUser
	if activeCount >= maxInstances {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":       "instance limit reached",
			"max_allowed": maxInstances,
			"active":      activeCount,
		})
		return
	}

	// Check if user already has an instance for this challenge
	var existingID *string
	err = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT i.id FROM instances i
		 JOIN challenges c ON i.challenge_id = c.id
		 WHERE i.user_id = $1 AND c.slug = $2 AND i.status IN ('running', 'starting')`,
		uid, req.ChallengeSlug).Scan(&existingID)
	if existingID != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":       "instance already exists for this challenge",
			"instance_id": *existingID,
		})
		return
	}

	// Get challenge details with resource_type
	var challenge struct {
		ID              string
		Name            string
		ResourceType    string // 'docker' or 'vm'
		ContainerImage  string
		ContainerTag    string
		CPULimit        string
		MemoryLimit     string
		ExposedPorts    []byte
		InstanceTimeout *int
		MaxExtensions   *int
	}

	err = h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, name, resource_type, COALESCE(container_image, ''), COALESCE(container_tag, 'latest'),
		        COALESCE(cpu_limit, '1'), COALESCE(memory_limit, '512m'),
		        exposed_ports, instance_timeout, max_extensions
		 FROM challenges WHERE slug = $1 AND status = 'published'`,
		req.ChallengeSlug).Scan(
		&challenge.ID, &challenge.Name, &challenge.ResourceType, &challenge.ContainerImage, &challenge.ContainerTag,
		&challenge.CPULimit, &challenge.MemoryLimit, &challenge.ExposedPorts,
		&challenge.InstanceTimeout, &challenge.MaxExtensions,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	// Calculate timeout
	timeout := h.config.Container.DefaultTimeout
	if challenge.InstanceTimeout != nil {
		timeout = time.Duration(*challenge.InstanceTimeout) * time.Second
	}

	maxExts := 3
	if challenge.MaxExtensions != nil {
		maxExts = *challenge.MaxExtensions
	}

	// Create instance record
	instanceID := uuid.New()
	expiresAt := time.Now().Add(timeout)

	_, err = h.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO instances (id, user_id, challenge_id, status, created_at, expires_at, max_extensions)
		 VALUES ($1, $2, $3, 'starting', NOW(), $4, $5)`,
		instanceID, uid, challenge.ID, expiresAt, maxExts)
	if err != nil {
		h.logger.Error("failed to create instance record", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create instance"})
		return
	}

	var instanceIP string
	var resourceID string // container_id or vm_id

	// Start instance based on resource type
	if challenge.ResourceType == "vm" {
		// VM-based challenge
		if h.vmSvc == nil || !h.vmSvc.IsAvailable() {
			h.db.Pool.Exec(c.Request.Context(),
				`UPDATE instances SET status = 'failed', error_message = 'VM service unavailable' WHERE id = $1`, instanceID)
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "VM service is not available on this server",
				"hint":  "This challenge requires VM infrastructure which is not configured",
			})
			return
		}

		// Look up VM template from challenge_resources
		var vmTemplateID string
		err = h.db.Pool.QueryRow(c.Request.Context(),
			`SELECT vm_template_id FROM challenge_resources 
			 WHERE challenge_id = $1 AND resource_type = 'vm' AND is_active = TRUE
			 ORDER BY sort_order LIMIT 1`,
			challenge.ID).Scan(&vmTemplateID)
		if err != nil {
			h.logger.Error("failed to find VM template for challenge", zap.Error(err))
			h.db.Pool.Exec(c.Request.Context(),
				`UPDATE instances SET status = 'failed', error_message = 'No VM template configured' WHERE id = $1`, instanceID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "VM template not configured for this challenge"})
			return
		}

		// Create VM instance
		vmInfo, err := h.vmSvc.CreateInstanceForChallenge(c.Request.Context(), challenge.ID, instanceID.String(), vmTemplateID)
		if err != nil {
			h.logger.Error("failed to create VM", zap.Error(err))
			h.db.Pool.Exec(c.Request.Context(),
				`UPDATE instances SET status = 'failed', error_message = $2 WHERE id = $1`, instanceID, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start VM: " + err.Error()})
			return
		}
		instanceIP = vmInfo.IPAddress
		resourceID = vmInfo.VMID
	} else {
		// Docker container challenge
		containerReq := container.CreateInstanceRequest{
			InstanceID:    instanceID,
			ChallengeSlug: req.ChallengeSlug,
			Image:         challenge.ContainerImage,
			Tag:           challenge.ContainerTag,
			CPULimit:      challenge.CPULimit,
			MemoryLimit:   challenge.MemoryLimit,
			Labels: map[string]string{
				"anvil.instance_id":  instanceID.String(),
				"anvil.user_id":      uid.String(),
				"anvil.challenge_id": challenge.ID,
			},
		}

		containerInfo, err := h.containerSvc.CreateInstance(c.Request.Context(), containerReq)
		if err != nil {
			h.logger.Error("failed to create container", zap.Error(err))
			h.db.Pool.Exec(c.Request.Context(),
				`UPDATE instances SET status = 'failed', error_message = $2 WHERE id = $1`, instanceID, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start container"})
			return
		}
		instanceIP = containerInfo.IPAddress
		resourceID = containerInfo.ContainerID
	}

	// Update instance record
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`UPDATE instances SET container_id = $1, ip_address = $2, status = 'running' WHERE id = $3`,
		resourceID, instanceIP, instanceID)
	if err != nil {
		h.logger.Error("failed to update instance", zap.Error(err))
	}

	c.JSON(http.StatusCreated, gin.H{
		"instance": InstanceResponse{
			ID:            instanceID.String(),
			ChallengeID:   challenge.ID,
			ChallengeName: challenge.Name,
			ChallengeSlug: req.ChallengeSlug,
			ContainerID:   resourceID,
			Status:        "running",
			IPAddress:     instanceIP,
			CreatedAt:     time.Now().Unix(),
			ExpiresAt:     expiresAt.Unix(),
			MaxExtensions: maxExts,
		},
		"message": "Instance started successfully",
	})
}

// Get returns details of a specific instance
func (h *InstanceHandler) Get(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)
	instanceID := c.Param("id")

	query := `
		SELECT 
			i.id, i.challenge_id, i.container_id, i.status,
			i.ip_address, i.ports, i.created_at, i.expires_at,
			i.extensions_used, i.max_extensions,
			c.name as challenge_name, c.slug as challenge_slug
		FROM instances i
		JOIN challenges c ON i.challenge_id = c.id
		WHERE i.id = $1 AND i.user_id = $2
	`

	var inst InstanceResponse
	var portsJSON []byte
	var createdAt, expiresAt time.Time
	var ipAddress, containerID *string

	err := h.db.Pool.QueryRow(c.Request.Context(), query, instanceID, uid).Scan(
		&inst.ID, &inst.ChallengeID, &containerID, &inst.Status,
		&ipAddress, &portsJSON, &createdAt, &expiresAt,
		&inst.ExtensionsUsed, &inst.MaxExtensions,
		&inst.ChallengeName, &inst.ChallengeSlug,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	if containerID != nil {
		inst.ContainerID = *containerID
	}
	if ipAddress != nil {
		inst.IPAddress = *ipAddress
	}
	inst.CreatedAt = createdAt.Unix()
	inst.ExpiresAt = expiresAt.Unix()

	c.JSON(http.StatusOK, inst)
}

// Extend extends the lifetime of an instance
func (h *InstanceHandler) Extend(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)
	instanceID := c.Param("id")

	// Get instance
	var inst struct {
		ID             string
		Status         string
		ExpiresAt      time.Time
		ExtensionsUsed int
		MaxExtensions  int
	}

	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, status, expires_at, extensions_used, max_extensions
		 FROM instances WHERE id = $1 AND user_id = $2`,
		instanceID, uid).Scan(&inst.ID, &inst.Status, &inst.ExpiresAt, &inst.ExtensionsUsed, &inst.MaxExtensions)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	if inst.Status != "running" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "instance is not running"})
		return
	}

	if inst.ExtensionsUsed >= inst.MaxExtensions {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "max extensions reached",
			"used":  inst.ExtensionsUsed,
			"max":   inst.MaxExtensions,
		})
		return
	}

	// Extend by 1 hour
	extension := time.Hour
	newExpiry := inst.ExpiresAt.Add(extension)

	_, err = h.db.Pool.Exec(c.Request.Context(),
		`UPDATE instances SET expires_at = $1, extensions_used = extensions_used + 1 WHERE id = $2`,
		newExpiry, instanceID)
	if err != nil {
		h.logger.Error("failed to extend instance", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to extend instance"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":              "Instance extended successfully",
		"new_expires_at":       newExpiry.Unix(),
		"extensions_used":      inst.ExtensionsUsed + 1,
		"extensions_remaining": inst.MaxExtensions - inst.ExtensionsUsed - 1,
	})
}

// Stop stops an instance
func (h *InstanceHandler) Stop(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)
	instanceID := c.Param("id")

	// Get instance
	var containerID *string
	var status string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT container_id, status FROM instances WHERE id = $1 AND user_id = $2`,
		instanceID, uid).Scan(&containerID, &status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	if status == "stopped" || status == "terminated" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "instance already stopped"})
		return
	}

	// Stop container
	if containerID != nil && *containerID != "" {
		if err := h.containerSvc.StopInstance(c.Request.Context(), *containerID); err != nil {
			h.logger.Warn("failed to stop container", zap.Error(err))
		}
	}

	// Update instance
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`UPDATE instances SET status = 'stopped' WHERE id = $1`, instanceID)
	if err != nil {
		h.logger.Error("failed to update instance", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"message": "Instance stopped successfully"})
}

// Delete terminates and removes an instance
func (h *InstanceHandler) Delete(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	uid := userID.(uuid.UUID)
	instanceID := c.Param("id")

	// Get instance
	var containerID *string
	err := h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT container_id FROM instances WHERE id = $1 AND user_id = $2`,
		instanceID, uid).Scan(&containerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	// Remove container
	if containerID != nil && *containerID != "" {
		h.containerSvc.StopInstance(c.Request.Context(), *containerID)
		h.containerSvc.RemoveInstance(c.Request.Context(), *containerID)
	}

	// Update instance
	_, err = h.db.Pool.Exec(c.Request.Context(),
		`UPDATE instances SET status = 'stopped', stopped_at = NOW() WHERE id = $1`, instanceID)
	if err != nil {
		h.logger.Error("failed to update instance", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"message": "Instance stopped successfully"})
}
