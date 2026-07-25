// Package admin is the transport layer of the payment AdminService: the
// agent-profile lifecycle. These are internal RPCs (no gateway tier in
// front) — the passport check in each handler IS the authorization.
package admin

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/payment/internal/api/admin/mapper"
	transfermapper "mokapay.app/app/services/payment/internal/api/transfer/mapper"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	"mokapay.app/app/services/payment/internal/business/agentprofile/usecase"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferuc "mokapay.app/app/services/payment/internal/business/transfer/usecase"
	"mokapay.app/app/services/payment/internal/sdk/grpcerr"
	"mokapay.app/app/services/payment/internal/sdk/identity"
	"mokapay.app/core/logger"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

type Handler struct {
	paymentv1.UnimplementedAdminServiceServer

	logger    *logger.Logger
	uc        usecase.UseCase
	transfers transferuc.UseCase
}

func NewHandler(l *logger.Logger, uc usecase.UseCase, transfers transferuc.UseCase) *Handler {
	return &Handler{logger: l, uc: uc, transfers: transfers}
}

// ListAllTransfers is the ops worklist.
func (h *Handler) ListAllTransfers(ctx context.Context, req *paymentv1.ListAllTransfersRequest) (*paymentv1.ListTransfersResponse, error) {
	if _, err := identity.RequireSystemOrRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	var st *transfer.Status
	if req.Status != nil {
		if s := transfermapper.StatusFromProto(*req.Status); s != "" {
			st = new(s)
		}
	}

	list, err := h.transfers.ListAll(ctx, st, req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.Transfer, 0, len(list))
	for _, t := range list {
		out = append(out, transfermapper.TransferToProto(transferuc.TransferView{Transfer: t}))
	}

	return &paymentv1.ListTransfersResponse{Transfers: out}, nil
}

// CancelTransferAdmin: ops-only, reason required — cancellation moves
// (releases) money, so it needs an accountable human.
func (h *Handler) CancelTransferAdmin(ctx context.Context, req *paymentv1.CancelTransferAdminRequest) (*paymentv1.Transfer, error) {
	adminID, err := identity.RequireRoles(ctx, "ops", "admin")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer id")
	}
	if req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, "reason is required")
	}

	cancelled, err := h.transfers.CancelByAdmin(ctx, adminID, id, req.Reason)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return transfermapper.TransferToProto(transferuc.TransferView{Transfer: cancelled}), nil
}

func (h *Handler) CreateAgentProfile(ctx context.Context, req *paymentv1.CreateAgentProfileRequest) (*paymentv1.AgentProfile, error) {
	// System (enrollment Accept) or ops — profile creation is plumbing,
	// not a decision.
	if _, err := identity.RequireSystemOrRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	accountID, err := uuid.Parse(req.AccountId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}
	if req.GuaranteeFloor != nil && *req.GuaranteeFloor < 0 {
		return nil, status.Error(codes.InvalidArgument, "guarantee_floor cannot be negative")
	}

	np := agentprofile.NewAgentProfile{
		AccountID:      accountID,
		GuaranteeFloor: req.GuaranteeFloor,
		City:           req.City,
	}
	if req.Currency != nil {
		np.Currency = *req.Currency
	}

	created, err := h.uc.Create(ctx, np)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AgentProfileToProto(created), nil
}

func (h *Handler) GetAgentProfile(ctx context.Context, req *paymentv1.GetAgentProfileRequest) (*paymentv1.AgentProfile, error) {
	if _, err := identity.RequireSystemOrRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	var (
		p   agentprofile.AgentProfile
		err error
	)
	switch sel := req.Selector.(type) {
	case *paymentv1.GetAgentProfileRequest_Id:
		id, perr := uuid.Parse(sel.Id)
		if perr != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid profile id")
		}
		p, err = h.uc.Get(ctx, id)
	case *paymentv1.GetAgentProfileRequest_AccountId:
		id, perr := uuid.Parse(sel.AccountId)
		if perr != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid account id")
		}
		p, err = h.uc.GetByAccount(ctx, id)
	default:
		return nil, status.Error(codes.InvalidArgument, "a selector is required")
	}
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AgentProfileToProto(p), nil
}

func (h *Handler) ActivateAgentProfile(ctx context.Context, req *paymentv1.ActivateAgentProfileRequest) (*paymentv1.AgentProfile, error) {
	// System (a retried enrollment approve forwards ops anyway) or ops.
	if _, err := identity.RequireSystemOrRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid profile id")
	}

	p, err := h.uc.Activate(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AgentProfileToProto(p), nil
}

func (h *Handler) SuspendAgentProfile(ctx context.Context, req *paymentv1.SuspendAgentProfileRequest) (*paymentv1.AgentProfile, error) {
	// Suspension is a moderation decision — an accountable human only.
	if _, err := identity.RequireRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid profile id")
	}

	p, err := h.uc.Suspend(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.AgentProfileToProto(p), nil
}

func (h *Handler) DeleteAgentProfile(ctx context.Context, req *paymentv1.DeleteAgentProfileRequest) (*emptypb.Empty, error) {
	if _, err := identity.RequireRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid profile id")
	}

	if err := h.uc.Delete(ctx, id); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}
