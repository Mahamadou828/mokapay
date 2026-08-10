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

// ErrCorridorNotAllowed rejects a currency pair outside the v1 ladder:
// every source currency one-way into cdf, plus the two same-currency
// corridors (usd→usd, cdf→cdf).
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

// The event_type vocabulary a transfer transition announces itself with.
// The notification job and the live websocket push carry the same string
// (matching.EventOfferReceived is the same vocabulary, owned by the domain
// that produces it), so a frontend keys off one set of names whichever
// channel it hears on first.
const (
	EventMatched         = "transfer.matched"
	EventExpired         = "transfer.expired"
	EventClientPaid      = "transfer.client_paid"
	EventPayoutDone      = "transfer.payout_done"
	EventCompleted       = "transfer.completed"
	EventCancelled       = "transfer.cancelled"
	EventDisputed        = "transfer.disputed"
	EventDisputeResolved = "transfer.dispute_resolved"
)

// Currency values mirror the stored currency enum.
type Currency string

const (
	CurrencyEUR Currency = "eur"
	CurrencyUSD Currency = "usd"
	CurrencyZAR Currency = "zar"
	CurrencyAOA Currency = "aoa"
	CurrencyZMW Currency = "zmw"
	CurrencyTZS Currency = "tzs"
	CurrencyCDF Currency = "cdf"
)

// SourceCurrencies is the send ladder — what a client may fund a transfer
// with. Every one of them settles into CDF and only into CDF; CDF itself
// is not on the list (a franc source is the same-currency corridor, not a
// rung of the ladder). Order is the order the picker offers, EUR first.
var SourceCurrencies = []Currency{
	CurrencyEUR, CurrencyUSD, CurrencyZAR, CurrencyAOA, CurrencyZMW, CurrencyTZS,
}

// AccountKind tells the two shapes of receiving account apart: a
// mobile-money wallet is reached by MSISDN, a bank account by an account
// number. It is what decides whether a screen collecting an account
// renders a phone field or a plain text one — the payment domain's single
// vocabulary for that split, shared by the operator catalogue below and
// by the platform's own deposit channels (ledger.DepositChannel).
type AccountKind string

const (
	AccountKindMobileMoney AccountKind = "mobile_money"
	AccountKindBank        AccountKind = "bank"
)

// Operator values mirror the stored payment_operator enum. Since the bank
// channels shipped, the catalogue is no longer mobile money only — use
// Kind to tell which half a value falls in.
type Operator string

const (
	OperatorOrangeMoney Operator = "orange_money"
	OperatorMPesa       Operator = "m_pesa"
	OperatorAirtelMoney Operator = "airtel_money"
	OperatorAfrimoney   Operator = "afrimoney"
	OperatorRawbank     Operator = "rawbank"
	OperatorEquityBCDC  Operator = "equity_bcdc"
)

// Kind reports what shape of account this operator is paid on. Derived,
// never stored: the operator alone decides it, so a persisted copy could
// only ever disagree with it.
func (o Operator) Kind() AccountKind {
	switch o {
	case OperatorRawbank, OperatorEquityBCDC:
		return AccountKindBank
	default:
		return AccountKindMobileMoney
	}
}

// Operators is the full catalogue: every value an agent can declare a
// payout wallet on, banks included.
var Operators = []Operator{
	OperatorOrangeMoney, OperatorMPesa, OperatorAirtelMoney,
	OperatorAfrimoney, OperatorRawbank, OperatorEquityBCDC,
}

// MobileMoneyOperators is the subset a *transfer* can be routed over. The
// bank operators are deliberately absent: an agent may hold a bank wallet
// (it is a real account, and withdrawals pay out to it), but routing a
// transfer through one would mean a beneficiary identified by a bank
// account rather than a phone number, and a settlement delay the
// transfer's timeouts were never sized for. Both are product decisions
// this slice does not make.
var MobileMoneyOperators = []Operator{
	OperatorOrangeMoney, OperatorMPesa, OperatorAirtelMoney, OperatorAfrimoney,
}

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
