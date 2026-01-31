package models

import (
	"time"

	"github.com/google/uuid"
)

// VPNConfig represents a user's VPN configuration
type VPNConfig struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	UserID    *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID *uuid.UUID `json:"session_id,omitempty" db:"session_id"`

	// WireGuard keys
	PrivateKey string `json:"-" db:"private_key"` // Never expose
	PublicKey  string `json:"public_key" db:"public_key"`

	// Assigned IP
	AssignedIP string `json:"assigned_ip" db:"assigned_ip"`

	// Status
	IsActive      bool       `json:"is_active" db:"is_active"`
	LastHandshake *time.Time `json:"last_handshake,omitempty" db:"last_handshake"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// VPNConfigResponse represents the VPN configuration file content
type VPNConfigResponse struct {
	ConfigFile string `json:"config_file"`
	AssignedIP string `json:"assigned_ip"`
	PublicKey  string `json:"public_key"`
	ServerPublicKey string `json:"server_public_key"`
	Endpoint   string `json:"endpoint"`
	DNS        string `json:"dns"`
}

// GenerateWireGuardConfig generates the WireGuard configuration file content
func (v *VPNConfig) GenerateWireGuardConfig(serverPublicKey, endpoint, dns, allowedIPs string) string {
	return `[Interface]
PrivateKey = ` + v.PrivateKey + `
Address = ` + v.AssignedIP + `/32
DNS = ` + dns + `

[Peer]
PublicKey = ` + serverPublicKey + `
AllowedIPs = ` + allowedIPs + `
Endpoint = ` + endpoint + `
PersistentKeepalive = 25
`
}

// AuditLogEntry represents an entry in the audit log
type AuditLogEntry struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	UserID     *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	Action     string     `json:"action" db:"action"`
	EntityType *string    `json:"entity_type,omitempty" db:"entity_type"`
	EntityID   *uuid.UUID `json:"entity_id,omitempty" db:"entity_id"`
	OldValues  *string    `json:"old_values,omitempty" db:"old_values"`
	NewValues  *string    `json:"new_values,omitempty" db:"new_values"`
	IPAddress  *string    `json:"ip_address,omitempty" db:"ip_address"`
	UserAgent  *string    `json:"user_agent,omitempty" db:"user_agent"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}

// PlatformSetting represents a platform configuration setting
type PlatformSetting struct {
	Key         string     `json:"key" db:"key"`
	Value       string     `json:"value" db:"value"`
	Description *string    `json:"description,omitempty" db:"description"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	UpdatedBy   *uuid.UUID `json:"updated_by,omitempty" db:"updated_by"`
}

// Common audit actions
const (
	AuditActionUserRegistered     = "user.registered"
	AuditActionUserLogin          = "user.login"
	AuditActionUserLogout         = "user.logout"
	AuditActionUserUpdated        = "user.updated"
	AuditActionUserBanned         = "user.banned"
	AuditActionUserUnbanned       = "user.unbanned"

	AuditActionChallengeCreated   = "challenge.created"
	AuditActionChallengeUpdated   = "challenge.updated"
	AuditActionChallengeDeleted   = "challenge.deleted"
	AuditActionChallengePublished = "challenge.published"

	AuditActionFlagSubmitted      = "flag.submitted"
	AuditActionFlagSolved         = "flag.solved"
	AuditActionFirstBlood         = "flag.first_blood"

	AuditActionInstanceStarted    = "instance.started"
	AuditActionInstanceStopped    = "instance.stopped"
	AuditActionInstanceExtended   = "instance.extended"
	AuditActionInstanceExpired    = "instance.expired"

	AuditActionVPNConfigGenerated = "vpn.config_generated"
	AuditActionVPNConnected       = "vpn.connected"
	AuditActionVPNDisconnected    = "vpn.disconnected"

	AuditActionHintUnlocked       = "hint.unlocked"

	AuditActionSettingUpdated     = "setting.updated"
)
