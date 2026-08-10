// Package commission holds the commission domain: the per-transfer
// accrual (the 60/30/10 split of the fee plus the FX margin), the weekly
// payout batch, and the pure money math both rest on. Nothing here
// touches the database — the arithmetic is integer-only and unit-tested,
// so SQL only ever stores numbers this package decided.
package commission

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// ErrCommissionExists is returned by Accrue when the transfer already has
// its accrual. Idempotent callers treat it as success — the UNIQUE on
// transfer_fk is what makes replaying a completion safe.
var ErrCommissionExists = errors.New("transfer already has a commission accrual")

// ErrCommissionNotFound covers an unknown accrual.
var ErrCommissionNotFound = errors.New("commission not found")

// ErrBatchNotCreated is returned by AddBatch when the batch did not
// happen: the week is already paid for that agent, nothing was due, or a
// concurrent accrual changed the total under the caller. Never a partial
// payout — the caller re-reads and retries.
var ErrBatchNotCreated = errors.New("commission batch not created")

// ErrNotPayable rejects an accrual the payout math cannot express (a
// transfer with no assigned agent, or no way to reach the guarantee
// balance's denomination).
var ErrNotPayable = errors.New("commission not payable")

// The split of the fee, flat for every agent level (D9 — level affects
// capacity and priority, never the share). Basis points so the math stays
// integer; the guarantee fund takes the remainder, which keeps the three
// shares summing to the fee exactly.
const (
	AgentShareBps    = 6_000 // 60% of the fee — the agent's earning
	PlatformShareBps = 3_000 // 30% of the fee — the platform
	// The remaining ~10% funds the guarantee pool; it is computed as
	// fee − agent − platform so no cent is ever lost to rounding.
)

// Status values mirror the stored commission_status enum. It is derived
// in the view (batch link + the batch's ledger request), never stored.
type Status string

const (
	StatusAccrued Status = "accrued" // earned, not yet in a payout batch
	StatusBatched Status = "batched" // in a batch whose ledger request is open
	StatusPaid    Status = "paid"    // the batch's ledger request settled
)

// Shares is the split of one transfer's revenue. The fee shares are all
// denominated in the transfer's from_currency; Margin is in its
// to_currency and belongs 100% to the platform — the two are deliberately
// never added together (two currencies, one number, is how the prototype
// lost track of what it had earned).
type Shares struct {
	Agent         int64
	Platform      int64
	GuaranteeFund int64
	Margin        int64
}

// SplitFee applies the 60/30/10 rule to a fee and carries the FX margin
// through untouched. Half-up on the two percentage shares, remainder to
// the guarantee fund: Agent+Platform+GuaranteeFund == fee, always.
func SplitFee(feeAmount, marginAmount int64) Shares {
	agent := (feeAmount*AgentShareBps + 5_000) / 10_000
	platform := (feeAmount*PlatformShareBps + 5_000) / 10_000

	return Shares{
		Agent:         agent,
		Platform:      platform,
		GuaranteeFund: feeAmount - agent - platform,
		Margin:        marginAmount,
	}
}

// PayableAmount expresses the agent's share in the guarantee balance's
// denomination (USD minor units), which is the only currency the ledger
// moves. A USD fee is already there; every other source currency converts
// at the transfer's OWN locked rate, read off the two amounts it stamped
// at create (hold_amount IS total_amount converted into USD, whatever the
// source) — never a fresh lookup, so the payout can never disagree with
// the quote the client got. That ratio is what makes this work for the
// whole send ladder without enumerating it: the currency names the scale,
// the stamped pair carries it.
func PayableAmount(agentShare int64, feeCurrency transfer.Currency, totalAmount, holdAmount int64) (int64, error) {
	if feeCurrency == transfer.CurrencyUSD {
		return agentShare, nil
	}
	if totalAmount <= 0 || holdAmount <= 0 {
		return 0, ErrNotPayable
	}

	// source minor units × (USD cents per source minor unit), half-up.
	return (agentShare*holdAmount + totalAmount/2) / totalAmount, nil
}

