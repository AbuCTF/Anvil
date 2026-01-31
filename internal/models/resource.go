package models

import (
	"encoding/json"
	"time"
)

// ResourceType represents the type of challenge resource
type ResourceType string

const (
	ResourceTypeDocker ResourceType = "docker"
	ResourceTypeVM     ResourceType = "vm"
)

// ImageFormat represents VM image formats
type ImageFormat string

const (
	ImageFormatOVA   ImageFormat = "ova"
	ImageFormatVMDK  ImageFormat = "vmdk"
	ImageFormatQCOW2 ImageFormat = "qcow2"
	ImageFormatVDI   ImageFormat = "vdi"
	ImageFormatRAW   ImageFormat = "raw"
	ImageFormatISO   ImageFormat = "iso"
)

// ChallengeResource links a challenge to its Docker or VM configuration
type ChallengeResource struct {
	ID          string       `json:"id" db:"id"`
	ChallengeID string       `json:"challenge_id" db:"challenge_id"`
	
	// Resource type
	ResourceType ResourceType `json:"resource_type" db:"resource_type"`
	
	// Docker configuration
	DockerImage         *string `json:"docker_image,omitempty" db:"docker_image"`
	DockerRegistry      *string `json:"docker_registry,omitempty" db:"docker_registry"`
	DockerTag           *string `json:"docker_tag,omitempty" db:"docker_tag"`
	DockerfileUploadID  *string `json:"dockerfile_upload_id,omitempty" db:"dockerfile_upload_id"`
	
	// VM configuration
	VMTemplateID *string `json:"vm_template_id,omitempty" db:"vm_template_id"`
	
	// Resource limits (override challenge defaults)
	CPULimit    *string `json:"cpu_limit,omitempty" db:"cpu_limit"`
	MemoryLimit *string `json:"memory_limit,omitempty" db:"memory_limit"`
	
	// Network configuration
	ExposedPorts json.RawMessage `json:"exposed_ports,omitempty" db:"exposed_ports"`
	
	// Ordering
	SortOrder int `json:"sort_order" db:"sort_order"`
	
	// Status
	IsActive bool `json:"is_active" db:"is_active"`
	
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// PortConfig represents an exposed port configuration
type PortConfig struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"` // tcp, udp
	Name     string `json:"name,omitempty"`     // HTTP, SSH, etc.
}

// GetExposedPorts parses and returns the exposed ports
func (r *ChallengeResource) GetExposedPorts() ([]PortConfig, error) {
	if r.ExposedPorts == nil {
		return nil, nil
	}
	var ports []PortConfig
	if err := json.Unmarshal(r.ExposedPorts, &ports); err != nil {
		return nil, err
	}
	return ports, nil
}

// VMTemplate represents a VM template from an uploaded image
type VMTemplate struct {
	ID          string `json:"id" db:"id"`
	UploadID    *string `json:"upload_id,omitempty" db:"upload_id"`
	
	// Basic info
	Name        string `json:"name" db:"name"`
	Slug        string `json:"slug" db:"slug"`
	Description string `json:"description" db:"description"`
	
	// Image info
	ImagePath       string      `json:"image_path" db:"image_path"`
	OriginalFormat  ImageFormat `json:"original_format" db:"original_format"`
	OriginalPath    *string     `json:"original_path,omitempty" db:"original_path"`
	ImageSize       int64       `json:"image_size" db:"image_size"`
	
	// VM specifications
	VCPU     int `json:"vcpu" db:"vcpu"`
	MemoryMB int `json:"memory_mb" db:"memory_mb"`
	DiskGB   int `json:"disk_gb" db:"disk_gb"`
	
	// OS info
	OSType    *string `json:"os_type,omitempty" db:"os_type"`       // linux, windows
	OSVariant *string `json:"os_variant,omitempty" db:"os_variant"` // ubuntu20.04, etc.
	OSName    *string `json:"os_name,omitempty" db:"os_name"`       // Display name
	
	// Hardware requirements
	RequiresKVM        bool `json:"requires_kvm" db:"requires_kvm"`
	RequiresNestedVirt bool `json:"requires_nested_virt" db:"requires_nested_virt"`
	GPURequired        bool `json:"gpu_required" db:"gpu_required"`
	
	// Network
	NetworkMode     string          `json:"network_mode" db:"network_mode"` // nat, bridge, isolated
	ExposedServices json.RawMessage `json:"exposed_services,omitempty" db:"exposed_services"`
	
	// Ownership
	AuthorID *string `json:"author_id,omitempty" db:"author_id"`
	IsPublic bool    `json:"is_public" db:"is_public"`
	IsActive bool    `json:"is_active" db:"is_active"`
	
	// Metadata
	Metadata json.RawMessage `json:"metadata,omitempty" db:"metadata"`
	
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// ServiceConfig represents an exposed service in a VM template
type ServiceConfig struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}

