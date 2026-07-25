// Package enrollment holds the agent-enrollment domain types: the
// admin-initiated invite → TOS acceptance → approval workflow that
// promotes a user to agent.
package enrollment

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrEnrollmentNotFound covers an unknown id, a foreign owner and a
// transition from the wrong state alike — callers can't tell them apart
// on purpose.
var ErrEnrollmentNotFound = errors.New("enrollment not found")

// ErrEnrollmentPending is returned by Add when the account already has a
// live (invited/accepted) enrollment — the partial unique index.
var ErrEnrollmentPending = errors.New("account already has a live enrollment")

// ErrNotEligible rejects an invite (or approval) of an account that is not
// an active user (already an agent, ops, suspended, banned…).
var ErrNotEligible = errors.New("account not eligible for enrollment")

// ErrTOSVersionMismatch rejects an accept carrying a terms version other
// than the current one — the client displayed stale terms and must
// refetch.
var ErrTOSVersionMismatch = errors.New("terms of service version is not current")

// Status values mirror the stored enrollment_status enum.
type Status string

const (
	StatusInvited   Status = "invited"
	StatusAccepted  Status = "accepted"
	StatusDeclined  Status = "declined"
	StatusApproved  Status = "approved"
	StatusCancelled Status = "cancelled"
)

// Enrollment is the read entity — one enrollment request, in-flight or
// historical.
type Enrollment struct {
	ID            uuid.UUID
	AccountID     uuid.UUID
	InvitedBy     uuid.UUID
	Status        Status
	TOSVersion    *string    // set once accepted
	TOSAcceptedAt *time.Time // set once accepted
	DecidedBy     *uuid.UUID // admin who approved/cancelled, candidate on decline
	DecidedAt     *time.Time // terminal-transition stamp
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Filter narrows enrollment listings; nil means "not filtering".
type Filter struct {
	Status *Status
}
