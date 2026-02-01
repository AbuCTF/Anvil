package models

import (
	"time"

	"github.com/google/uuid"
)

// NodeStatus represents the status of a VM node
type NodeStatus string

const (
	NodeStatusOnline      NodeStatus = "online"
	NodeStatusOffline     NodeStatus = "offline"
	NodeStatusMaintenance NodeStatus = "maintenance"
	NodeStatusDraining    NodeStatus = "draining"
)

// VMNode represents a worker node that can run VMs
type VMNode struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Hostname  string    `json:"hostname" db:"hostname"`
	IPAddress string    `json:"ip_address" db:"ip_address"`

	// Connection details
	SSHPort     int     `json:"ssh_port" db:"ssh_port"`
	SSHUser     string  `json:"ssh_user" db:"ssh_user"`
	SSHKeyPath  *string `json:"-" db:"ssh_key_path"` // Never expose
	LibvirtURI  string  `json:"libvirt_uri" db:"libvirt_uri"`
	APIEndpoint *string `json:"api_endpoint,omitempty" db:"api_endpoint"`

	// Capacity
	TotalVCPU     int `json:"total_vcpu" db:"total_vcpu"`
	TotalMemoryMB int `json:"total_memory_mb" db:"total_memory_mb"`
	TotalDiskGB   int `json:"total_disk_gb" db:"total_disk_gb"`

	// Current usage
	UsedVCPU     int `json:"used_vcpu" db:"used_vcpu"`
	UsedMemoryMB int `json:"used_memory_mb" db:"used_memory_mb"`
	UsedDiskGB   int `json:"used_disk_gb" db:"used_disk_gb"`
	ActiveVMs    int `json:"active_vms" db:"active_vms"`

	// Limits
	MaxVMs           int `json:"max_vms" db:"max_vms"`
	ReservedVCPU     int `json:"reserved_vcpu" db:"reserved_vcpu"`
	ReservedMemoryMB int `json:"reserved_memory_mb" db:"reserved_memory_mb"`

	// Network config
	VMNetworkName string  `json:"vm_network_name" db:"vm_network_name"`
	VMSubnet      *string `json:"vm_subnet,omitempty" db:"vm_subnet"`

	// VNC port range
	VNCPortStart int `json:"vnc_port_start" db:"vnc_port_start"`
	VNCPortEnd   int `json:"vnc_port_end" db:"vnc_port_end"`

	// Status
	Status           NodeStatus `json:"status" db:"status"`
	LastHeartbeat    *time.Time `json:"last_heartbeat,omitempty" db:"last_heartbeat"`
	LastHealthCheck  *time.Time `json:"last_health_check,omitempty" db:"last_health_check"`
	HealthCheckError *string    `json:"health_check_error,omitempty" db:"health_check_error"`

	// Primary node flag
	IsPrimary bool `json:"is_primary" db:"is_primary"`
	Priority  int  `json:"priority" db:"priority"`

	// Location
	Region   *string `json:"region,omitempty" db:"region"`
	Zone     *string `json:"zone,omitempty" db:"zone"`
	Provider *string `json:"provider,omitempty" db:"provider"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// AvailableVCPU returns the available vCPU count
func (n *VMNode) AvailableVCPU() int {
	return n.TotalVCPU - n.UsedVCPU - n.ReservedVCPU
}

// AvailableMemoryMB returns the available memory in MB
func (n *VMNode) AvailableMemoryMB() int {
	return n.TotalMemoryMB - n.UsedMemoryMB - n.ReservedMemoryMB
}

// CanAcceptVM checks if the node can accept a new VM with given requirements
func (n *VMNode) CanAcceptVM(vcpu, memoryMB int) bool {
	if n.Status != NodeStatusOnline {
		return false
	}
	if n.ActiveVMs >= n.MaxVMs {
		return false
	}
	if n.AvailableVCPU() < vcpu {
		return false
	}
	if n.AvailableMemoryMB() < memoryMB {
		return false
	}
	return true
}

// NodeHealth represents a health check snapshot
type NodeHealth struct {
	ID     uuid.UUID `json:"id" db:"id"`
	NodeID uuid.UUID `json:"node_id" db:"node_id"`

	// Resource usage
	UsedVCPU     int `json:"used_vcpu" db:"used_vcpu"`
	UsedMemoryMB int `json:"used_memory_mb" db:"used_memory_mb"`
	UsedDiskGB   int `json:"used_disk_gb" db:"used_disk_gb"`
	ActiveVMs    int `json:"active_vms" db:"active_vms"`

	// System metrics
	LoadAverage   *float64 `json:"load_average,omitempty" db:"load_average"`
	CPUPercent    *float64 `json:"cpu_percent,omitempty" db:"cpu_percent"`
	MemoryPercent *float64 `json:"memory_percent,omitempty" db:"memory_percent"`
	DiskPercent   *float64 `json:"disk_percent,omitempty" db:"disk_percent"`

	// Network metrics
	NetworkRxBytes int64 `json:"network_rx_bytes" db:"network_rx_bytes"`
	NetworkTxBytes int64 `json:"network_tx_bytes" db:"network_tx_bytes"`

	// Status
	Status       NodeStatus `json:"status" db:"status"`
	IsHealthy    bool       `json:"is_healthy" db:"is_healthy"`
	ErrorMessage *string    `json:"error_message,omitempty" db:"error_message"`

	RecordedAt time.Time `json:"recorded_at" db:"recorded_at"`
}

// UserCooldown tracks cooldown periods for users
type UserCooldown struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	ChallengeID   uuid.UUID  `json:"challenge_id" db:"challenge_id"`
	CooldownUntil time.Time  `json:"cooldown_until" db:"cooldown_until"`
	Reason        string     `json:"reason" db:"reason"`
	InstanceID    *uuid.UUID `json:"instance_id,omitempty" db:"instance_id"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// IsActive checks if the cooldown is still active
