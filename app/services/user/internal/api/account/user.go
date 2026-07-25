package account

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/api/account/mapper"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func (h UserHandler) Me(ctx context.Context, _ *userv1.MeRequest) (*userv1.Account, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	account, err := h.uc.Me(ctx, pass.Claim.Subject)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}

// GetAccountBySub is the gateway-internal passport-rehydration lookup
func (h UserHandler) GetAccountBySub(ctx context.Context, req *userv1.GetAccountBySubRequest) (*userv1.Account, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil || !pass.IsSystem() {
		return nil, status.Error(codes.Unauthenticated, "no valid passport in context")
	}

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
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	account, err := h.uc.UpdateOwnAccount(ctx, pass.Claim.Subject, mapper.UpdateOwnToDomain(req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}
