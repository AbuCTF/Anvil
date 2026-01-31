package models

import (
	"time"

	"github.com/google/uuid"
)

// Submission represents a flag submission attempt
type Submission struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	UserID      *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID   *uuid.UUID `json:"session_id,omitempty" db:"session_id"`
	ChallengeID uuid.UUID  `json:"challenge_id" db:"challenge_id"`
	FlagID      *uuid.UUID `json:"flag_id,omitempty" db:"flag_id"`
	InstanceID  *uuid.UUID `json:"instance_id,omitempty" db:"instance_id"`

	SubmittedFlag string `json:"-" db:"submitted_flag"` // Don't expose in API
	IsCorrect     bool   `json:"is_correct" db:"is_correct"`
	PointsAwarded int    `json:"points_awarded" db:"points_awarded"`

	IPAddress *string `json:"-" db:"ip_address"`
	UserAgent *string `json:"-" db:"user_agent"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// SolvedFlag represents a successfully solved flag
type SolvedFlag struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	UserID       *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID    *uuid.UUID `json:"session_id,omitempty" db:"session_id"`
	ChallengeID  uuid.UUID  `json:"challenge_id" db:"challenge_id"`
	FlagID       uuid.UUID  `json:"flag_id" db:"flag_id"`
	SubmissionID *uuid.UUID `json:"submission_id,omitempty" db:"submission_id"`

	PointsAwarded int       `json:"points_awarded" db:"points_awarded"`
	SolvedAt      time.Time `json:"solved_at" db:"solved_at"`
}

// HintUnlock represents an unlocked hint
type HintUnlock struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	UserID         *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID      *uuid.UUID `json:"session_id,omitempty" db:"session_id"`
	HintID         uuid.UUID  `json:"hint_id" db:"hint_id"`
	PointsDeducted int        `json:"points_deducted" db:"points_deducted"`
	UnlockedAt     time.Time  `json:"unlocked_at" db:"unlocked_at"`
}

// FlagSubmitRequest represents a flag submission request
type FlagSubmitRequest struct {
	ChallengeID uuid.UUID `json:"challenge_id" binding:"required"`
	Flag        string    `json:"flag" binding:"required"`
}

// FlagSubmitResponse represents the response to a flag submission
type FlagSubmitResponse struct {
	Correct       bool    `json:"correct"`
	Message       string  `json:"message"`
	FlagName      *string `json:"flag_name,omitempty"`
	PointsAwarded int     `json:"points_awarded"`
	TotalScore    int     `json:"total_score"`
	IsFirstBlood  bool    `json:"is_first_blood"`
	AlreadySolved bool    `json:"already_solved"`
}

// ScoreboardEntry represents an entry on the scoreboard
type ScoreboardEntry struct {
	Rank             int        `json:"rank"`
	UserID           *uuid.UUID `json:"user_id,omitempty"`
	Username         string     `json:"username"`
	DisplayName      *string    `json:"display_name,omitempty"`
	TeamName         *string    `json:"team_name,omitempty"`
	TotalScore       int        `json:"total_score"`
	ChallengesSolved int        `json:"challenges_solved"`
	FlagsSolved      int        `json:"flags_solved"`
	LastSolveAt      *time.Time `json:"last_solve_at,omitempty"`
}

// Scoreboard represents the full scoreboard
type Scoreboard struct {
	Entries    []ScoreboardEntry `json:"entries"`
	TotalUsers int               `json:"total_users"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// UserStats represents detailed statistics for a user
type UserStats struct {
	UserID           uuid.UUID `json:"user_id"`
	Username         string    `json:"username"`
	TotalScore       int       `json:"total_score"`
	Rank             int       `json:"rank"`
	ChallengesSolved int       `json:"challenges_solved"`
	FlagsSolved      int       `json:"flags_solved"`
	FirstBloods      int       `json:"first_bloods"`
	HintsUsed        int       `json:"hints_used"`
	PointsDeducted   int       `json:"points_deducted"`

	// Category breakdown
	CategoryStats []CategoryStat `json:"category_stats"`

	// Activity
	RecentSolves []RecentSolve `json:"recent_solves"`
}

// CategoryStat represents stats for a specific category
type CategoryStat struct {
	CategoryID   uuid.UUID `json:"category_id"`
	CategoryName string    `json:"category_name"`
	Solved       int       `json:"solved"`
	Total        int       `json:"total"`
	Points       int       `json:"points"`
}

// RecentSolve represents a recently solved challenge
type RecentSolve struct {
	ChallengeID   uuid.UUID `json:"challenge_id"`
	ChallengeName string    `json:"challenge_name"`
	FlagName      string    `json:"flag_name"`
	Points        int       `json:"points"`
	SolvedAt      time.Time `json:"solved_at"`
	IsFirstBlood  bool      `json:"is_first_blood"`
}
