package account

import (
	"context"

	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/auth/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Account covers profile self-service (Me / UpdateOwnAccount) and the
// OPS/ADMIN moderation surface. Authorization is the api layer's job — the
// self path never sets Role/Status on the payload.
type Account struct {
	logger *logger.Logger
	repo   repo.Store
}

func New(l *logger.Logger, db *coredb.Postgres) Account {
	return Account{
		logger: l,
		repo:   repo.New(db),
	}
}

// Me returns the caller's account, resolved by the Cognito sub the gateway
// forwarded.
func (a Account) Me(ctx context.Context, sub string) (auth.Account, error) {
	return a.repo.GetAccountBySub(ctx, sub)
}

// GetAccountBySub resolves an account from a Cognito sub — the gateway's
// passport-rehydration lookup right after token validation.
func (a Account) GetAccountBySub(ctx context.Context, sub string) (auth.Account, error) {
	return a.repo.GetAccountBySub(ctx, sub)
}

// UpdateOwnAccount applies a profile update to the caller's own account.
// The payload's ID is overwritten with the account resolved from the sub.
func (a Account) UpdateOwnAccount(ctx context.Context, sub string, up auth.UpdateAccount) (auth.Account, error) {
	account, err := a.repo.GetAccountBySub(ctx, sub)
	if err != nil {
		a.logger.Error(ctx, "Account.UpdateOwnAccount: failed to get account", "err", err)
		return auth.Account{}, err
	}

	up.ID = account.ID
	updated, err := a.repo.UpdateAccount(ctx, up)
	if err != nil {
		a.logger.Error(ctx, "Account.UpdateOwnAccount: failed to update account", "err", err)
		return auth.Account{}, err
	}

	return updated, nil
}
