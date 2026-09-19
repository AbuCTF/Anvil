package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ChallengeDifficulty string

const (
	DifficultyEasy   ChallengeDifficulty = "easy"
	DifficultyMedium ChallengeDifficulty = "medium"
	DifficultyHard   ChallengeDifficulty = "hard"
	DifficultyInsane ChallengeDifficulty = "insane"
)

type ChallengeStatus string

const (
	ChallengeDraft     ChallengeStatus = "draft"
	ChallengePublished ChallengeStatus = "published"
	ChallengeArchived  ChallengeStatus = "archived"
)

type Category struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Slug        string    `json:"slug" db:"slug"`
	Description *string   `json:"description,omitempty" db:"description"`
	Color       *string   `json:"color,omitempty" db:"color"`
	Icon        *string   `json:"icon,omitempty" db:"icon"`
	SortOrder   int       `json:"sort_order" db:"sort_order"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

type ExposedPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // tcp, udp
}

type Challenge struct {
	ID          uuid.UUID           `json:"id" db:"id"`
	Name        string              `json:"name" db:"name"`
	Slug        string              `json:"slug" db:"slug"`
	Description *string             `json:"description,omitempty" db:"description"`
	Difficulty  ChallengeDifficulty `json:"difficulty" db:"difficulty"`
	CategoryID  *uuid.UUID          `json:"category_id,omitempty" db:"category_id"`
	Status      ChallengeStatus     `json:"status" db:"status"`

	AuthorID   *uuid.UUID `json:"author_id,omitempty" db:"author_id"`
	AuthorName *string    `json:"author_name,omitempty" db:"author_name"`

	ContainerImage    string  `json:"container_image" db:"container_image"`
	ContainerRegistry *string `json:"container_registry,omitempty" db:"container_registry"`
	ContainerTag      string  `json:"container_tag" db:"container_tag"`

	CPULimit    string `json:"cpu_limit" db:"cpu_limit"`
	MemoryLimit string `json:"memory_limit" db:"memory_limit"`

	ExposedPortsJSON json.RawMessage `json:"-" db:"exposed_ports"`
	ExposedPorts     []ExposedPort   `json:"exposed_ports" db:"-"`
	NetworkMode      string          `json:"network_mode" db:"network_mode"`

	InstanceTimeout *int `json:"instance_timeout,omitempty" db:"instance_timeout"`
	MaxExtensions   *int `json:"max_extensions,omitempty" db:"max_extensions"`

	VMTimeoutMinutes   *int `json:"vm_timeout_minutes,omitempty" db:"vm_timeout_minutes"`
	VMMaxExtensions    *int `json:"vm_max_extensions,omitempty" db:"vm_max_extensions"`
	VMExtensionMinutes *int `json:"vm_extension_minutes,omitempty" db:"vm_extension_minutes"`
	CooldownMinutes    *int `json:"cooldown_minutes,omitempty" db:"cooldown_minutes"`

	ResourceType string `json:"resource_type" db:"resource_type"` // docker or vm

	BasePoints int `json:"base_points" db:"base_points"`

	TotalFlags    int `json:"total_flags" db:"total_flags"`
	TotalSolves   int `json:"total_solves" db:"total_solves"`
	TotalAttempts int `json:"total_attempts" db:"total_attempts"`

	ReleaseDate *time.Time `json:"release_date,omitempty" db:"release_date"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`

	Category *Category `json:"category,omitempty" db:"-"`
	Flags    []Flag    `json:"flags,omitempty" db:"-"`
	Hints    []Hint    `json:"hints,omitempty" db:"-"`
}

func (c *Challenge) ParseExposedPorts() error {
	if c.ExposedPortsJSON != nil {
		return json.Unmarshal(c.ExposedPortsJSON, &c.ExposedPorts)
	}
	c.ExposedPorts = []ExposedPort{}
	return nil
}

type Flag struct {
	ID          uuid.UUID `json:"id" db:"id"`
	ChallengeID uuid.UUID `json:"challenge_id" db:"challenge_id"`

	Name        string  `json:"name" db:"name"`
	Description *string `json:"description,omitempty" db:"description"`
	SortOrder   int     `json:"sort_order" db:"sort_order"`

	FlagHash      string  `json:"-" db:"flag_hash"` // hash stored, never expose
	FlagFormat    *string `json:"flag_format,omitempty" db:"flag_format"`
	IsRegex       bool    `json:"is_regex" db:"is_regex"`
	CaseSensitive bool    `json:"case_sensitive" db:"case_sensitive"`

	// when flag_type is dynamic, anvil generates prefix{uuid} per instance and
	// injects it as the FLAG env var; flag_hash is not used.
	FlagType          string  `json:"flag_type" db:"flag_type"`
	DynamicFlagPrefix *string `json:"dynamic_flag_prefix,omitempty" db:"dynamic_flag_prefix"`

	Points int `json:"points" db:"points"`

	TotalSolves    int        `json:"total_solves" db:"total_solves"`
	FirstBloodUser *uuid.UUID `json:"first_blood_user_id,omitempty" db:"first_blood_user_id"`
	FirstBloodAt   *time.Time `json:"first_blood_at,omitempty" db:"first_blood_at"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type Hint struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	ChallengeID uuid.UUID  `json:"challenge_id" db:"challenge_id"`
	FlagID      *uuid.UUID `json:"flag_id,omitempty" db:"flag_id"`

	Content   string `json:"content" db:"content"`
	Cost      int    `json:"cost" db:"cost"`
	SortOrder int    `json:"sort_order" db:"sort_order"`

	UnlockAfterAttempts *int `json:"unlock_after_attempts,omitempty" db:"unlock_after_attempts"`
	UnlockAfterTime     *int `json:"unlock_after_time,omitempty" db:"unlock_after_time"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type ChallengeWithProgress struct {
	Challenge
	UserProgress *UserChallengeProgress `json:"user_progress,omitempty"`
}

type UserChallengeProgress struct {
	FlagsSolved    int        `json:"flags_solved"`
	TotalFlags     int        `json:"total_flags"`
	PointsEarned   int        `json:"points_earned"`
	MaxPoints      int        `json:"max_points"`
	IsCompleted    bool       `json:"is_completed"`
	FirstSolvedAt  *time.Time `json:"first_solved_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	HintsUnlocked  int        `json:"hints_unlocked"`
	PointsDeducted int        `json:"points_deducted"`
}
