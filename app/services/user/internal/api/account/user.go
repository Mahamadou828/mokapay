package account

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/api/account/mapper"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/app/services/user/internal/sdk/identity"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func (h UserHandler) Me(ctx context.Context, _ *userv1.MeRequest) (*userv1.Account, error) {
	sub, err := identity.Sub(ctx)
	if err != nil {
		return nil, err
	}

	account, err := h.uc.Me(ctx, sub)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}

// GetAccountBySub is the gateway-internal passport-rehydration lookup: the
// sub comes from a token the gateway just validated, so there is no
// caller-identity check here — the route is never exposed over REST.
func (h UserHandler) GetAccountBySub(ctx context.Context, req *userv1.GetAccountBySubRequest) (*userv1.Account, error) {
	if req.Sub == "" {
		return nil, status.Error(codes.InvalidArgument, "sub is required")
	}

	account, err := h.uc.GetAccountBySub(ctx, req.Sub)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}

func (h UserHandler) UpdateAccount(ctx context.Context, req *userv1.UpdateAccountRequest) (*userv1.Account, error) {
	sub, err := identity.Sub(ctx)
	if err != nil {
		return nil, err
	}

	account, err := h.uc.UpdateOwnAccount(ctx, sub, mapper.UpdateOwnToDomain(req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}
