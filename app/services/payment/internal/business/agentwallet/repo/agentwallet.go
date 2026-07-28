// Package repo owns the agent wallet persistence.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/app/services/payment/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// Add declares a wallet; agentwallet.ErrWalletExists on a live duplicate
// operator (remove then re-add to change msisdn or flags). The owning
// profile is an explicit parameter on purpose.
func (s Store) Add(ctx context.Context, agentProfileID uuid.UUID, nw agentwallet.NewWallet) (agentwallet.Wallet, error) {
	row, err := s.sqlc.AddAgentWallet(ctx, sqlc.AddAgentWalletParams{
		AgentProfileID: agentProfileID.String(),
		Operator:       sqlc.PaymentOperator(nw.Operator),
		Msisdn:         nw.Msisdn,
		AcceptsUsd:     nw.AcceptsUSD,
		AcceptsCdf:     nw.AcceptsCDF,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "agent_wallets_operator_uq" {
			return agentwallet.Wallet{}, agentwallet.ErrWalletExists
		}
		return agentwallet.Wallet{}, fmt.Errorf("adding agent wallet: %w", err)
	}

	return build(row.ID, row.AgentProfileFk, row.Operator, row.Msisdn, row.AcceptsUsd, row.AcceptsCdf, row.CreatedAt, row.UpdatedAt)
}

// List returns the agent's live wallets.
func (s Store) List(ctx context.Context, agentProfileID uuid.UUID) ([]agentwallet.Wallet, error) {
	rows, err := s.sqlc.ListAgentWallets(ctx, agentProfileID.String())
	if err != nil {
		return nil, fmt.Errorf("listing agent wallets: %w", err)
	}

	out := make([]agentwallet.Wallet, 0, len(rows))
	for _, row := range rows {
		w, err := build(row.ID, row.AgentProfileFk, row.Operator, row.Msisdn, row.AcceptsUsd, row.AcceptsCdf, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}

	return out, nil
}

// GetByPair resolves the wallet the client pays after a match: the
// operator's wallet, provided it accepts the transfer's currency.
func (s Store) GetByPair(ctx context.Context, agentProfileID uuid.UUID, op transfer.Operator, cur transfer.Currency) (agentwallet.Wallet, error) {
	row, err := s.sqlc.GetAgentWalletByPair(ctx, sqlc.GetAgentWalletByPairParams{
		AgentProfileID: agentProfileID.String(),
		Operator:       sqlc.PaymentOperator(op),
		Currency:       sqlc.Currency(cur),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentwallet.Wallet{}, agentwallet.ErrWalletNotFound
		}
		return agentwallet.Wallet{}, fmt.Errorf("getting wallet pair: %w", err)
	}

	return build(row.ID, row.AgentProfileFk, row.Operator, row.Msisdn, row.AcceptsUsd, row.AcceptsCdf, row.CreatedAt, row.UpdatedAt)
}

// Delete soft-deletes, owner-scoped.
func (s Store) Delete(ctx context.Context, id string, agentProfileID uuid.UUID) error {
	if err := s.sqlc.DeleteAgentWallet(ctx, sqlc.DeleteAgentWalletParams{
		ID:             id,
		AgentProfileID: agentProfileID.String(),
	}); err != nil {
		return fmt.Errorf("deleting agent wallet %s: %w", id, err)
	}
	return nil
}

func build(id, profileFk string, op sqlc.PaymentOperator, msisdn string, acceptsUSD, acceptsCDF bool, createdAt, updatedAt time.Time) (agentwallet.Wallet, error) {
	wid, err := uuid.Parse(id)
	if err != nil {
		return agentwallet.Wallet{}, fmt.Errorf("parsing wallet id %q: %w", id, err)
	}
	profile, err := uuid.Parse(profileFk)
	if err != nil {
		return agentwallet.Wallet{}, fmt.Errorf("parsing agent profile id %q: %w", profileFk, err)
	}

	return agentwallet.Wallet{
		ID:             wid,
		AgentProfileID: profile,
		Operator:       transfer.Operator(op),
		Msisdn:         msisdn,
		AcceptsUSD:     acceptsUSD,
		AcceptsCDF:     acceptsCDF,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}
