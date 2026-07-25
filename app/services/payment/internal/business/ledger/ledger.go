// Package ledger holds the ledger domain types: the mutable ledger_request
// workflow object and the immutable settlement lines. This is the ONLY
// path a balance moves through.
package ledger

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrRequestNotFound covers an unknown id and a transition from the wrong
// state alike (a rejected approve may just mean "already done" — the
// caller reads the request back to tell).
var ErrRequestNotFound = errors.New("ledger request not found")

// ErrDuplicateKey is returned by Add when the idempotency key already
// exists — a producer retry; read the existing request with GetByKey.
var ErrDuplicateKey = errors.New("idempotency key already used")

// ErrNotFeasible is returned by Add when the request cannot be honored:
// insufficient available funds (incl. the withdrawal guarantee floor) or
// an ineligible/unknown profile (outflows need an active one).
var ErrNotFeasible = errors.New("ledger request not feasible")

// Type values mirror the stored ledger_request_type enum.
type Type string

const (
	TypeFunding    Type = "funding"
	TypeWithdrawal Type = "withdrawal"
	TypeRefund     Type = "refund"
	TypeDeduction  Type = "deduction"
	TypeCommission Type = "commission"
)

// Status values mirror the stored ledger_request_status enum.
type Status string

const (
	StatusInitiated            Status = "initiated"
	StatusPendingAgentAction   Status = "pending_agent_action"
	StatusPendingAdminApproval Status = "pending_admin_approval"
	StatusDone                 Status = "done"
	StatusRejected             Status = "rejected"
	StatusDisputed             Status = "disputed" // reserved, transfers volley
)

// Direction says whether the request increases or decreases the balance —
// deliberately not credit/debit (that vocabulary flips with the point of
// view).
type Direction string

const (
	DirectionInflow  Direction = "inflow"
	DirectionOutflow Direction = "outflow"
)

// LedgerRequest is the read entity of the workflow object. An open
// outflow request IS a reservation: its amount sits in the profile's held
// until it settles or is rejected.
type LedgerRequest struct {
	ID             uuid.UUID
	AgentProfileID uuid.UUID
	Type           Type
	Direction      Direction
	Amount         int64 // minor units, always positive; sign lives in Direction
	Status         Status
	EnrollmentID   *uuid.UUID // guarantee-funding linkage (user service NK)
	ProofFileID    *uuid.UUID // file service NK, attached on proof upload
	Reason         *string
	IdempotencyKey string
	DecidedBy      *uuid.UUID // account NK of the deciding admin
	DecidedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewRequest is the create payload.
type NewRequest struct {
	AgentProfileID uuid.UUID
	Type           Type
	Direction      Direction
	Amount         int64
	IdempotencyKey string
	EnrollmentID   *uuid.UUID
	Reason         *string
}

// Filter narrows request listings; nil fields mean "not filtering".
type Filter struct {
	Status         *Status
	Type           *Type
	AgentProfileID *uuid.UUID
}

// Line is one immutable settlement record: signed amount (inflow +,
// outflow −) and the balance it left behind.
type Line struct {
	ID             uuid.UUID
	AgentProfileID uuid.UUID
	RequestID      uuid.UUID
	Amount         int64
	BalanceAfter   int64
	CreatedAt      time.Time
}
