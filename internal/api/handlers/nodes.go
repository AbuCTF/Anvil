package handlers

import (
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// NodeHandler handles VM node management
type NodeHandler struct {
	config *config.Config
	db     *database.DB
	logger *zap.Logger
}

// NewNodeHandler creates a new node handler
func NewNodeHandler(cfg *config.Config, db *database.DB, logger *zap.Logger) *NodeHandler {
	return &NodeHandler{config: cfg, db: db, logger: logger}
}

func (h *NodeHandler) ready(c *gin.Context) bool {
	if h == nil || h.db == nil || h.db.Pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node service unavailable"})
		return false
	}
	return true
}

func (h *NodeHandler) logError(message string, fields ...zap.Field) {
	if h != nil && h.logger != nil {
		h.logger.Error(message, fields...)
	}
}

// NodeResponse represents a VM node in API responses
type NodeResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Hostname      string  `json:"hostname"`
	IPAddress     string  `json:"ip_address"`
	Status        string  `json:"status"`
	IsPrimary     bool    `json:"is_primary"`
	TotalVCPU     int     `json:"total_vcpu"`
	UsedVCPU      int     `json:"used_vcpu"`
	TotalMemoryMB int     `json:"total_memory_mb"`
	UsedMemoryMB  int     `json:"used_memory_mb"`
	TotalDiskGB   int     `json:"total_disk_gb"`
	ActiveVMs     int     `json:"active_vms"`
	MaxVMs        int     `json:"max_vms"`
	LastHeartbeat *int64  `json:"last_heartbeat,omitempty"`
	Region        *string `json:"region,omitempty"`
	Provider      *string `json:"provider,omitempty"`
	CreatedAt     int64   `json:"created_at"`
}

