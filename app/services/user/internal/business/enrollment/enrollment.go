// Package enrollment holds the agent-enrollment domain types. Two entry
// points share one table and one state machine:
//
//   - invited (ops-initiated): invite → TOS acceptance → admin approval.
//     InvitedBy names the inviter, and reaching agent needs an explicit
//     admin decision on the enrollment itself.
//   - self-serve (AG-03/AG-04): the candidate signs themselves up. There
//     is no inviter (InvitedBy is nil, which is what tells the two apart),
//     the terms are stamped by the very call that creates the request —
//     so it is born StatusAccepted — and there is no separate decision on
//     the enrollment at all: ops approving the guarantee DEPOSIT is the
//     decision, and it is what carries the request to StatusApproved.
//
// Both therefore sit in StatusAccepted while their guarantee funding is
// outstanding, which is why the state machine needed no new value: that
// state already meant "terms stamped, guarantee open, waiting on money".
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

// ErrGuaranteeNotSettled blocks an approval whose guarantee funding is
// missing or not yet approvable on the payment side.
var ErrGuaranteeNotSettled = errors.New("guarantee funding not settled")

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
	InvitedBy     *uuid.UUID // nil = self-serve (nobody invited them)
	Status        Status
	TOSVersion    *string    // set on accept, or inline at self-serve creation
	TOSAcceptedAt *time.Time // same
	DecidedBy     *uuid.UUID // admin who approved/cancelled, candidate on decline
	DecidedAt     *time.Time // terminal-transition stamp
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SelfServe reports whether the candidate signed themselves up. It is the
// single discriminator between the two entry points: only a self-serve
// request is promoted by its guarantee deposit rather than by an admin.
func (e Enrollment) SelfServe() bool { return e.InvitedBy == nil }

// SelfEnrollment is the self-serve creation payload (AG-04). TOSVersion is
// required — the acceptance happens here, not in a later step — and City
// seeds the agent profile the same call provisions.
type SelfEnrollment struct {
	TOSVersion string
	City       *string
}

// Filter narrows enrollment listings; nil means "not filtering".
type Filter struct {
	Status *Status
}
