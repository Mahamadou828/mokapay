package usecase

import (
	"context"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/auth/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// AccountProvider is the narrow slice of the auth provider the account
// domain needs: revoking sessions on moderation and removing the identity
// on delete.
type AccountProvider interface {
	Logout(ctx context.Context, username string) error
	AdminDelete(ctx context.Context, username string) error
}

// Account covers profile self-service (Me / UpdateOwnAccount) and the
// OPS/ADMIN moderation surface. Authorization is the api layer's job — the
// self path never sets Role/Status on the payload.
type Account struct {
	logger   *logger.Logger
	repo     repo.Store
	provider AccountProvider
}

func NewAccount(l *logger.Logger, db *coredb.Postgres, p AccountProvider) Account {
	return Account{
		logger:   l,
		repo:     repo.New(db),
		provider: p,
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

func (a Account) GetAccount(ctx context.Context, id uuid.UUID) (auth.Account, error) {
	return a.repo.GetAccount(ctx, id.String())
}

func (a Account) ListAccounts(ctx context.Context, f auth.AccountFilter) ([]auth.Account, error) {
	return a.repo.ListAccounts(ctx, f)
}

// AdminUpdateAccount applies a moderation/profile update. Setting the status
// to suspended or banned also revokes the account's Cognito sessions — new
// tokens are already blocked by the status check at login/refresh; this
// kills the outstanding refresh tokens.
func (a Account) AdminUpdateAccount(ctx context.Context, up auth.UpdateAccount) (auth.Account, error) {
	updated, err := a.repo.UpdateAccount(ctx, up)
	if err != nil {
		a.logger.Error(ctx, "Account.AdminUpdateAccount: failed to update account", "err", err)
		return auth.Account{}, err
	}

	if up.Status != nil && (*up.Status == auth.AccountStatusSuspended || *up.Status == auth.AccountStatusBanned) {
		// Non-fatal: the moderation is stored; a failed sign-out only means
		// existing refresh tokens survive until someone retries.
		if err := a.provider.Logout(ctx, updated.PhoneNumber); err != nil {
			a.logger.Error(ctx, "Account.AdminUpdateAccount: failed to revoke sessions of moderated account — retry the sign-out", "err", err, "phone", updated.PhoneNumber)
		}
	}

	return updated, nil
}

// DeleteAccount removes the Cognito user first (so a failure never leaves the
// phone blocked for re-registration), then soft-deletes the account row.
func (a Account) DeleteAccount(ctx context.Context, id uuid.UUID) error {
	account, err := a.repo.GetAccount(ctx, id.String())
	if err != nil {
		a.logger.Error(ctx, "Account.DeleteAccount: failed to get account", "err", err)
		return err
	}

	if err := a.provider.AdminDelete(ctx, account.PhoneNumber); err != nil {
		a.logger.Error(ctx, "Account.DeleteAccount: failed to delete cognito user", "err", err)
		return err
	}

	if err := a.repo.DeleteAccount(ctx, id.String()); err != nil {
		a.logger.Error(ctx, "Account.DeleteAccount: failed to delete account row — cognito user already gone, needs manual cleanup", "err", err, "phone", account.PhoneNumber)
		return err
	}

	return nil
}
