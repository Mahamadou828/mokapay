// Package transfer holds the transfer domain types: the transfer itself
// (money stamped once at create, status machine driven by guarded
// procedures) and its append-only timeline. The reservation lifecycle —
// held += at accept, held −= at the terminal — lives entirely in the
// payments_ext procedures; nothing here mutates money.
package transfer

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrTransferNotFound covers an unknown id and a transition from the
// wrong state alike (the caller reads the transfer back to tell — same
// contract as the ledger).
var ErrTransferNotFound = errors.New("transfer not found")

// ErrCorridorNotAllowed rejects a currency pair outside the three v1
// corridors (usd→cdf, usd→usd, cdf→cdf).
var ErrCorridorNotAllowed = errors.New("currency corridor not allowed")

// ErrNotAllowed rejects a caller whose role has no business here.
var ErrNotAllowed = errors.New("caller not allowed")

// ErrInvalidAmount rejects a non-positive or cap-exceeding source amount.
var ErrInvalidAmount = errors.New("invalid transfer amount")

// ErrRecipientRequired: neither a beneficiary id nor inline recipient
// details were given.
var ErrRecipientRequired = errors.New("recipient details required")

// ErrInvalidStatusMove rejects a transition outside the proof path's
// two legal targets.
var ErrInvalidStatusMove = errors.New("invalid transfer status transition")

// Currency values mirror the stored currency enum.
type Currency string

const (
	CurrencyUSD Currency = "usd"
	CurrencyCDF Currency = "cdf"
)

// Operator values mirror the stored payment_operator enum.
type Operator string

const (
	OperatorOrangeMoney Operator = "orange_money"
	OperatorMPesa       Operator = "m_pesa"
	OperatorAirtelMoney Operator = "airtel_money"
)

// Operators is the v1 catalogue (all three serve both currencies).
var Operators = []Operator{OperatorOrangeMoney, OperatorMPesa, OperatorAirtelMoney}

// Status values mirror the stored transfer_status enum.
type Status string

const (
	StatusPendingMatch              Status = "pending_match"
	StatusPendingAgentApproval      Status = "pending_agent_approval"
	StatusPendingClientTransfer     Status = "pending_client_transfer"
	StatusPendingAgentTransfer      Status = "pending_agent_transfer"
	StatusPendingClientConfirmation Status = "pending_client_confirmation"
	StatusDone                      Status = "done"
	StatusCancelled                 Status = "cancelled"
	StatusExpired                   Status = "expired"
	StatusDisputed                  Status = "disputed"
	// StatusRefunded is the dispute path's own terminal: the agent was
	// found at fault, their guarantee was captured and the client is
	// refunded from it.
	StatusRefunded Status = "refunded"
)

// Transfer is the read entity. Every money column was stamped at create
// and never changes; HoldAmount is USD minor units whatever the corridor.
type Transfer struct {
	ID                uuid.UUID
	Reference         string // MKP-YYYY-NNNNNN
	ClientID          uuid.UUID
	BeneficiaryID     *uuid.UUID
	RecipientName     string
	RecipientPhone    string
	RecipientOperator Operator
	RecipientCity     *string
	FromCurrency      Currency
	FromOperator      Operator
	FromAmount        int64 // minor units of FromCurrency
	ToCurrency        Currency
	ToAmount          int64   // minor units of ToCurrency
	FeePct            string  // decimal string, e.g. "2.00"
	FeeAmount         int64   // FromCurrency minor units
	MarginAmount      int64   // ToCurrency minor units; 0 on same-currency
	TotalAmount       int64   // FromAmount + FeeAmount — what the client pays
	Rate              *string // decimal string; nil = same-currency corridor
	HoldAmount        int64   // USD minor units, reserved at accept
	Note              *string
	Status            Status
	AgentProfileID    *uuid.UUID // set on accept
	MatchRound        int32
	ProofClientID     *uuid.UUID // file-service NK
	ProofAgentID      *uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewTransfer is the repo create payload — every derived money value is
// already computed by the usecase (rate lock happens there, once).
// RateMicros is the FX rate × 1e6; nil on same-currency corridors.
type NewTransfer struct {
	BeneficiaryID     *uuid.UUID
	RecipientName     string
	RecipientPhone    string
	RecipientOperator Operator
	RecipientCity     *string
	FromCurrency      Currency
	FromOperator      Operator
	FromAmount        int64
	ToCurrency        Currency
	ToAmount          int64
	FeePctBps         int32 // fee percentage in basis points (200 = 2.00%)
	FeeAmount         int64
	MarginAmount      int64
	HoldAmount        int64
	RateMicros        *int64
	Note              *string
}

// Filter narrows transfer listings; nil fields mean "not filtering".
type Filter struct {
	ClientID       *uuid.UUID
	AgentProfileID *uuid.UUID
	Status         *Status
}

// Event is one immutable timeline row. FromStatus is nil only on the
// birth row; ActorID nil means system/timer.
type Event struct {
	ID         uuid.UUID
	TransferID uuid.UUID
	ActorID    *uuid.UUID
	FromStatus *Status
	ToStatus   Status
	Reason     *string
	CreatedAt  time.Time
}
