package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/anvil-lab/anvil/internal/services/vm"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// VMHandler handles VM-related API endpoints
type VMHandler struct {
	vmService *vm.Service
	logger    *zap.Logger
}

// NewVMHandler creates a new VM handler
func NewVMHandler(vmService *vm.Service, logger *zap.Logger) *VMHandler {
	return &VMHandler{
		vmService: vmService,
		logger:    logger,
	}
}

// CreateVMRequest is the API request for creating a VM
type CreateVMRequest struct {
	TemplateID  string `json:"template_id" binding:"required"`
	ChallengeID string `json:"challenge_id" binding:"required"`
	VCPU        int    `json:"vcpu,omitempty"`
	MemoryMB    int    `json:"memory_mb,omitempty"`
	Duration    string `json:"duration,omitempty"` // e.g., "2h", "4h"
}

// VMResponse is the API response for VM operations
type VMResponse struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	TemplateID   string      `json:"template_id"`
	ChallengeID  string      `json:"challenge_id"`
	Status       string      `json:"status"`
	IPAddress    string      `json:"ip_address,omitempty"`
	VNCPort      int         `json:"vnc_port,omitempty"`
	SSHPort      int         `json:"ssh_port,omitempty"`
	ExposedPorts map[int]int `json:"exposed_ports,omitempty"`
	VCPU         int         `json:"vcpu"`
	MemoryMB     int         `json:"memory_mb"`
	CreatedAt    string      `json:"created_at"`
	StartedAt    *string     `json:"started_at,omitempty"`
	ExpiresAt    string      `json:"expires_at"`
}

// CreateVM creates a new VM instance
// POST /api/v1/vms
func (h *VMHandler) CreateVM(c *gin.Context) {
	var req CreateVMRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Parse duration if provided
	var duration time.Duration
	if req.Duration != "" {
		var err error
		duration, err = time.ParseDuration(req.Duration)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid duration format"})
			return
		}
	}

	vmReq := vm.CreateVMRequest{
		Name:        "", // Auto-generated
		TemplateID:  req.TemplateID,
		ChallengeID: req.ChallengeID,
		UserID:      userID.(string),
		VCPU:        req.VCPU,
		MemoryMB:    req.MemoryMB,
		Duration:    duration,
	}

	instance, err := h.vmService.CreateInstance(c.Request.Context(), vmReq)
	if err != nil {
		h.logger.Error("failed to create VM",
			zap.String("user_id", userID.(string)),
			zap.String("template_id", req.TemplateID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, vmInstanceToResponse(instance))
}

// GetVM returns details about a specific VM
// GET /api/v1/vms/:id
func (h *VMHandler) GetVM(c *gin.Context) {
	instanceID := c.Param("id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instance, err := h.vmService.GetInstance(c.Request.Context(), instanceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not found"})
		return
	}

	// Check ownership (unless admin)
	role, _ := c.Get("role")
	if instance.UserID != userID.(string) && role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	c.JSON(http.StatusOK, vmInstanceToResponse(instance))
}

