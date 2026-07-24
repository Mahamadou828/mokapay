package admin

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/user/internal/api/account/mapper"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func (h Handler) GetAccount(ctx context.Context, req *userv1.GetAccountRequest) (*userv1.Account, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}

	account, err := h.uc.GetAccount(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}

func (h Handler) ListAccounts(ctx context.Context, req *userv1.ListAccountsRequest) (*userv1.ListAccountsResponse, error) {
	accounts, err := h.uc.ListAccounts(ctx, mapper.AccountFilterToDomain(req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*userv1.Account, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, mapper.AccountToProto(a))
	}

	return &userv1.ListAccountsResponse{Accounts: out}, nil
}

func (h Handler) UpdateAccount(ctx context.Context, req *userv1.AdminServiceUpdateAccountRequest) (*userv1.Account, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}

	account, err := h.uc.AdminUpdateAccount(ctx, mapper.AdminUpdateToDomain(id, req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}

func (h Handler) DeleteAccount(ctx context.Context, req *userv1.DeleteAccountRequest) (*emptypb.Empty, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}

	if err := h.uc.DeleteAccount(ctx, id); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}
