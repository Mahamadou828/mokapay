package usecase

import (
	"context"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/auth"
)

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