// ListUserVMs returns all VMs for the current user
// GET /api/v1/vms
func (h *VMHandler) ListUserVMs(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instances, err := h.vmService.ListUserInstances(c.Request.Context(), userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var responses []VMResponse
	for _, inst := range instances {
		responses = append(responses, vmInstanceToResponse(inst))
	}

	c.JSON(http.StatusOK, gin.H{
		"vms":   responses,
		"count": len(responses),
	})
}

// StartVM starts a stopped VM
// POST /api/v1/vms/:id/start
func (h *VMHandler) StartVM(c *gin.Context) {
	instanceID := c.Param("id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instance, err := h.vmService.GetInstance(c.Request.Context(), instanceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not found"})
		return
	}

	if instance.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	if err := h.vmService.StartInstance(c.Request.Context(), instanceID); err != nil {
		h.logger.Error("failed to start VM", zap.String("id", instanceID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	instance, _ = h.vmService.GetInstance(c.Request.Context(), instanceID)
	c.JSON(http.StatusOK, vmInstanceToResponse(instance))
}

// StopVM stops a running VM
// POST /api/v1/vms/:id/stop
func (h *VMHandler) StopVM(c *gin.Context) {
	instanceID := c.Param("id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instance, err := h.vmService.GetInstance(c.Request.Context(), instanceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not found"})
		return
	}

	if instance.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	if err := h.vmService.StopInstance(c.Request.Context(), instanceID); err != nil {
		h.logger.Error("failed to stop VM", zap.String("id", instanceID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	instance, _ = h.vmService.GetInstance(c.Request.Context(), instanceID)
	c.JSON(http.StatusOK, vmInstanceToResponse(instance))
}

// ResetVM resets a VM to its initial state
// POST /api/v1/vms/:id/reset
func (h *VMHandler) ResetVM(c *gin.Context) {
	instanceID := c.Param("id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instance, err := h.vmService.GetInstance(c.Request.Context(), instanceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not found"})
		return
	}

	if instance.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	// Check reset limit from metadata (stored during creation)
	maxResets := 3 // default
	resetCount := 0
	if val, ok := instance.Metadata["reset_count"]; ok {
		fmt.Sscanf(val, "%d", &resetCount)
	}
	if val, ok := instance.Metadata["max_resets"]; ok {
		fmt.Sscanf(val, "%d", &maxResets)
	}

	if resetCount >= maxResets {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":       "reset limit reached",
			"reset_count": resetCount,
			"max_resets":  maxResets,
		})
		return
	}

	if err := h.vmService.ResetInstance(c.Request.Context(), instanceID); err != nil {
		h.logger.Error("failed to reset VM", zap.String("id", instanceID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Increment reset counter
	instance.Metadata["reset_count"] = fmt.Sprintf("%d", resetCount+1)

	instance, _ = h.vmService.GetInstance(c.Request.Context(), instanceID)
	c.JSON(http.StatusOK, vmInstanceToResponse(instance))
}

// ExtendVM extends the VM's expiration time
// POST /api/v1/vms/:id/extend
func (h *VMHandler) ExtendVM(c *gin.Context) {
	instanceID := c.Param("id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instance, err := h.vmService.GetInstance(c.Request.Context(), instanceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not found"})
		return
	}

	if instance.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	var req struct {
		Duration string `json:"duration"` // e.g., "1h", "30m"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Duration = "1h" // Default 1 hour extension
	}

	duration, err := time.ParseDuration(req.Duration)
	if err != nil {
		duration = time.Hour
	}

	if err := h.vmService.ExtendInstance(c.Request.Context(), instanceID, duration); err != nil {
		h.logger.Error("failed to extend VM", zap.String("id", instanceID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	instance, _ = h.vmService.GetInstance(c.Request.Context(), instanceID)
	c.JSON(http.StatusOK, vmInstanceToResponse(instance))
}

// DestroyVM permanently destroys a VM
// DELETE /api/v1/vms/:id
func (h *VMHandler) DestroyVM(c *gin.Context) {
	instanceID := c.Param("id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	instance, err := h.vmService.GetInstance(c.Request.Context(), instanceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "VM not found"})
		return
	}

	if instance.UserID != userID.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	if err := h.vmService.DestroyInstance(c.Request.Context(), instanceID); err != nil {
		h.logger.Error("failed to destroy VM", zap.String("id", instanceID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "VM destroyed"})
}

// ListTemplates returns all available VM templates
// GET /api/v1/vms/templates
func (h *VMHandler) ListTemplates(c *gin.Context) {
	templates, err := h.vmService.ListTemplates(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var responses []gin.H
	for _, t := range templates {
		responses = append(responses, gin.H{
			"id":          t.ID,
			"name":        t.Name,
			"description": t.Description,
			"os":          t.OS,
			"vcpu":        t.VCPU,
			"memory_mb":   t.MemoryMB,
			"disk_gb":     t.DiskGB,
			"created_at":  t.CreatedAt.Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"templates": responses,
		"count":     len(responses),
	})
}

// GetTemplate returns details about a specific template
// GET /api/v1/vms/templates/:id
func (h *VMHandler) GetTemplate(c *gin.Context) {
	templateID := c.Param("id")

	template, err := h.vmService.GetTemplate(c.Request.Context(), templateID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          template.ID,
		"name":        template.Name,
		"description": template.Description,
		"os":          template.OS,
		"vcpu":        template.VCPU,
		"memory_mb":   template.MemoryMB,
		"disk_gb":     template.DiskGB,
		"metadata":    template.Metadata,
		"created_at":  template.CreatedAt.Format(time.RFC3339),
		"updated_at":  template.UpdatedAt.Format(time.RFC3339),
	})
}

// Helper function to convert VMInstance to API response
func vmInstanceToResponse(inst *vm.VMInstance) VMResponse {
	resp := VMResponse{
		ID:           inst.ID,
		Name:         inst.Name,
		TemplateID:   inst.TemplateID,
		ChallengeID:  inst.ChallengeID,
		Status:       string(inst.State),
		IPAddress:    inst.IPAddress,
		VNCPort:      inst.VNCPort,
		SSHPort:      inst.SSHPort,
		ExposedPorts: inst.ExposedPorts,
		VCPU:         inst.VCPU,
		MemoryMB:     inst.MemoryMB,
		CreatedAt:    inst.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    inst.ExpiresAt.Format(time.RFC3339),
	}

	if inst.StartedAt != nil {
		s := inst.StartedAt.Format(time.RFC3339)
		resp.StartedAt = &s
	}

	return resp
}
