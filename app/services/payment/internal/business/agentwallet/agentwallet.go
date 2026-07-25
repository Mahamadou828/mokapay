// Package agentwallet holds the agent wallet domain types: the
// {operator, currency} pairs an agent can serve, with the msisdn the
// client pays to off-platform after a match. Currency is a property of
// the wallet, not the operator (dollarized market).
package agentwallet

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// ErrWalletExists is returned by Add when the agent already declared
// this {operator, currency} pair.
var ErrWalletExists = errors.New("wallet pair already declared")

var ErrWalletNotFound = errors.New("wallet not found")

// Wallet is the read entity.
type Wallet struct {
	ID             uuid.UUID
	AgentProfileID uuid.UUID
	Operator       transfer.Operator
	Currency       transfer.Currency
	Msisdn         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewWallet is the create payload; the owning profile is an explicit
// repo parameter.
type NewWallet struct {
	Operator transfer.Operator
	Currency transfer.Currency
	Msisdn   string
}