// ListNodes returns all VM nodes
// GET /api/v1/admin/nodes
func (h *NodeHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}

	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, name, hostname, ip_address, status, is_primary,
		       total_vcpu, used_vcpu, total_memory_mb, used_memory_mb,
		       total_disk_gb, active_vms, max_vms, last_heartbeat,
		       region, provider, created_at
		FROM vm_nodes
		ORDER BY is_primary DESC, name ASC
	`)
	if err != nil {
		h.logError("failed to list nodes", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch nodes"})
		return
	}
	defer rows.Close()

	var nodes []NodeResponse
	for rows.Next() {
		var n NodeResponse
		var lastHeartbeat *time.Time
		var createdAt time.Time
		var region, provider *string

		if err := rows.Scan(
			&n.ID, &n.Name, &n.Hostname, &n.IPAddress, &n.Status, &n.IsPrimary,
			&n.TotalVCPU, &n.UsedVCPU, &n.TotalMemoryMB, &n.UsedMemoryMB,
			&n.TotalDiskGB, &n.ActiveVMs, &n.MaxVMs, &lastHeartbeat,
			&region, &provider, &createdAt,
		); err != nil {
			h.logError("failed to scan node", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch nodes"})
			return
		}

		if lastHeartbeat != nil {
			ts := lastHeartbeat.Unix()
			n.LastHeartbeat = &ts
		}
		n.Region = region
		n.Provider = provider
		n.CreatedAt = createdAt.Unix()

		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		h.logError("failed while listing nodes", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch nodes"})
		return
	}

	if nodes == nil {
		nodes = []NodeResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes": nodes,
		"total": len(nodes),
	})
}

// GetNode returns a specific node
// GET /api/v1/admin/nodes/:id
func (h *NodeHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	nodeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node ID"})
		return
	}

	var n NodeResponse
	var lastHeartbeat *time.Time
	var createdAt time.Time
	var region, provider *string

	err = h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT id, name, hostname, ip_address, status, is_primary,
		       total_vcpu, used_vcpu, total_memory_mb, used_memory_mb,
		       total_disk_gb, active_vms, max_vms, last_heartbeat,
		       region, provider, created_at
		FROM vm_nodes WHERE id = $1
	`, nodeID).Scan(
		&n.ID, &n.Name, &n.Hostname, &n.IPAddress, &n.Status, &n.IsPrimary,
		&n.TotalVCPU, &n.UsedVCPU, &n.TotalMemoryMB, &n.UsedMemoryMB,
		&n.TotalDiskGB, &n.ActiveVMs, &n.MaxVMs, &lastHeartbeat,
		&region, &provider, &createdAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if err != nil {
		h.logError("failed to fetch node", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch node"})
		return
	}

	if lastHeartbeat != nil {
		ts := lastHeartbeat.Unix()
		n.LastHeartbeat = &ts
	}
	n.Region = region
	n.Provider = provider
	n.CreatedAt = createdAt.Unix()

	c.JSON(http.StatusOK, n)
}

// CreateNodeRequest represents the request to create a node
type CreateNodeRequest struct {
	Name          string `json:"name" binding:"required"`
	Hostname      string `json:"hostname" binding:"required"`
	IPAddress     string `json:"ip_address" binding:"required"`
	TotalVCPU     int    `json:"total_vcpu" binding:"required"`
	TotalMemoryMB int    `json:"total_memory_mb" binding:"required"`
	TotalDiskGB   int    `json:"total_disk_gb" binding:"required"`
	MaxVMs        int    `json:"max_vms"`
	Region        string `json:"region"`
	Provider      string `json:"provider"`
	SSHUser       string `json:"ssh_user"`
	SSHPort       int    `json:"ssh_port"`
	APIEndpoint   string `json:"api_endpoint"`
}

func validateCreateNodeRequest(req *CreateNodeRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Hostname = strings.TrimSpace(req.Hostname)
	req.IPAddress = strings.TrimSpace(req.IPAddress)
	req.Region = strings.TrimSpace(req.Region)
	req.Provider = strings.TrimSpace(req.Provider)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	req.APIEndpoint = strings.TrimSpace(req.APIEndpoint)

	if req.Name == "" || req.Hostname == "" || req.IPAddress == "" {
		return errors.New("name, hostname, and ip_address are required")
	}
	if len(req.Name) > 100 || len(req.Hostname) > 255 || len(req.IPAddress) > 45 ||
		len(req.Region) > 50 || len(req.Provider) > 50 || len(req.SSHUser) > 50 || len(req.APIEndpoint) > 255 {
		return errors.New("one or more node fields exceed their maximum length")
	}
	if strings.IndexFunc(req.Hostname, func(r rune) bool { return r <= ' ' || r == '/' || r == '\\' }) >= 0 {
		return errors.New("hostname contains invalid characters")
	}
	if net.ParseIP(req.IPAddress) == nil {
		return errors.New("ip_address must be a valid IPv4 or IPv6 address")
	}
	if req.MaxVMs == 0 {
		req.MaxVMs = 10
	}
	if req.SSHPort == 0 {
		req.SSHPort = 22
	}
	if req.SSHUser == "" {
		req.SSHUser = "anvil"
	}
	if req.TotalVCPU < 1 || req.TotalMemoryMB < 1 || req.TotalDiskGB < 1 || req.MaxVMs < 1 {
		return errors.New("node capacities and max_vms must be positive")
	}
	if req.TotalVCPU > math.MaxInt32 || req.TotalMemoryMB > math.MaxInt32 ||
		req.TotalDiskGB > math.MaxInt32 || req.MaxVMs > math.MaxInt32 {
		return errors.New("node capacities and max_vms are too large")
	}
	if req.SSHPort < 1 || req.SSHPort > 65535 {
		return errors.New("ssh_port must be between 1 and 65535")
	}
	if req.APIEndpoint != "" {
		endpoint, err := url.ParseRequestURI(req.APIEndpoint)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
			return errors.New("api_endpoint must be an absolute HTTP or HTTPS URL without credentials")
		}
	}
	return nil
}

