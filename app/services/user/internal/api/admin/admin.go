package admin

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/user/internal/api/account/mapper"
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// requireAdmin gates the operations where ops and admin differ (staff
// creation, role changes). The gateway's ops tier admits both roles — this
// is the narrower, service-side gate.
func requireAdmin(ctx context.Context) error {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, "no passport in context")
	}
	if pass.User.Role != string(auth.AccountRoleAdmin) {
		return status.Error(codes.PermissionDenied, "admin role required")
	}
	return nil
}

// CreateAccount provisions a back-office (ops/admin) account — the one
// operation reserved to admins.
func (h Handler) CreateAccount(ctx context.Context, req *userv1.AdminServiceCreateAccountRequest) (*userv1.Account, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	role := mapper.AccountRoleFromProto(req.Role)
	if role != auth.AccountRoleOps && role != auth.AccountRoleAdmin {
		return nil, status.Error(codes.InvalidArgument, "role must be ops or admin")
	}
	if req.PhoneNumber == "" || req.Password == "" || req.FirstName == "" || req.LastName == "" {
		return nil, status.Error(codes.InvalidArgument, "phone_number, password, first_name and last_name are required")
	}

	account, err := h.uc.CreateStaffAccount(ctx, role, mapper.CreateStaffToDomain(req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AccountToProto(account), nil
}

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

	// Moderation is ops work; changing a role is not — without this gate
	// any ops could promote an account (their own included) to admin.
	if req.Role != nil {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
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
