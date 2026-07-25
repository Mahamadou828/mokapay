// Package transfer is the transport layer of the TransferService — the
// client-facing surface. Owner scoping comes from the passport identity
// (never wire input); UpdateTransferStatus is system-only (the file
// worker's proof path).
package transfer

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/payment/internal/api/transfer/mapper"
	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/app/services/payment/internal/business/transfer/usecase"
	"mokapay.app/app/services/payment/internal/sdk/grpcerr"
	"mokapay.app/app/services/payment/internal/sdk/identity"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

type Handler struct {
	paymentv1.UnimplementedTransferServiceServer

	logger *logger.Logger
	uc     usecase.UseCase
}

func NewHandler(l *logger.Logger, uc usecase.UseCase) *Handler {
	return &Handler{logger: l, uc: uc}
}

func (h *Handler) GetAvailableOperators(ctx context.Context, req *paymentv1.GetAvailableOperatorsRequest) (*paymentv1.GetAvailableOperatorsResponse, error) {
	if _, err := identity.RequireSystemOrRoles(ctx, "user", "agent", "pending_agent", "ops", "admin"); err != nil {
		return nil, err
	}

	ops, err := h.uc.AvailableOperators(
		mapper.CurrencyFromProto(req.FromCurrency),
		mapper.CurrencyFromProto(req.ToCurrency),
	)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]paymentv1.Operator, 0, len(ops))
	for _, o := range ops {
		out = append(out, mapper.OperatorToProto(o))
	}

	return &paymentv1.GetAvailableOperatorsResponse{Operators: out}, nil
}

func (h *Handler) GetQuote(ctx context.Context, req *paymentv1.GetQuoteRequest) (*paymentv1.Quote, error) {
	if _, err := identity.RequireSystemOrRoles(ctx, "user", "agent", "pending_agent", "ops", "admin"); err != nil {
		return nil, err
	}

	q, err := h.uc.GetQuote(ctx,
		mapper.CurrencyFromProto(req.FromCurrency),
		req.FromAmount,
		mapper.CurrencyFromProto(req.ToCurrency),
	)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.QuoteToProto(q), nil
}

func (h *Handler) CreateTransfer(ctx context.Context, req *paymentv1.CreateTransferRequest) (*paymentv1.Transfer, error) {
	clientID, err := identity.RequireRoles(ctx, "user", "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	in := usecase.CreateTransfer{
		FromCurrency: mapper.CurrencyFromProto(req.FromCurrency),
		FromOperator: mapper.OperatorFromProto(req.FromOperator),
		FromAmount:   req.FromAmount,
		ToCurrency:   mapper.CurrencyFromProto(req.ToCurrency),
		Note:         req.Note,
	}
	if in.FromOperator == "" {
		return nil, status.Error(codes.InvalidArgument, "from operator is required")
	}
	switch {
	case req.BeneficiaryId != nil:
		id, err := uuid.Parse(*req.BeneficiaryId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid beneficiary id")
		}
		in.BeneficiaryID = new(id)
	case req.RecipientName != nil && req.RecipientPhone != nil && req.RecipientOperator != nil:
		op := mapper.OperatorFromProto(*req.RecipientOperator)
		if *req.RecipientName == "" || *req.RecipientPhone == "" || op == "" {
			return nil, status.Error(codes.InvalidArgument, "incomplete recipient details")
		}
		in.Recipient = &usecase.NewRecipient{
			Name:     *req.RecipientName,
			Phone:    *req.RecipientPhone,
			Operator: op,
			City:     req.RecipientCity,
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "beneficiary id or recipient details required")
	}

	created, err := h.uc.CreateTransfer(ctx, clientID, in)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TransferToProto(usecase.TransferView{Transfer: created}), nil
}

func (h *Handler) GetTransfer(ctx context.Context, req *paymentv1.GetTransferRequest) (*paymentv1.Transfer, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer id")
	}

	view, err := h.uc.Get(ctx, pass, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TransferToProto(view), nil
}