// Create adds a new VM node
// POST /api/v1/admin/nodes
func (h *NodeHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req CreateNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateCreateNodeRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	nodeID := uuid.New()

	result, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO vm_nodes (
			id, name, hostname, ip_address, total_vcpu, total_memory_mb,
			total_disk_gb, max_vms, region, provider, ssh_user, ssh_port,
			api_endpoint, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'offline', NOW(), NOW())
	`, nodeID, req.Name, req.Hostname, req.IPAddress, req.TotalVCPU, req.TotalMemoryMB,
		req.TotalDiskGB, req.MaxVMs, req.Region, req.Provider, req.SSHUser, req.SSHPort, req.APIEndpoint)
	if err != nil {
		if postgresErrorCode(err) == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "a node with this name already exists"})
			return
		}
		h.logError("failed to create node", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create node"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logError("node insert affected an unexpected number of rows", zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create node"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":      nodeID.String(),
		"message": "node created",
	})
}

// UpdateNode updates a node
// PUT /api/v1/admin/nodes/:id
func (h *NodeHandler) Update(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	nodeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node ID"})
		return
	}

	var req struct {
		Status        *string `json:"status"`
		MaxVMs        *int    `json:"max_vms"`
		TotalVCPU     *int    `json:"total_vcpu"`
		TotalMemoryMB *int    `json:"total_memory_mb"`
		TotalDiskGB   *int    `json:"total_disk_gb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Status == nil && req.MaxVMs == nil && req.TotalVCPU == nil && req.TotalMemoryMB == nil && req.TotalDiskGB == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case "online", "offline", "maintenance", "draining":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node status"})
			return
		}
	}
	if (req.MaxVMs != nil && *req.MaxVMs < 1) ||
		(req.TotalVCPU != nil && *req.TotalVCPU < 1) ||
		(req.TotalMemoryMB != nil && *req.TotalMemoryMB < 1) ||
		(req.TotalDiskGB != nil && *req.TotalDiskGB < 1) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node capacities and max_vms must be positive"})
		return
	}
	if (req.MaxVMs != nil && *req.MaxVMs > math.MaxInt32) ||
		(req.TotalVCPU != nil && *req.TotalVCPU > math.MaxInt32) ||
		(req.TotalMemoryMB != nil && *req.TotalMemoryMB > math.MaxInt32) ||
		(req.TotalDiskGB != nil && *req.TotalDiskGB > math.MaxInt32) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node capacities and max_vms are too large"})
		return
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logError("failed to begin node update", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node"})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var usedVCPU, usedMemoryMB, usedDiskGB, activeVMs int
	err = tx.QueryRow(c.Request.Context(), `
		SELECT used_vcpu, used_memory_mb, used_disk_gb, active_vms
		FROM vm_nodes WHERE id = $1 FOR UPDATE
	`, nodeID).Scan(&usedVCPU, &usedMemoryMB, &usedDiskGB, &activeVMs)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if err != nil {
		h.logError("failed to lock node for update", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node"})
		return
	}
	if (req.MaxVMs != nil && *req.MaxVMs < activeVMs) ||
		(req.TotalVCPU != nil && *req.TotalVCPU < usedVCPU) ||
		(req.TotalMemoryMB != nil && *req.TotalMemoryMB < usedMemoryMB) ||
		(req.TotalDiskGB != nil && *req.TotalDiskGB < usedDiskGB) {
		c.JSON(http.StatusConflict, gin.H{"error": "node capacity cannot be reduced below current usage"})
		return
	}

	result, err := tx.Exec(c.Request.Context(), `
		UPDATE vm_nodes SET
			status = COALESCE($1, status),
			max_vms = COALESCE($2, max_vms),
			total_vcpu = COALESCE($3, total_vcpu),
			total_memory_mb = COALESCE($4, total_memory_mb),
			total_disk_gb = COALESCE($5, total_disk_gb),
			updated_at = NOW()
		WHERE id = $6
	`, req.Status, req.MaxVMs, req.TotalVCPU, req.TotalMemoryMB, req.TotalDiskGB, nodeID)
	if err != nil {
		h.logError("failed to update node", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logError("node update affected an unexpected number of rows", zap.String("node_id", nodeID.String()), zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.logError("failed to commit node update", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "node updated"})
}

// DeleteNode removes a node
// DELETE /api/v1/admin/nodes/:id
func (h *NodeHandler) Delete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	nodeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node ID"})
		return
	}

	tx, err := h.db.Pool.Begin(c.Request.Context())
	if err != nil {
		h.logError("failed to begin node deletion", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var activeVMs int
	err = tx.QueryRow(c.Request.Context(),
		`SELECT active_vms FROM vm_nodes WHERE id = $1 FOR UPDATE`, nodeID,
	).Scan(&activeVMs)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if err != nil {
		h.logError("failed to lock node for deletion", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}

	var referencedVMs int
	if err := tx.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_instances WHERE node_id = $1`, nodeID,
	).Scan(&referencedVMs); err != nil {
		h.logError("failed to inspect node instances", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}
	if activeVMs > 0 || referencedVMs > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":          "cannot delete node with VM allocations",
			"active_vms":     activeVMs,
			"referenced_vms": referencedVMs,
		})
		return
	}

	result, err := tx.Exec(c.Request.Context(), `DELETE FROM vm_nodes WHERE id = $1`, nodeID)
	if err != nil {
		if postgresErrorCode(err) == "23503" {
			c.JSON(http.StatusConflict, gin.H{"error": "node is still referenced by infrastructure records"})
			return
		}
		h.logError("failed to delete node", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logError("node deletion affected an unexpected number of rows", zap.String("node_id", nodeID.String()), zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		h.logError("failed to commit node deletion", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "node deleted"})
}

// Heartbeat updates a node's heartbeat timestamp
// POST /api/v1/nodes/heartbeat
func (h *NodeHandler) Heartbeat(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req struct {
		NodeID       string `json:"node_id" binding:"required"`
		UsedVCPU     int    `json:"used_vcpu"`
		UsedMemoryMB int    `json:"used_memory_mb"`
		ActiveVMs    int    `json:"active_vms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	nodeID, err := uuid.Parse(req.NodeID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node ID"})
		return
	}
	if req.UsedVCPU < 0 || req.UsedMemoryMB < 0 || req.ActiveVMs < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node usage values cannot be negative"})
		return
	}
	if req.UsedVCPU > math.MaxInt32 || req.UsedMemoryMB > math.MaxInt32 || req.ActiveVMs > math.MaxInt32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node usage values are too large"})
		return
	}

	result, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE vm_nodes SET
			used_vcpu = $1,
			used_memory_mb = $2,
			active_vms = $3,
			last_heartbeat = NOW(),
			status = 'online'
		WHERE id = $4
	`, req.UsedVCPU, req.UsedMemoryMB, req.ActiveVMs, nodeID)
	if err != nil {
		h.logError("failed to update heartbeat", zap.String("node_id", nodeID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update heartbeat"})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if result.RowsAffected() != 1 {
		h.logError("heartbeat affected an unexpected number of rows", zap.String("node_id", nodeID.String()), zap.Int64("rows_affected", result.RowsAffected()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update heartbeat"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "heartbeat received"})
}

// GetInfrastructureStats returns overall infrastructure statistics
// GET /api/v1/admin/infrastructure/stats
func (h *NodeHandler) GetInfrastructureStats(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var stats struct {
		TotalNodes     int
		OnlineNodes    int
		TotalVCPU      int
		UsedVCPU       int
		TotalMemoryGB  int
		UsedMemoryGB   int
		TotalVMs       int
		RunningVMs     int
		VMTemplates    int
		PendingUploads int
	}

	err := h.db.Pool.QueryRow(c.Request.Context(), `
		SELECT
			(SELECT COUNT(*) FROM vm_nodes),
			(SELECT COUNT(*) FROM vm_nodes WHERE status = 'online'),
			(SELECT COALESCE(SUM(total_vcpu), 0) FROM vm_nodes),
			(SELECT COALESCE(SUM(used_vcpu), 0) FROM vm_nodes),
			(SELECT COALESCE(SUM(total_memory_mb), 0) / 1024 FROM vm_nodes),
			(SELECT COALESCE(SUM(used_memory_mb), 0) / 1024 FROM vm_nodes),
			(SELECT COUNT(*) FROM instances i JOIN challenges c ON c.id = i.challenge_id WHERE c.resource_type = 'vm'),
			(SELECT COUNT(*) FROM instances i JOIN challenges c ON c.id = i.challenge_id WHERE c.resource_type = 'vm' AND i.status = 'running'),
			(SELECT COUNT(*) FROM vm_templates WHERE is_active = true),
			(SELECT COUNT(*) FROM uploads WHERE status IN ('pending', 'uploading', 'processing'))
	`).Scan(
		&stats.TotalNodes, &stats.OnlineNodes, &stats.TotalVCPU, &stats.UsedVCPU,
		&stats.TotalMemoryGB, &stats.UsedMemoryGB, &stats.TotalVMs, &stats.RunningVMs,
		&stats.VMTemplates, &stats.PendingUploads,
	)
	if err != nil {
		h.logError("failed to load infrastructure stats", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch infrastructure stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes": gin.H{
			"total":  stats.TotalNodes,
			"online": stats.OnlineNodes,
		},
		"resources": gin.H{
			"vcpu": gin.H{
				"total":     stats.TotalVCPU,
				"used":      stats.UsedVCPU,
				"available": stats.TotalVCPU - stats.UsedVCPU,
			},
			"memory_gb": gin.H{
				"total":     stats.TotalMemoryGB,
				"used":      stats.UsedMemoryGB,
				"available": stats.TotalMemoryGB - stats.UsedMemoryGB,
			},
		},
		"vms": gin.H{
			"total":   stats.TotalVMs,
			"running": stats.RunningVMs,
		},
		"templates":       stats.VMTemplates,
		"pending_uploads": stats.PendingUploads,
	})
}
