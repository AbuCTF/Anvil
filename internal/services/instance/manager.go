// Package instance provides a unified interface for managing both
// Docker containers and VMs as challenge instances.
package instance

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/anvil-lab/anvil/internal/services/container"
	"github.com/anvil-lab/anvil/internal/services/vm"
	"go.uber.org/zap"
)

// InstanceType represents the type of instance
type InstanceType string

const (
	InstanceTypeDocker InstanceType = "docker"
	InstanceTypeVM     InstanceType = "vm"
)

// InstanceState represents the current state of an instance
type InstanceState string

const (
	StateProvisioning InstanceState = "provisioning"
	StateStarting     InstanceState = "starting"
	StateRunning      InstanceState = "running"
	StatePaused       InstanceState = "paused"
	StateStopping     InstanceState = "stopping"
	StateStopped      InstanceState = "stopped"
	StateError        InstanceState = "error"
	StateExpired      InstanceState = "expired"
	StateDestroyed    InstanceState = "destroyed"
)

// Instance represents a unified instance (Docker or VM)
type Instance struct {
	ID           string            `json:"id"`
	Type         InstanceType      `json:"type"`
	ChallengeID  string            `json:"challenge_id"`
	ResourceID   string            `json:"resource_id"`
	UserID       string            `json:"user_id"`
	Name         string            `json:"name"`
	State        InstanceState     `json:"state"`
	
	// Network info
	IPAddress    string            `json:"ip_address,omitempty"`
	ExposedPorts map[int]int       `json:"exposed_ports,omitempty"` // guest:host mapping
	
	// Resource allocation
	CPU          string            `json:"cpu,omitempty"`
	MemoryMB     int               `json:"memory_mb,omitempty"`
	
	// Access methods
	VNCPort      int               `json:"vnc_port,omitempty"`  // For VMs
	SSHPort      int               `json:"ssh_port,omitempty"`
	
	// Timing
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at"`
	
	// Extensions
	ExtensionsUsed int             `json:"extensions_used"`
	MaxExtensions  int             `json:"max_extensions"`
	
	// Error info
	Error        string            `json:"error,omitempty"`
	
	// Metadata
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// CreateInstanceRequest contains parameters for creating an instance
type CreateInstanceRequest struct {
	ChallengeID string            `json:"challenge_id"`
	ResourceID  string            `json:"resource_id"`
	UserID      string            `json:"user_id"`
	Type        InstanceType      `json:"type"`
	
	// For Docker
	Image       string            `json:"image,omitempty"`
	CPULimit    string            `json:"cpu_limit,omitempty"`
	MemoryLimit string            `json:"memory_limit,omitempty"`
	Ports       []int             `json:"ports,omitempty"`
	
	// For VM
	TemplateID  string            `json:"template_id,omitempty"`
	VCPU        int               `json:"vcpu,omitempty"`
	MemoryMB    int               `json:"memory_mb,omitempty"`
	
	// Common
	Duration    time.Duration     `json:"duration"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Manager provides unified instance management across Docker and VMs
type Manager struct {
	containerSvc *container.Service
	vmSvc        *vm.Service
	logger       *zap.Logger
	config       Config
	
	mu           sync.RWMutex
	instances    map[string]*Instance // Cached instances
}

// Config contains instance manager configuration
type Config struct {
	DefaultDuration   time.Duration
	MaxDuration       time.Duration
	MaxExtensions     int
	ExtensionDuration time.Duration
	MaxPerUser        int
	MaxDockerPerUser  int
	MaxVMPerUser      int
}

// DefaultConfig returns default instance manager configuration
func DefaultConfig() Config {
	return Config{
		DefaultDuration:   2 * time.Hour,
		MaxDuration:       8 * time.Hour,
		MaxExtensions:     3,
		ExtensionDuration: 30 * time.Minute,
		MaxPerUser:        3,
		MaxDockerPerUser:  3,
		MaxVMPerUser:      2,
	}
}

// NewManager creates a new instance manager
func NewManager(containerSvc *container.Service, vmSvc *vm.Service, logger *zap.Logger, config Config) *Manager {
	return &Manager{
		containerSvc: containerSvc,
		vmSvc:        vmSvc,
		logger:       logger,
		config:       config,
		instances:    make(map[string]*Instance),
	}
}

// CreateInstance creates a new instance (Docker or VM)
func (m *Manager) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*Instance, error) {
	// Check user limits
	userInstances := m.getUserInstanceCount(req.UserID)
	if userInstances >= m.config.MaxPerUser {
		return nil, fmt.Errorf("user has reached maximum instances limit (%d)", m.config.MaxPerUser)
	}

	// Set duration
	duration := req.Duration
	if duration == 0 {
		duration = m.config.DefaultDuration
	}
	if duration > m.config.MaxDuration {
		duration = m.config.MaxDuration
	}

	var instance *Instance

	switch req.Type {
	case InstanceTypeDocker:
		inst, err := m.createDockerInstance(ctx, req, duration)
		if err != nil {
			return nil, err
		}
		instance = inst

	case InstanceTypeVM:
		inst, err := m.createVMInstance(ctx, req, duration)
		if err != nil {
			return nil, err
		}
		instance = inst

	default:
		return nil, fmt.Errorf("unsupported instance type: %s", req.Type)
	}

	// Cache instance
	m.mu.Lock()
	m.instances[instance.ID] = instance
	m.mu.Unlock()

	m.logger.Info("instance created",
		zap.String("instance_id", instance.ID),
		zap.String("type", string(instance.Type)),
		zap.String("user_id", req.UserID),
		zap.String("challenge_id", req.ChallengeID),
	)

	return instance, nil
}

func (m *Manager) createDockerInstance(ctx context.Context, req CreateInstanceRequest, duration time.Duration) (*Instance, error) {
	// Check Docker-specific limits
	dockerCount := m.getUserDockerCount(req.UserID)
	if dockerCount >= m.config.MaxDockerPerUser {
		return nil, fmt.Errorf("user has reached maximum Docker instances limit (%d)", m.config.MaxDockerPerUser)
	}

	// Create container
	containerReq := container.CreateContainerRequest{
		Image:       req.Image,
		CPULimit:    req.CPULimit,
		MemoryLimit: req.MemoryLimit,
		ExposePorts: req.Ports,
		UserID:      req.UserID,
		ChallengeID: req.ChallengeID,
		Labels: map[string]string{
			"anvil.resource_id":  req.ResourceID,
			"anvil.challenge_id": req.ChallengeID,
			"anvil.user_id":      req.UserID,
		},
	}

	cont, err := m.containerSvc.CreateContainer(ctx, containerReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	now := time.Now()
	instance := &Instance{
		ID:            cont.ID,
		Type:          InstanceTypeDocker,
		ChallengeID:   req.ChallengeID,
		ResourceID:    req.ResourceID,
		UserID:        req.UserID,
		Name:          cont.Name,
		State:         StateRunning,
		IPAddress:     cont.IPAddress,
		ExposedPorts:  cont.PortMappings,
		CPU:           req.CPULimit,
		MemoryMB:      parseMemoryMB(req.MemoryLimit),
		CreatedAt:     now,
		StartedAt:     &now,
		ExpiresAt:     now.Add(duration),
		MaxExtensions: m.config.MaxExtensions,
		Metadata:      req.Metadata,
	}

	return instance, nil
}

func (m *Manager) createVMInstance(ctx context.Context, req CreateInstanceRequest, duration time.Duration) (*Instance, error) {
	// Check VM-specific limits
	vmCount := m.getUserVMCount(req.UserID)
	if vmCount >= m.config.MaxVMPerUser {
		return nil, fmt.Errorf("user has reached maximum VM instances limit (%d)", m.config.MaxVMPerUser)
	}

	vmReq := vm.CreateVMRequest{
		TemplateID:  req.TemplateID,
		ChallengeID: req.ChallengeID,
		UserID:      req.UserID,
		VCPU:        req.VCPU,
		MemoryMB:    req.MemoryMB,
		Duration:    duration,
		Metadata:    req.Metadata,
	}

	vmInst, err := m.vmSvc.CreateInstance(ctx, vmReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create VM: %w", err)
	}

	instance := &Instance{
		ID:            vmInst.ID,
		Type:          InstanceTypeVM,
		ChallengeID:   vmInst.ChallengeID,
		ResourceID:    req.ResourceID,
		UserID:        vmInst.UserID,
		Name:          vmInst.Name,
		State:         stateFromVMState(vmInst.State),
		IPAddress:     vmInst.IPAddress,
		ExposedPorts:  vmInst.ExposedPorts,
		MemoryMB:      vmInst.MemoryMB,
		VNCPort:       vmInst.VNCPort,
		SSHPort:       vmInst.SSHPort,
		CreatedAt:     vmInst.CreatedAt,
		StartedAt:     vmInst.StartedAt,
		ExpiresAt:     vmInst.ExpiresAt,
		MaxExtensions: m.config.MaxExtensions,
		Metadata:      vmInst.Metadata,
	}

	return instance, nil
}

// GetInstance retrieves an instance by ID
func (m *Manager) GetInstance(ctx context.Context, instanceID string) (*Instance, error) {
	m.mu.RLock()
	instance, exists := m.instances[instanceID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}

	// Refresh state
	switch instance.Type {
	case InstanceTypeDocker:
		cont, err := m.containerSvc.GetContainer(ctx, instanceID)
		if err == nil {
			instance.State = stateFromContainerStatus(cont.Status)
		}
	case InstanceTypeVM:
		vmInst, err := m.vmSvc.GetInstance(ctx, instanceID)
		if err == nil {
			instance.State = stateFromVMState(vmInst.State)
		}
	}

	return instance, nil
}

// StopInstance stops an instance
func (m *Manager) StopInstance(ctx context.Context, instanceID string) error {
	instance, err := m.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	switch instance.Type {
	case InstanceTypeDocker:
		return m.containerSvc.StopContainer(ctx, instanceID)
	case InstanceTypeVM:
		return m.vmSvc.StopInstance(ctx, instanceID)
	}

	return nil
}

// StartInstance starts a stopped instance
func (m *Manager) StartInstance(ctx context.Context, instanceID string) error {
	instance, err := m.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	switch instance.Type {
	case InstanceTypeDocker:
		return m.containerSvc.StartContainer(ctx, instanceID)
	case InstanceTypeVM:
		return m.vmSvc.StartInstance(ctx, instanceID)
	}

	return nil
}

// ResetInstance resets an instance to initial state
func (m *Manager) ResetInstance(ctx context.Context, instanceID string) error {
	instance, err := m.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	switch instance.Type {
	case InstanceTypeDocker:
		// For Docker, we destroy and recreate
		// This would require storing the original create request
		return fmt.Errorf("reset not supported for Docker containers, please destroy and recreate")
	case InstanceTypeVM:
		return m.vmSvc.ResetInstance(ctx, instanceID)
	}

	return nil
}

// DestroyInstance permanently destroys an instance
func (m *Manager) DestroyInstance(ctx context.Context, instanceID string) error {
	instance, err := m.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	var destroyErr error
	switch instance.Type {
	case InstanceTypeDocker:
		destroyErr = m.containerSvc.RemoveContainer(ctx, instanceID)
	case InstanceTypeVM:
		destroyErr = m.vmSvc.DestroyInstance(ctx, instanceID)
	}

	if destroyErr == nil {
		m.mu.Lock()
		delete(m.instances, instanceID)
		m.mu.Unlock()
	}

	return destroyErr
}

// ExtendInstance extends the expiration time
func (m *Manager) ExtendInstance(ctx context.Context, instanceID string) error {
	m.mu.Lock()
	instance, exists := m.instances[instanceID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	if instance.ExtensionsUsed >= instance.MaxExtensions {
		m.mu.Unlock()
		return fmt.Errorf("maximum extensions reached")
	}

	instance.ExpiresAt = instance.ExpiresAt.Add(m.config.ExtensionDuration)
	instance.ExtensionsUsed++
	m.mu.Unlock()

	// Update backend
	if instance.Type == InstanceTypeVM {
		m.vmSvc.ExtendInstance(ctx, instanceID, m.config.ExtensionDuration)
	}

	m.logger.Info("instance extended",
		zap.String("instance_id", instanceID),
		zap.Time("new_expiry", instance.ExpiresAt),
	)

	return nil
}

// ListUserInstances returns all instances for a user
func (m *Manager) ListUserInstances(ctx context.Context, userID string) ([]*Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var instances []*Instance
	for _, inst := range m.instances {
		if inst.UserID == userID {
			instances = append(instances, inst)
		}
	}

	return instances, nil
}

// CleanupExpired destroys expired instances
func (m *Manager) CleanupExpired(ctx context.Context) error {
	m.mu.RLock()
	var expired []string
	now := time.Now()
	for id, inst := range m.instances {
		if now.After(inst.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range expired {
		if err := m.DestroyInstance(ctx, id); err != nil {
			m.logger.Error("failed to destroy expired instance",
				zap.String("instance_id", id),
				zap.Error(err),
			)
		}
	}

	return nil
}

// Status returns the overall status of the instance manager
func (m *Manager) Status() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dockerCount, vmCount, runningCount int
	for _, inst := range m.instances {
		if inst.Type == InstanceTypeDocker {
			dockerCount++
		} else {
			vmCount++
		}
		if inst.State == StateRunning {
			runningCount++
		}
	}

	return map[string]interface{}{
		"total_instances":   len(m.instances),
		"docker_instances":  dockerCount,
		"vm_instances":      vmCount,
		"running_instances": runningCount,
	}
}

// Helper functions

func (m *Manager) getUserInstanceCount(userID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, inst := range m.instances {
		if inst.UserID == userID {
			count++
		}
	}
	return count
}

func (m *Manager) getUserDockerCount(userID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, inst := range m.instances {
		if inst.UserID == userID && inst.Type == InstanceTypeDocker {
			count++
		}
	}
	return count
}

func (m *Manager) getUserVMCount(userID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, inst := range m.instances {
		if inst.UserID == userID && inst.Type == InstanceTypeVM {
			count++
		}
	}
	return count
}

func stateFromVMState(s vm.VMState) InstanceState {
	switch s {
	case vm.VMStateProvisioning:
		return StateProvisioning
	case vm.VMStateRunning:
		return StateRunning
	case vm.VMStatePaused:
		return StatePaused
	case vm.VMStateShutdown, vm.VMStateShutoff:
		return StateStopped
	case vm.VMStateCrashed, vm.VMStateError:
		return StateError
	default:
		return StateProvisioning
	}
}

func stateFromContainerStatus(status string) InstanceState {
	switch status {
	case "running":
		return StateRunning
	case "paused":
		return StatePaused
	case "exited", "stopped":
		return StateStopped
	case "created":
		return StateProvisioning
	default:
		return StateProvisioning
	}
}

func parseMemoryMB(memLimit string) int {
	// Parse memory limit string like "512m", "1g" to MB
	if memLimit == "" {
		return 0
	}
	// Simple parsing - in production use a proper parser
	var value int
	var unit string
	fmt.Sscanf(memLimit, "%d%s", &value, &unit)
	
	switch unit {
	case "g", "G":
		return value * 1024
	case "m", "M", "":
		return value
	default:
		return value
	}
}
