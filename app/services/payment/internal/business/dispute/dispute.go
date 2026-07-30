// Package dispute holds the dispute domain: the record of a contested
// transfer and the resolution table that fixes what the ruling does with
// the money. The table is a pure function here (unit-tested); the
// payments_ext procedures only apply what it decided.
package dispute

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// ErrDisputeNotFound covers an unknown dispute and a resolve from the
// wrong state alike (the caller reads the dispute back to tell — same
// contract as the ledger and the transfer).
var ErrDisputeNotFound = errors.New("dispute not found")

// ErrNotDisputable rejects opening a dispute on a transfer that has no
// money at risk: before an agent committed there is no hold and no
// counterparty (that is a cancel), and after a terminal there is nothing
// left to freeze.
var ErrNotDisputable = errors.New("transfer cannot be disputed in its current state")

// ErrInvalidResolution rejects a ruling outside the resolution table.
var ErrInvalidResolution = errors.New("invalid dispute resolution")

// ErrNotAllowed rejects a caller with no standing in the transfer.
var ErrNotAllowed = errors.New("caller not allowed")

// Party values mirror the stored dispute_party enum: who raised it.
type Party string

const (
	PartyClient Party = "client"
	PartyAgent  Party = "agent"
	PartySystem Party = "system" // the payout-timeout auto-dispute
)

// Reason values mirror the stored dispute_reason enum.
type Reason string

const (
	ReasonAgentNotPaid         Reason = "agent_not_paid"
	ReasonRecipientNotReceived Reason = "recipient_not_received"
	ReasonClientFraud          Reason = "client_fraud"
	ReasonOther                Reason = "other"
)

// Status values mirror the stored dispute_status enum.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

// Resolution values mirror the stored dispute_resolution enum — the
// admin's ruling.
type Resolution string

const (
	ResolutionAgentRight  Resolution = "agent_right"
	ResolutionAgentFault  Resolution = "agent_fault"
	ResolutionClientFraud Resolution = "client_fraud"
	ResolutionNoFault     Resolution = "no_fault"
)

// HoldOutcome values mirror the stored hold_outcome enum: what the ruling
// did with the frozen collateral.
type HoldOutcome string

const (
	HoldReleased HoldOutcome = "released"
	HoldCaptured HoldOutcome = "captured"
)

// Effect is the whole money outcome of a ruling: what happens to the
// hold, where the transfer lands, and whether the client gets their money
// back. One row of the spec's resolution table.
type Effect struct {
	HoldOutcome    HoldOutcome
	TerminalStatus transfer.Status
	RefundClient   bool
}

// Completes reports whether the ruling lands the transfer at `done`: the
// payout stands and the money reached the recipient. Such a ruling carries
// everything a happy-path completion does — the agent earns their share
// (whether they were proven right or the client defrauded them, the work
// was performed) and the movement gets its AML evaluation.
func (e Effect) Completes() bool {
	return e.TerminalStatus == transfer.StatusDone
}

// effects is the resolution table, verbatim from the spec's
// "Dispute / bad-transfer path":
//
//	agent proven right  -> release, no balance move, done
//	agent at fault      -> CAPTURE (deduction), client refunded, refunded
//	client fraud        -> release (make the agent whole), no refund, done
//	no fault / stuck    -> release, no refund, cancelled
var effects = map[Resolution]Effect{
	ResolutionAgentRight:  {HoldOutcome: HoldReleased, TerminalStatus: transfer.StatusDone},
	ResolutionAgentFault:  {HoldOutcome: HoldCaptured, TerminalStatus: transfer.StatusRefunded, RefundClient: true},
	ResolutionClientFraud: {HoldOutcome: HoldReleased, TerminalStatus: transfer.StatusDone},
	ResolutionNoFault:     {HoldOutcome: HoldReleased, TerminalStatus: transfer.StatusCancelled},
}

// EffectOf returns the money outcome of a ruling.
// dispute.ErrInvalidResolution for anything outside the table.
func EffectOf(r Resolution) (Effect, error) {
	e, ok := effects[r]
	if !ok {
		return Effect{}, ErrInvalidResolution
	}
	return e, nil
}

// Dispute is the read entity. Every money figure is a snapshot taken when
// the transfer froze, so a ruling reads the dispute rather than a
// transfer that moved on.
type Dispute struct {
	ID                uuid.UUID
	TransferID        uuid.UUID
	TransferReference string
	ClientID          uuid.UUID
	AgentProfileID    *uuid.UUID
	OpenedByParty     Party
	OpenedBy          *uuid.UUID // nil = system auto-dispute
	Reason            Reason
	Details           *string
	FromStatus        transfer.Status
	HoldAmount        int64             // USD minor units, frozen while open
	RefundCurrency    transfer.Currency // what the client paid in…
	RefundAmount      int64             // …and how much they would be owed back
	Status            Status
	Resolution        *Resolution
	HoldOutcome       *HoldOutcome
	ResolutionNote    *string
	DeductionID       *uuid.UUID // the capture's ledger request, on agent_fault
	ResolvedBy        *uuid.UUID
	ResolvedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewDispute is the open payload. OpenedBy is nil only for the system
// auto-dispute.
type NewDispute struct {
	TransferID uuid.UUID
	Party      Party
	OpenedBy   *uuid.UUID
	Reason     Reason
	Details    *string
}

// Filter narrows dispute listings; nil fields mean "not filtering".
type Filter struct {
	Status         *Status
	TransferID     *uuid.UUID
	AgentProfileID *uuid.UUID
	ClientID       *uuid.UUID
}
