package models

import (
	"time"

	"github.com/google/uuid"
)

type TeamStatus string

const (
	TeamActive   TeamStatus = "active"
	TeamDisabled TeamStatus = "disabled"
)

type MemberRole string

const (
	MemberCaptain MemberRole = "captain"
	MemberRegular MemberRole = "member"
)

type ServiceCategory string

const (
	CategoryPwn       ServiceCategory = "pwn"
	CategoryWeb       ServiceCategory = "web"
	CategoryCrypto    ServiceCategory = "crypto"
	CategoryMisc      ServiceCategory = "misc"
	CategoryRev       ServiceCategory = "rev"
	CategoryForensics ServiceCategory = "forensics"
)

type ServiceTier string

const (
	TierCore    ServiceTier = "core"
	TierStretch ServiceTier = "stretch"
)

type SLAStatus string

const (
	SLAOk           SLAStatus = "OK"
	SLADown         SLAStatus = "DOWN"
	SLAFaulty       SLAStatus = "FAULTY"
	SLAFlagNotFound SLAStatus = "FLAG_NOT_FOUND"
	SLARecovering   SLAStatus = "RECOVERING"
)

type ScoreStream string

const (
	StreamAttack  ScoreStream = "ATTACK"
	StreamDefense ScoreStream = "DEFENSE"
	StreamSLA     ScoreStream = "SLA"
	StreamKoth    ScoreStream = "KOTH"
)

type GameTeam struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	Slug      string     `json:"slug" db:"slug"`
	Subnet    *string    `json:"subnet,omitempty" db:"subnet"`
	VulnboxIP *string    `json:"vulnbox_ip,omitempty" db:"vulnbox_ip"`
	NodeID    *uuid.UUID `json:"node_id,omitempty" db:"node_id"`
	Token     *string    `json:"-" db:"token"`
	Status    TeamStatus `json:"status" db:"status"`
	IsNOP     bool       `json:"is_nop" db:"is_nop"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" db:"updated_at"`
}

type GameTeamMember struct {
	TeamID    uuid.UUID  `json:"team_id" db:"team_id"`
	UserID    uuid.UUID  `json:"user_id" db:"user_id"`
	Role      MemberRole `json:"role" db:"role"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}

type GameService struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	Name       string          `json:"name" db:"name"`
	Slug       string          `json:"slug" db:"slug"`
	Category   ServiceCategory `json:"category" db:"category"`
	Tier       ServiceTier     `json:"tier" db:"tier"`
	Port       *int            `json:"port,omitempty" db:"port"`
	CheckerRef *string         `json:"checker_ref,omitempty" db:"checker_ref"`
	FlagStores int             `json:"flag_stores" db:"flag_stores"`
	Enabled    bool            `json:"enabled" db:"enabled"`
	SortOrder  int             `json:"sort_order" db:"sort_order"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at" db:"updated_at"`
}

type GameTick struct {
	TickNumber int        `json:"tick_number" db:"tick_number"`
	StartedAt  time.Time  `json:"started_at" db:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	Status     string     `json:"status" db:"status"`
}

type GameFlag struct {
	ID             uuid.UUID `json:"id" db:"id"`
	TickNumber     int       `json:"tick_number" db:"tick_number"`
	TeamID         uuid.UUID `json:"team_id" db:"team_id"`
	ServiceID      uuid.UUID `json:"service_id" db:"service_id"`
	StoreIndex     int       `json:"store_index" db:"store_index"`
	Flag           string    `json:"flag" db:"flag"`
	ValidFromTick  int       `json:"valid_from_tick" db:"valid_from_tick"`
	ValidUntilTick int       `json:"valid_until_tick" db:"valid_until_tick"`
	PlantedAt      time.Time `json:"planted_at" db:"planted_at"`
}

type GameSLACheck struct {
	ID         uuid.UUID `json:"id" db:"id"`
	TickNumber int       `json:"tick_number" db:"tick_number"`
	TeamID     uuid.UUID `json:"team_id" db:"team_id"`
	ServiceID  uuid.UUID `json:"service_id" db:"service_id"`
	Status     SLAStatus `json:"status" db:"status"`
	LatencyMS  *int      `json:"latency_ms,omitempty" db:"latency_ms"`
	Message    *string   `json:"message,omitempty" db:"message"`
	CheckedAt  time.Time `json:"checked_at" db:"checked_at"`
}

type GameCapture struct {
	ID             uuid.UUID `json:"id" db:"id"`
	TickNumber     int       `json:"tick_number" db:"tick_number"`
	AttackerTeamID uuid.UUID `json:"attacker_team_id" db:"attacker_team_id"`
	VictimTeamID   uuid.UUID `json:"victim_team_id" db:"victim_team_id"`
	ServiceID      uuid.UUID `json:"service_id" db:"service_id"`
	FlagID         uuid.UUID `json:"flag_id" db:"flag_id"`
	SubmittedAt    time.Time `json:"submitted_at" db:"submitted_at"`
}

type GameKothHill struct {
	ID           uuid.UUID `json:"id" db:"id"`
	Name         string    `json:"name" db:"name"`
	Slug         string    `json:"slug" db:"slug"`
	Category     *string   `json:"category,omitempty" db:"category"`
	Host         *string   `json:"host,omitempty" db:"host"`
	Port         *int      `json:"port,omitempty" db:"port"`
	CheckerRef   *string   `json:"checker_ref,omitempty" db:"checker_ref"`
	ResetSeconds int       `json:"reset_seconds" db:"reset_seconds"`
	Enabled      bool      `json:"enabled" db:"enabled"`
	SortOrder    int       `json:"sort_order" db:"sort_order"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

type GameKothRound struct {
	RoundNumber int        `json:"round_number" db:"round_number"`
	StartedAt   time.Time  `json:"started_at" db:"started_at"`
	EndsAt      *time.Time `json:"ends_at,omitempty" db:"ends_at"`
	Status      string     `json:"status" db:"status"`
}

type GameKothControl struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	TickNumber       int        `json:"tick_number" db:"tick_number"`
	RoundNumber      *int       `json:"round_number,omitempty" db:"round_number"`
	HillID           uuid.UUID  `json:"hill_id" db:"hill_id"`
	ControllerTeamID *uuid.UUID `json:"controller_team_id,omitempty" db:"controller_team_id"`
	CheckedAt        time.Time  `json:"checked_at" db:"checked_at"`
}

type GameScoreEvent struct {
	ID          uuid.UUID   `json:"id" db:"id"`
	TeamID      uuid.UUID   `json:"team_id" db:"team_id"`
	TickNumber  *int        `json:"tick_number,omitempty" db:"tick_number"`
	RoundNumber *int        `json:"round_number,omitempty" db:"round_number"`
	Stream      ScoreStream `json:"stream" db:"stream"`
	Points      float64     `json:"points" db:"points"`
	Source      *string     `json:"source,omitempty" db:"source"`
	CreatedAt   time.Time   `json:"created_at" db:"created_at"`
}

type GameStanding struct {
	TeamID    uuid.UUID `json:"team_id" db:"team_id"`
	Attack    float64   `json:"attack" db:"attack"`
	Defense   float64   `json:"defense" db:"defense"`
	SLA       float64   `json:"sla" db:"sla"`
	Koth      float64   `json:"koth" db:"koth"`
	Total     float64   `json:"total" db:"total"`
	Rank      *int      `json:"rank,omitempty" db:"rank"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