func (c *UserCooldown) IsActive() bool {
	return time.Now().Before(c.CooldownUntil)
}

// RemainingSeconds returns the remaining cooldown time in seconds
func (c *UserCooldown) RemainingSeconds() int {
	if !c.IsActive() {
		return 0
	}
	return int(time.Until(c.CooldownUntil).Seconds())
}

// ChallengeTimerConfig represents timer settings for a challenge
type ChallengeTimerConfig struct {
	TimeoutMinutes   int `json:"timeout_minutes"`
	MaxExtensions    int `json:"max_extensions"`
	ExtensionMinutes int `json:"extension_minutes"`
	CooldownMinutes  int `json:"cooldown_minutes"`
}

// DefaultTimerConfig returns default timer config based on difficulty
func DefaultTimerConfig(difficulty ChallengeDifficulty) ChallengeTimerConfig {
	switch difficulty {
	case DifficultyEasy:
		return ChallengeTimerConfig{
			TimeoutMinutes:   60,
			MaxExtensions:    2,
			ExtensionMinutes: 30,
			CooldownMinutes:  5,
		}
	case DifficultyMedium:
		return ChallengeTimerConfig{
			TimeoutMinutes:   120,
			MaxExtensions:    3,
			ExtensionMinutes: 30,
			CooldownMinutes:  10,
		}
	case DifficultyHard:
		return ChallengeTimerConfig{
			TimeoutMinutes:   180,
			MaxExtensions:    3,
			ExtensionMinutes: 30,
			CooldownMinutes:  10,
		}
	case DifficultyInsane:
		return ChallengeTimerConfig{
			TimeoutMinutes:   240,
			MaxExtensions:    4,
			ExtensionMinutes: 30,
			CooldownMinutes:  15,
		}
	default:
		return ChallengeTimerConfig{
			TimeoutMinutes:   120,
			MaxExtensions:    2,
			ExtensionMinutes: 30,
			CooldownMinutes:  10,
		}
	}
}
