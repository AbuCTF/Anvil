package models

import (
	"time"

	"github.com/google/uuid"
)

// User roles
type UserRole string

const (
	RoleUser   UserRole = "user"
	RoleAuthor UserRole = "author"
	RoleAdmin  UserRole = "admin"
)

// User status
type UserStatus string

const (
	StatusActive    UserStatus = "active"
	StatusSuspended UserStatus = "suspended"
	StatusBanned    UserStatus = "banned"
)

// User represents a platform user
type User struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Username     string     `json:"username" db:"username"`
	Email        *string    `json:"email,omitempty" db:"email"`
	PasswordHash string     `json:"-" db:"password_hash"`
	Role         UserRole   `json:"role" db:"role"`
	Status       UserStatus `json:"status" db:"status"`

	// Profile
	DisplayName *string `json:"display_name,omitempty" db:"display_name"`
	AvatarURL   *string `json:"avatar_url,omitempty" db:"avatar_url"`
	Bio         *string `json:"bio,omitempty" db:"bio"`

	// Stats
	TotalScore       int `json:"total_score" db:"total_score"`
	ChallengesSolved int `json:"challenges_solved" db:"challenges_solved"`

	// Metadata
	EmailVerified bool       `json:"email_verified" db:"email_verified"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty" db:"last_login_at"`
	LastLoginIP   *string    `json:"-" db:"last_login_ip"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// TeamToken for token-based access
type TeamToken struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	Token       string     `json:"token" db:"token"`
	TeamName    string     `json:"team_name" db:"team_name"`
	MaxUses     int        `json:"max_uses" db:"max_uses"`
	CurrentUses int        `json:"current_uses" db:"current_uses"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// InviteCode for invite-only registration
type InviteCode struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	Code        string     `json:"code" db:"code"`
	MaxUses     int        `json:"max_uses" db:"max_uses"`
	CurrentUses int        `json:"current_uses" db:"current_uses"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// Session represents an active user session
type Session struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	UserID       *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	TokenID      *uuid.UUID `json:"token_id,omitempty" db:"token_id"`
	SessionToken string     `json:"-" db:"session_token"`
	IPAddress    *string    `json:"-" db:"ip_address"`
	UserAgent    *string    `json:"-" db:"user_agent"`
	ExpiresAt    time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// RefreshToken for JWT refresh
type RefreshToken struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	TokenHash string    `json:"-" db:"token_hash"`
	ExpiresAt time.Time `json:"expires_at" db:"expires_at"`
	Revoked   bool      `json:"revoked" db:"revoked"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
