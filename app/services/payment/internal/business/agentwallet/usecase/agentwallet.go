// Package usecase is the agent wallet self-service: the profile always
// resolves from the caller's account — an agent only ever manages their
// own wallets, whatever ids the payload claims.
package usecase

import (
	"context"
	"errors"

	"github.com/google/uuid"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	"mokapay.app/app/services/payment/internal/business/agentwallet/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

type UseCase struct {
	logger   *logger.Logger
	wallets  repo.Store
	profiles profilerepo.Store
}

func New(l *logger.Logger, db *coredb.Postgres) UseCase {
	return UseCase{
		logger:   l,
		wallets:  repo.New(db),
		profiles: profilerepo.New(db),
	}
}

// ListMine returns the caller's wallets.
func (u UseCase) ListMine(ctx context.Context, accountID uuid.UUID) ([]agentwallet.Wallet, error) {
	profile, err := u.profiles.GetByAccount(ctx, accountID.String())
	if err != nil {
		return nil, err
	}
	return u.wallets.List(ctx, profile.ID)
}

// Add declares a wallet on the caller's profile.
func (u UseCase) Add(ctx context.Context, accountID uuid.UUID, nw agentwallet.NewWallet) (agentwallet.Wallet, error) {
	if !nw.AcceptsUSD && !nw.AcceptsCDF {
		return agentwallet.Wallet{}, agentwallet.ErrNoCurrency
	}

	profile, err := u.profiles.GetByAccount(ctx, accountID.String())
	if err != nil {
		return agentwallet.Wallet{}, err
	}

	w, err := u.wallets.Add(ctx, profile.ID, nw)
	if err != nil {
		if !errors.Is(err, agentwallet.ErrWalletExists) {
			u.logger.Error(ctx, "AgentWallet.Add: failed", "err", err)
		}
		return agentwallet.Wallet{}, err
	}
	return w, nil
}

// Remove soft-deletes one of the caller's wallets.
func (u UseCase) Remove(ctx context.Context, accountID, walletID uuid.UUID) error {
	profile, err := u.profiles.GetByAccount(ctx, accountID.String())
	if err != nil {
		return err
	}
	return u.wallets.Delete(ctx, walletID.String(), profile.ID)
}
