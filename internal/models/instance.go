package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Instance status
type InstanceStatus string

const (
	InstancePending  InstanceStatus = "pending"
	InstanceCreating InstanceStatus = "creating"
	InstanceRunning  InstanceStatus = "running"
	InstanceStopping InstanceStatus = "stopping"
	InstanceStopped  InstanceStatus = "stopped"
	InstanceFailed   InstanceStatus = "failed"
	InstanceExpired  InstanceStatus = "expired"
)

// PortMapping represents a port mapping from container to host
type PortMapping map[string]int // {"80": 32001, "22": 32002}

// Instance represents a running challenge container
type Instance struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	ChallengeID uuid.UUID  `json:"challenge_id" db:"challenge_id"`
	UserID      *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID   *uuid.UUID `json:"session_id,omitempty" db:"session_id"`

	// Container details
	ContainerID   *string        `json:"container_id,omitempty" db:"container_id"`
	ContainerName *string        `json:"container_name,omitempty" db:"container_name"`
	Status        InstanceStatus `json:"status" db:"status"`

	// Network
	IPAddress         *string         `json:"ip_address,omitempty" db:"ip_address"`
	AssignedPortsJSON json.RawMessage `json:"-" db:"assigned_ports"`
	AssignedPorts     PortMapping     `json:"assigned_ports" db:"-"`

	// Timing
	StartedAt      *time.Time `json:"started_at,omitempty" db:"started_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty" db:"stopped_at"`
	ExtensionsUsed int        `json:"extensions_used" db:"extensions_used"`

	// Error tracking
	ErrorMessage *string `json:"error_message,omitempty" db:"error_message"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// Relationships
	Challenge *Challenge `json:"challenge,omitempty" db:"-"`
}

// ParseAssignedPorts parses the JSONB assigned_ports field
func (i *Instance) ParseAssignedPorts() error {
	if i.AssignedPortsJSON != nil {
		return json.Unmarshal(i.AssignedPortsJSON, &i.AssignedPorts)
	}
	i.AssignedPorts = make(PortMapping)
	return nil
}

// TimeRemaining returns the duration until the instance expires
func (i *Instance) TimeRemaining() time.Duration {
	if i.ExpiresAt == nil {
		return 0
	}
	remaining := time.Until(*i.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsExpired checks if the instance has expired
func (i *Instance) IsExpired() bool {
	if i.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*i.ExpiresAt)
}

// CanExtend checks if the instance can be extended
func (i *Instance) CanExtend(maxExtensions int) bool {
	return i.Status == InstanceRunning && i.ExtensionsUsed < maxExtensions
}

// InstanceCreateRequest represents a request to create an instance
type InstanceCreateRequest struct {
	ChallengeID uuid.UUID `json:"challenge_id" binding:"required"`
}

// InstanceResponse represents the API response for an instance
type InstanceResponse struct {
	ID            uuid.UUID      `json:"id"`
	ChallengeID   uuid.UUID      `json:"challenge_id"`
	ChallengeName string         `json:"challenge_name"`
	Status        InstanceStatus `json:"status"`
	IPAddress     *string        `json:"ip_address,omitempty"`
	Ports         PortMapping    `json:"ports,omitempty"`
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	TimeRemaining int            `json:"time_remaining_seconds"`
	ExtensionsUsed int           `json:"extensions_used"`
	MaxExtensions  int           `json:"max_extensions"`
	CanExtend     bool           `json:"can_extend"`
	ErrorMessage  *string        `json:"error_message,omitempty"`
}
