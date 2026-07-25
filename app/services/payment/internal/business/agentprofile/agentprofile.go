// Package agentprofile holds the agent-profile domain types: the money
// account of an agent (balance / held / derived available) and its
// lifecycle status. Balance moves only through the ledger domain.
package agentprofile

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrProfileNotFound = errors.New("agent profile not found")

// ErrProfileExists is returned by Add when the account already has a live
// profile (idempotent creators treat it as success and look the profile up).
var ErrProfileExists = errors.New("account already has an agent profile")

// ErrProfileHasFunds blocks deletion while balance or held is non-zero —
// money is never soft-deleted.
var ErrProfileHasFunds = errors.New("agent profile still holds funds")

// ErrInvalidTransition rejects an illegal lifecycle move (e.g. suspending
// a pending profile).
var ErrInvalidTransition = errors.New("invalid agent profile status transition")

// Status values mirror the stored agent_profile_status enum.
type Status string

const (
	StatusPending   Status = "pending"
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

// Level values mirror the stored agent_level enum.
type Level string

const (
	LevelN1 Level = "n1"
	LevelN2 Level = "n2"
	LevelN3 Level = "n3"
	LevelN4 Level = "n4"
)

// AgentProfile is the read entity. Available is view-derived
// (balance − held) and never stored.
type AgentProfile struct {
	ID             uuid.UUID
	AccountID      uuid.UUID // user-service account (NK)
	Status         Status
	Level          Level
	Currency       string // denomination of the amounts, not a transacting constraint
	Balance        int64  // minor units
	Held           int64
	Available      int64
	GuaranteeFloor int64
	City           *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewAgentProfile is the create payload. Empty Currency takes the store
// default (USD); GuaranteeFloor is a pointer because 0 is a legitimate
// floor — nil means the 20 USD default.
type NewAgentProfile struct {
	AccountID      uuid.UUID
	Currency       string
	GuaranteeFloor *int64
	City           *string
}
