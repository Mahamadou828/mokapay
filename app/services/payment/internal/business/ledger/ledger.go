// Package ledger holds the ledger domain types: the mutable ledger_request
// workflow object and the immutable settlement lines. This is the ONLY
// path a balance moves through.
package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// ErrRequestNotFound covers an unknown id and a transition from the wrong
// state alike (a rejected approve may just mean "already done" — the
// caller reads the request back to tell).
var ErrRequestNotFound = errors.New("ledger request not found")

// ErrReferenceInUse is returned by Add when the referenced entity
// already has a live-or-settled request (rejected ones free the slot) —
// a replayed create; read the existing request with GetByReference.
var ErrReferenceInUse = errors.New("entity already has a ledger request")

// ErrNotFeasible is returned by Add when the request cannot be honored:
// insufficient available funds (incl. the withdrawal guarantee floor) or
// an ineligible/unknown profile (outflows need an active one).
var ErrNotFeasible = errors.New("ledger request not feasible")

var ErrLedgerStatusClose = errors.New("ledger request is already in a closed status")

// ErrInvalidType rejects a type the caller may not create (or one with no
// direction mapping).
var ErrInvalidType = errors.New("ledger type not allowed")

// ErrInvalidStatusMove rejects a transition outside the state machine.
var ErrInvalidStatusMove = errors.New("invalid ledger status transition")

// ErrNotAllowed rejects a caller whose role has no business here.
var ErrNotAllowed = errors.New("caller not allowed")

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

// ReferenceType values mirror the stored ledger_reference_type enum.
type ReferenceType string

const (
	ReferenceEnrollment ReferenceType = "enrollment"
	ReferenceTransfer   ReferenceType = "transfer"
)

// Reference names the entity a request settles (guarantee → enrollment,
// capture → transfer). At most one live-or-settled request per entity;
// free-standing requests (agent deposits, withdrawals) carry none.
type Reference struct {
	Type ReferenceType
	ID   uuid.UUID
}

// LedgerRequest is the read entity of the workflow object. An open
// outflow request IS a reservation: its amount sits in the profile's held
// until it settles or is rejected.
type LedgerRequest struct {
	ID             uuid.UUID
	Ref            string // human reference (DEP-2026-000042): the motif of the off-platform payment
	AgentProfileID uuid.UUID
	Type           Type
	Direction      Direction
	Amount         int64 // minor units, always positive; sign lives in Direction
	Status         Status
	Reference      *Reference // nil for free-standing requests
	ProofFileID    *uuid.UUID // file service NK, attached on proof upload
	Reason         *string
	DecidedBy      *uuid.UUID // account NK of the deciding admin
	DecidedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CreatedBy      uuid.UUID
}

// NewRequest is the create payload. Direction is deliberately absent —
// the caller never decides it; the usecase derives it from Type and
// hands it to the repo separately.
type NewRequest struct {
	AgentProfileID uuid.UUID
	Type           Type
	Amount         int64
	Reference      *Reference // decided at review: typed reference, no idempotency key
	Reason         *string
}

// Filter narrows request listings; nil fields mean "not filtering".
type Filter struct {
	Status         *Status
	Type           *Type
	AgentProfileID *uuid.UUID
}

// DepositChannel is one official platform receiving account: where an
// agent sends a funding deposit off-platform (the G2/G12 catalogue). Ops
// configuration served verbatim to the client — never persisted, never
// hardcoded app-side, so an account number changes without a release.
//
// Kind reuses the payment domain's account vocabulary rather than a
// private copy of the same two strings: the client branches on it to
// decide whether the sender field is a phone number or an account number,
// exactly as it does for an agent's own wallets.
type DepositChannel struct {
	Name          string // display label: "M-Pesa", "Rawbank"
	Kind          transfer.AccountKind
	AccountName   string // e.g. "MOKA PAY SARL"
	AccountNumber string // MSISDN or bank account number
	Instructions  *string
}

// ParseDepositChannels decodes the DEPOSIT_CHANNELS JSON array. Every
// entry needs name, kind, account_name and account_number — these strings
// are where agents send real money, so a partial entry fails startup
// instead of rendering blank. Kind is checked against the vocabulary too:
// the client branches on it, so a typo would silently ask for a phone
// number where a bank account belongs.
func ParseDepositChannels(raw string) ([]DepositChannel, error) {
	var rows []struct {
		Name          string  `json:"name"`
		Kind          string  `json:"kind"`
		AccountName   string  `json:"account_name"`
		AccountNumber string  `json:"account_number"`
		Instructions  *string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("parsing deposit channels: %w", err)
	}

	out := make([]DepositChannel, 0, len(rows))
	for i, r := range rows {
		if r.Name == "" || r.Kind == "" || r.AccountName == "" || r.AccountNumber == "" {
			return nil, fmt.Errorf("deposit channel %d: name, kind, account_name and account_number are required", i)
		}
		kind := transfer.AccountKind(r.Kind)
		if kind != transfer.AccountKindMobileMoney && kind != transfer.AccountKindBank {
			return nil, fmt.Errorf("deposit channel %d: kind %q must be %q or %q",
				i, r.Kind, transfer.AccountKindMobileMoney, transfer.AccountKindBank)
		}
		out = append(out, DepositChannel{
			Name:          r.Name,
			Kind:          kind,
			AccountName:   r.AccountName,
			AccountNumber: r.AccountNumber,
			Instructions:  r.Instructions,
		})
	}

	return out, nil
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