// GetExposedServices parses and returns the exposed services
func (t *VMTemplate) GetExposedServices() ([]ServiceConfig, error) {
	if t.ExposedServices == nil {
		return nil, nil
	}
	var services []ServiceConfig
	if err := json.Unmarshal(t.ExposedServices, &services); err != nil {
		return nil, err
	}
	return services, nil
}

// VMInstanceStatus represents VM instance states
type VMInstanceStatus string

const (
	VMStatusProvisioning VMInstanceStatus = "provisioning"
	VMStatusStarting     VMInstanceStatus = "starting"
	VMStatusRunning      VMInstanceStatus = "running"
	VMStatusPaused       VMInstanceStatus = "paused"
	VMStatusStopping     VMInstanceStatus = "stopping"
	VMStatusStopped      VMInstanceStatus = "stopped"
	VMStatusError        VMInstanceStatus = "error"
	VMStatusExpired      VMInstanceStatus = "expired"
	VMStatusDestroyed    VMInstanceStatus = "destroyed"
)

// VMInstance represents a running VM instance
type VMInstance struct {
	ID           string           `json:"id" db:"id"`
	ChallengeID  string           `json:"challenge_id" db:"challenge_id"`
	ResourceID   string           `json:"resource_id" db:"resource_id"`
	VMTemplateID string           `json:"vm_template_id" db:"vm_template_id"`
	UserID       *string          `json:"user_id,omitempty" db:"user_id"`
	SessionID    *string          `json:"session_id,omitempty" db:"session_id"`
	
	// Instance details
	Name   string           `json:"name" db:"name"`
	Status VMInstanceStatus `json:"status" db:"status"`
	
	// Disk
	OverlayPath *string `json:"overlay_path,omitempty" db:"overlay_path"`
	
	// Resources
	VCPU     int `json:"vcpu" db:"vcpu"`
	MemoryMB int `json:"memory_mb" db:"memory_mb"`
	
	// Network
	NetworkID     *string         `json:"network_id,omitempty" db:"network_id"`
	IPAddress     *string         `json:"ip_address,omitempty" db:"ip_address"`
	MACAddress    *string         `json:"mac_address,omitempty" db:"mac_address"`
	AssignedPorts json.RawMessage `json:"assigned_ports,omitempty" db:"assigned_ports"`
	
	// Access
	VNCPort     *int    `json:"vnc_port,omitempty" db:"vnc_port"`
	VNCPassword *string `json:"vnc_password,omitempty" db:"vnc_password"`
	SSHPort     *int    `json:"ssh_port,omitempty" db:"ssh_port"`
	
	// Timing
	StartedAt      *time.Time `json:"started_at,omitempty" db:"started_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty" db:"stopped_at"`
	ExtensionsUsed int        `json:"extensions_used" db:"extensions_used"`
	
	// Error tracking
	ErrorMessage *string `json:"error_message,omitempty" db:"error_message"`
	
	// Host info
	HostNode *string `json:"host_node,omitempty" db:"host_node"`
	
	// Metadata
	Metadata json.RawMessage `json:"metadata,omitempty" db:"metadata"`
	
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// GetAssignedPorts parses and returns port mappings
func (v *VMInstance) GetAssignedPorts() (map[int]int, error) {
	if v.AssignedPorts == nil {
		return nil, nil
	}
	var ports map[int]int
	if err := json.Unmarshal(v.AssignedPorts, &ports); err != nil {
		return nil, err
	}
	return ports, nil
}

// BuildStatus represents Docker build states
type BuildStatus string

const (
	BuildStatusQueued    BuildStatus = "queued"
	BuildStatusBuilding  BuildStatus = "building"
	BuildStatusPushing   BuildStatus = "pushing"
	BuildStatusCompleted BuildStatus = "completed"
	BuildStatusFailed    BuildStatus = "failed"
	BuildStatusCancelled BuildStatus = "cancelled"
)

// DockerBuild represents a Docker image build job
type DockerBuild struct {
	ID          string      `json:"id" db:"id"`
	ChallengeID *string     `json:"challenge_id,omitempty" db:"challenge_id"`
	ResourceID  *string     `json:"resource_id,omitempty" db:"resource_id"`
	UploadID    string      `json:"upload_id" db:"upload_id"`
	
	// Build info
	ImageName string `json:"image_name" db:"image_name"`
	ImageTag  string `json:"image_tag" db:"image_tag"`
	
	// Status
	Status BuildStatus `json:"status" db:"status"`
	
	// Build details
	BuildArgs json.RawMessage `json:"build_args,omitempty" db:"build_args"`
	BuildLog  *string         `json:"build_log,omitempty" db:"build_log"`
	
	// Results
	ImageDigest *string `json:"image_digest,omitempty" db:"image_digest"`
	ImageSize   *int64  `json:"image_size,omitempty" db:"image_size"`
	
	// Timing
	QueuedAt    time.Time  `json:"queued_at" db:"queued_at"`
	StartedAt   *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	
	// Error
	ErrorMessage *string `json:"error_message,omitempty" db:"error_message"`
	
	// Triggered by
	TriggeredBy *string `json:"triggered_by,omitempty" db:"triggered_by"`
	
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// GetBuildArgs parses and returns build arguments
func (b *DockerBuild) GetBuildArgs() (map[string]string, error) {
	if b.BuildArgs == nil {
		return nil, nil
	}
	var args map[string]string
	if err := json.Unmarshal(b.BuildArgs, &args); err != nil {
		return nil, err
	}
	return args, nil
}

// CreateChallengeResourceRequest is used to add a resource to a challenge
type CreateChallengeResourceRequest struct {
	ChallengeID string       `json:"challenge_id" binding:"required"`
	Type        ResourceType `json:"type" binding:"required"`
	
	// Docker options
	DockerImage    string `json:"docker_image,omitempty"`
	DockerRegistry string `json:"docker_registry,omitempty"`
	DockerTag      string `json:"docker_tag,omitempty"`
	DockerfileUploadID string `json:"dockerfile_upload_id,omitempty"`
	
	// VM options
	VMTemplateID string `json:"vm_template_id,omitempty"`
	
	// Resource limits
	CPULimit    string `json:"cpu_limit,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty"`
	
	// Ports
	ExposedPorts []PortConfig `json:"exposed_ports,omitempty"`
}

// CreateVMTemplateRequest is used to register a new VM template
type CreateVMTemplateRequest struct {
	UploadID    string `json:"upload_id" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description,omitempty"`
	
	// VM specs
	VCPU     int `json:"vcpu,omitempty"`
	MemoryMB int `json:"memory_mb,omitempty"`
	
	// OS info
	OSType    string `json:"os_type,omitempty"`
	OSVariant string `json:"os_variant,omitempty"`
	OSName    string `json:"os_name,omitempty"`
	
	// Network
	NetworkMode     string          `json:"network_mode,omitempty"`
	ExposedServices []ServiceConfig `json:"exposed_services,omitempty"`
	
	// Visibility
	IsPublic bool `json:"is_public,omitempty"`
}
