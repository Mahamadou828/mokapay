// Package agentwallet holds the agent wallet domain types: one wallet
// per operator, with the account number the client pays to off-platform
// after a match and per-currency acceptance flags (DRC accounts hold both
// currencies on one account; the flags keep the "no dollars on Orange"
// opt-out until the business decides to drop it).
package agentwallet

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// ErrWalletExists is returned by Add when the agent already declared
// this operator (remove then re-add to change the account number or the
// flags).
var ErrWalletExists = errors.New("wallet already declared for this operator")

var ErrWalletNotFound = errors.New("wallet not found")

// ErrNoCurrency rejects a wallet accepting neither currency — it could
// never match anything.
var ErrNoCurrency = errors.New("wallet must accept at least one currency")

// Wallet is the read entity. Kind is derived from the operator, never
// stored — it is what tells a caller whether AccountNumber is an MSISDN
// or a bank account number.
type Wallet struct {
	ID             uuid.UUID
	AgentProfileID uuid.UUID
	Operator       transfer.Operator
	Kind           transfer.AccountKind
	AccountNumber  string
	AcceptsUSD     bool
	AcceptsCDF     bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewWallet is the create payload; the owning profile is an explicit
// repo parameter. Kind is absent on purpose: the operator already decides
// it, so accepting one would only let a caller contradict itself.
type NewWallet struct {
	Operator      transfer.Operator
	AccountNumber string
	AcceptsUSD    bool
	AcceptsCDF    bool
}