// AccountingWeek returns the Tuesday that opens the accounting week
// containing t (weeks run Tue→Mon and are paid out the following
// Tuesday). Always UTC midnight — the week is a calendar fact, not a
// local one.
func AccountingWeek(t time.Time) time.Time {
	u := t.UTC()
	day := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	back := (int(day.Weekday()) - int(time.Tuesday) + 7) % 7

	return day.AddDate(0, 0, -back)
}

// Commission is the read entity: one immutable accrual per completed
// transfer.
type Commission struct {
	ID                  uuid.UUID
	TransferID          uuid.UUID
	TransferReference   string
	AgentProfileID      uuid.UUID
	FeeCurrency         transfer.Currency
	FeeAmount           int64
	AgentAmount         int64
	PlatformAmount      int64
	GuaranteeFundAmount int64
	MarginCurrency      transfer.Currency
	MarginAmount        int64
	PayableAmount       int64 // USD minor units — what the batch credits
	AccountingWeek      time.Time
	BatchID             *uuid.UUID // nil until the weekly run picks it up
	Status              Status
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NewCommission is the accrual create payload — every amount already
// derived by NewAccrual.
type NewCommission struct {
	TransferID          uuid.UUID
	AgentProfileID      uuid.UUID
	FeeCurrency         transfer.Currency
	FeeAmount           int64
	AgentAmount         int64
	PlatformAmount      int64
	GuaranteeFundAmount int64
	MarginCurrency      transfer.Currency
	MarginAmount        int64
	PayableAmount       int64
	AccountingWeek      time.Time
}

// NewAccrual derives the accrual of a transfer that just earned one. It
// reads only what the transfer stamped at create (fee, margin, total,
// hold) — the money was decided once, at quote time, and is never
// recomputed here.
func NewAccrual(t transfer.Transfer, at time.Time) (NewCommission, error) {
	if t.AgentProfileID == nil {
		return NewCommission{}, ErrNotPayable
	}

	shares := SplitFee(t.FeeAmount, t.MarginAmount)
	payable, err := PayableAmount(shares.Agent, t.FromCurrency, t.TotalAmount, t.HoldAmount)
	if err != nil {
		return NewCommission{}, err
	}

	return NewCommission{
		TransferID:          t.ID,
		AgentProfileID:      *t.AgentProfileID,
		FeeCurrency:         t.FromCurrency,
		FeeAmount:           t.FeeAmount,
		AgentAmount:         shares.Agent,
		PlatformAmount:      shares.Platform,
		GuaranteeFundAmount: shares.GuaranteeFund,
		MarginCurrency:      t.ToCurrency,
		MarginAmount:        shares.Margin,
		PayableAmount:       payable,
		AccountingWeek:      AccountingWeek(at),
	}, nil
}

// Filter narrows accrual listings; nil fields mean "not filtering".
type Filter struct {
	AgentProfileID *uuid.UUID
	Status         *Status
	AccountingWeek *time.Time
}

// Batch is one agent's weekly payout: the accruals of an accounting week
// grouped behind a single `commission` ledger request. Status follows
// that request — the settlement is the only thing that means "paid".
type Batch struct {
	ID               uuid.UUID
	AgentProfileID   uuid.UUID
	AccountingWeek   time.Time
	CommissionCount  int32
	TotalAmount      int64 // USD minor units
	LedgerRequestID  uuid.UUID
	LedgerRequestRef string
	Status           Status
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// BatchFilter narrows batch listings; nil fields mean "not filtering".
type BatchFilter struct {
	AgentProfileID *uuid.UUID
	Status         *Status
}

// Due is one line of the weekly run's worklist: everything an agent is
// owed for one closed accounting week.
type Due struct {
	AgentProfileID  uuid.UUID
	AccountingWeek  time.Time
	CommissionCount int64
	TotalAmount     int64
}
