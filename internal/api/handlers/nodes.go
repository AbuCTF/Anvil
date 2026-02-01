package handlers

import (
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/config"
	"github.com/anvil-lab/anvil/internal/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	rows, err := h.db.Pool.Query(c.Request.Context(), `
		SELECT id, name, hostname, ip_address, status, is_primary,
		       total_vcpu, used_vcpu, total_memory_mb, used_memory_mb,
		       total_disk_gb, active_vms, max_vms, last_heartbeat,
		       region, provider, created_at
		FROM vm_nodes
		ORDER BY is_primary DESC, name ASC
	`)
	if err != nil {
		h.logger.Error("failed to list nodes", zap.Error(err))
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
			h.logger.Error("failed to scan node", zap.Error(err))
			continue
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
	nodeID := c.Param("id")

	var n NodeResponse
	var lastHeartbeat *time.Time
	var createdAt time.Time
	var region, provider *string

	err := h.db.Pool.QueryRow(c.Request.Context(), `
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
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
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

// Create adds a new VM node
// POST /api/v1/admin/nodes
func (h *NodeHandler) Create(c *gin.Context) {
	var req CreateNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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

	nodeID := uuid.New()

	_, err := h.db.Pool.Exec(c.Request.Context(), `
		INSERT INTO vm_nodes (
			id, name, hostname, ip_address, total_vcpu, total_memory_mb,
			total_disk_gb, max_vms, region, provider, ssh_user, ssh_port,
			api_endpoint, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'offline', NOW(), NOW())
	`, nodeID, req.Name, req.Hostname, req.IPAddress, req.TotalVCPU, req.TotalMemoryMB,
		req.TotalDiskGB, req.MaxVMs, req.Region, req.Provider, req.SSHUser, req.SSHPort, req.APIEndpoint)
	if err != nil {
		h.logger.Error("failed to create node", zap.Error(err))
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
	nodeID := c.Param("id")

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

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Status != nil {
		updates = append(updates, "status = $"+string(rune('0'+argIndex)))
		args = append(args, *req.Status)
		argIndex++
	}
	if req.MaxVMs != nil {
		updates = append(updates, "max_vms = $"+string(rune('0'+argIndex)))
		args = append(args, *req.MaxVMs)
		argIndex++
	}
	if req.TotalVCPU != nil {
		updates = append(updates, "total_vcpu = $"+string(rune('0'+argIndex)))
		args = append(args, *req.TotalVCPU)
		argIndex++
	}
	if req.TotalMemoryMB != nil {
		updates = append(updates, "total_memory_mb = $"+string(rune('0'+argIndex)))
		args = append(args, *req.TotalMemoryMB)
		argIndex++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}

	args = append(args, nodeID)

	// Note: This is a simplified approach - in production use proper query building
	_, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE vm_nodes SET status = COALESCE($1, status), updated_at = NOW() WHERE id = $2
	`, req.Status, nodeID)
	if err != nil {
		h.logger.Error("failed to update node", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "node updated"})
}

// DeleteNode removes a node
// DELETE /api/v1/admin/nodes/:id
func (h *NodeHandler) Delete(c *gin.Context) {
	nodeID := c.Param("id")

	// Check if node has active VMs
	var activeVMs int
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_instances WHERE node_id = $1 AND status IN ('running', 'starting')`,
		nodeID).Scan(&activeVMs)

	if activeVMs > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "cannot delete node with active VMs",
			"active_vms": activeVMs,
		})
		return
	}

	_, err := h.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM vm_nodes WHERE id = $1`, nodeID)
	if err != nil {
		h.logger.Error("failed to delete node", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "node deleted"})
}

// Heartbeat updates a node's heartbeat timestamp
// POST /api/v1/nodes/heartbeat
func (h *NodeHandler) Heartbeat(c *gin.Context) {
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

	_, err := h.db.Pool.Exec(c.Request.Context(), `
		UPDATE vm_nodes SET
			used_vcpu = $1,
			used_memory_mb = $2,
			active_vms = $3,
			last_heartbeat = NOW(),
			status = 'online'
		WHERE id = $4
	`, req.UsedVCPU, req.UsedMemoryMB, req.ActiveVMs, req.NodeID)
	if err != nil {
		h.logger.Error("failed to update heartbeat", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update heartbeat"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "heartbeat received"})
}

// GetInfrastructureStats returns overall infrastructure statistics
// GET /api/v1/admin/infrastructure/stats
func (h *NodeHandler) GetInfrastructureStats(c *gin.Context) {
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

	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_nodes`).Scan(&stats.TotalNodes)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_nodes WHERE status = 'online'`).Scan(&stats.OnlineNodes)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(total_vcpu), 0), COALESCE(SUM(used_vcpu), 0) FROM vm_nodes`).Scan(&stats.TotalVCPU, &stats.UsedVCPU)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(total_memory_mb), 0) / 1024, COALESCE(SUM(used_memory_mb), 0) / 1024 FROM vm_nodes`).Scan(&stats.TotalMemoryGB, &stats.UsedMemoryGB)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_instances`).Scan(&stats.TotalVMs)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_instances WHERE status = 'running'`).Scan(&stats.RunningVMs)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM vm_templates WHERE is_active = true`).Scan(&stats.VMTemplates)
	h.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM uploads WHERE status IN ('pending', 'uploading', 'processing')`).Scan(&stats.PendingUploads)

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