func (h *Handler) ListTransfers(ctx context.Context, req *paymentv1.ListTransfersRequest) (*paymentv1.ListTransfersResponse, error) {
	clientID, err := identity.RequireRoles(ctx, "user", "agent", "pending_agent", "ops", "admin")
	if err != nil {
		return nil, err
	}

	list, err := h.uc.ListForClient(ctx, clientID, statusFilter(req.Status), req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return transfersResponse(list), nil
}

func (h *Handler) ListTransferEvents(ctx context.Context, req *paymentv1.ListTransferEventsRequest) (*paymentv1.ListTransferEventsResponse, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	id, err := uuid.Parse(req.TransferId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer id")
	}

	events, err := h.uc.Timeline(ctx, pass, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.TransferEvent, 0, len(events))
	for _, e := range events {
		out = append(out, mapper.EventToProto(e))
	}

	return &paymentv1.ListTransferEventsResponse{Events: out}, nil
}

func (h *Handler) ListBeneficiaries(ctx context.Context, _ *paymentv1.ListBeneficiariesRequest) (*paymentv1.ListBeneficiariesResponse, error) {
	clientID, err := identity.RequireRoles(ctx, "user", "agent", "pending_agent", "ops", "admin")
	if err != nil {
		return nil, err
	}

	list, err := h.uc.ListBeneficiaries(ctx, clientID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.Beneficiary, 0, len(list))
	for _, b := range list {
		out = append(out, mapper.BeneficiaryToProto(b))
	}

	return &paymentv1.ListBeneficiariesResponse{Beneficiaries: out}, nil
}

func (h *Handler) DeleteBeneficiary(ctx context.Context, req *paymentv1.DeleteBeneficiaryRequest) (*emptypb.Empty, error) {
	clientID, err := identity.RequireRoles(ctx, "user", "agent", "pending_agent", "ops", "admin")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid beneficiary id")
	}

	if err := h.uc.DeleteBeneficiary(ctx, clientID, id); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h *Handler) ConfirmReceipt(ctx context.Context, req *paymentv1.ConfirmReceiptRequest) (*paymentv1.Transfer, error) {
	clientID, err := identity.RequireRoles(ctx, "user", "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer id")
	}

	done, err := h.uc.Confirm(ctx, clientID, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TransferToProto(usecase.TransferView{Transfer: done}), nil
}

func (h *Handler) CancelTransfer(ctx context.Context, req *paymentv1.CancelTransferRequest) (*paymentv1.Transfer, error) {
	clientID, err := identity.RequireRoles(ctx, "user", "agent", "pending_agent")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer id")
	}

	cancelled, err := h.uc.CancelByClient(ctx, clientID, id, req.Reason)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TransferToProto(usecase.TransferView{Transfer: cancelled}), nil
}

// UpdateTransferStatus is the file worker's proof path — system-only;
// forward-only between the two proof-gated pendings, never to done.
func (h *Handler) UpdateTransferStatus(ctx context.Context, req *paymentv1.UpdateTransferStatusRequest) (*paymentv1.Transfer, error) {
	if err := identity.RequireSystem(ctx); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer id")
	}
	proofID, err := uuid.Parse(req.ProofFileId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid proof file id")
	}
	newStatus := mapper.StatusFromProto(req.Status)
	if newStatus == "" {
		return nil, status.Error(codes.InvalidArgument, "status is required")
	}

	updated, err := h.uc.ApplyProof(ctx, id, newStatus, proofID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.TransferToProto(usecase.TransferView{Transfer: updated}), nil
}

func statusFilter(s *paymentv1.TransferStatus) *transfer.Status {
	if s == nil {
		return nil
	}
	if st := mapper.StatusFromProto(*s); st != "" {
		return new(st)
	}
	return nil
}

func transfersResponse(list []transfer.Transfer) *paymentv1.ListTransfersResponse {
	out := make([]*paymentv1.Transfer, 0, len(list))
	for _, t := range list {
		out = append(out, mapper.TransferToProto(usecase.TransferView{Transfer: t}))
	}
	return &paymentv1.ListTransfersResponse{Transfers: out}
}
