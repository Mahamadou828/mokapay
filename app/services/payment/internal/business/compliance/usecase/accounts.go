package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/payment/internal/business/compliance"
	"mokapay.app/core/client"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// Accounts resolves when an account was opened. Account age is the one
// snapshot input payment does not own — accounts live in the user service —
// and it is an interface so the snapshot assembly stays testable without a
// user service on the other end.
type Accounts interface {
	CreatedAt(ctx context.Context, accountID uuid.UUID) (time.Time, error)
}

// UserAccounts is the production Accounts: the user service's
// AdminService.GetAccount under a system passport, the same way the
// notification jobs push through it.
//
// Deliberately uncached. This runs once per completed transfer, inside a
// job that is already off the critical path; a cache here would buy a
// round trip and cost a staleness question on a value that decides whether
// the new-account rule fires.
type UserAccounts struct {
	moka *client.MokaClient
}

func NewUserAccounts(m *client.MokaClient) UserAccounts {
	return UserAccounts{moka: m}
}

func (a UserAccounts) CreatedAt(ctx context.Context, accountID uuid.UUID) (time.Time, error) {
	ctx = passport.AddToContext(ctx, passport.SystemPassport())

	account, err := a.moka.User.Admin.GetAccount(ctx, &userv1.GetAccountRequest{Id: accountID.String()})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			// Permanent: retrying cannot make the account exist.
			return time.Time{}, fmt.Errorf("account %s: %w", accountID, compliance.ErrAccountNotFound)
		}
		return time.Time{}, fmt.Errorf("resolving account %s: %w", accountID, err)
	}
	if account.GetCreatedAt() == nil {
		return time.Time{}, fmt.Errorf("account %s has no creation date", accountID)
	}

	return account.GetCreatedAt().AsTime(), nil
}
