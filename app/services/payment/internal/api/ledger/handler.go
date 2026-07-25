// Package ledger is the transport layer of the LedgerService. Internal
// RPCs — the passport checks here and in the usecase ARE the
// authorization: creation carries the caller's passport into the engine
// (role decides type + landing status), UpdateStatus is system-only,
// approve/reject need an accountable human.
package ledger

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/payment/internal/api/ledger/mapper"
	"mokapay.app/app/services/payment/internal/business/ledger"
	"mokapay.app/app/services/payment/internal/business/ledger/usecase"
	"mokapay.app/app/services/payment/internal/sdk/grpcerr"
	"mokapay.app/app/services/payment/internal/sdk/identity"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

type Handler struct {
	paymentv1.UnimplementedLedgerServiceServer

	logger *logger.Logger
	uc     usecase.UseCase
}

func NewHandler(l *logger.Logger, uc usecase.UseCase) *Handler {
	return &Handler{logger: l, uc: uc}
}

// isAgent reports whether the caller is an agent-role user passport (the
// scope-pinning cases).
func isAgent(ctx context.Context) bool {
	pass, err := passport.FromContext(ctx)
	return err == nil && !pass.IsSystem() && pass.User.Role == "agent"
}

func (h *Handler) CreateLedgerRequest(ctx context.Context, req *paymentv1.CreateLedgerRequestRequest) (*paymentv1.LedgerRequest, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	// Agents may omit the profile id — the engine resolves their own from
	// the passport regardless of what the payload claims.
	var profileID uuid.UUID
	if req.AgentProfileId != "" {
		profileID, err = uuid.Parse(req.AgentProfileId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid agent profile id")
		}
	} else if !isAgent(ctx) {
		return nil, status.Error(codes.InvalidArgument, "agent profile id is required")
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}
	reference, err := mapper.ReferenceFromProto(req.ReferenceEntityType, req.ReferenceEntityId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid reference")
	}

	created, err := h.uc.CreateLedgerRequest(ctx, pass, ledger.NewRequest{
		AgentProfileID: profileID,
		Type:           mapper.TypeFromProto(req.Type),
		Amount:         req.Amount,
		Reference:      reference,
		Reason:         req.Reason,
	})
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.LedgerRequestToProto(created), nil
}

func (h *Handler) GetLedgerRequest(ctx context.Context, req *paymentv1.GetLedgerRequestRequest) (*paymentv1.LedgerRequest, error) {
	if _, err := identity.RequireSystemOrRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request id")
	}

	r, err := h.uc.GetLedgerRequest(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.LedgerRequestToProto(r), nil
}

func (h *Handler) GetLedgerRequestByReference(ctx context.Context, req *paymentv1.GetLedgerRequestByReferenceRequest) (*paymentv1.LedgerRequest, error) {
	if _, err := identity.RequireSystemOrRoles(ctx, "ops", "admin"); err != nil {
		return nil, err
	}

	ref, err := mapper.ReferenceFromProto(new(req.ReferenceEntityType), new(req.ReferenceEntityId))
	if err != nil || ref == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid reference")
	}

	r, err := h.uc.GetLedgerRequestByReference(ctx, *ref)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.LedgerRequestToProto(r), nil
}

func (h *Handler) ListLedgerRequests(ctx context.Context, req *paymentv1.ListLedgerRequestsRequest) (*paymentv1.ListLedgerRequestsResponse, error) {
	callerID, err := identity.RequireSystemOrRoles(ctx, "agent", "ops", "admin")
	if err != nil {
		return nil, err
	}

	var f ledger.Filter
	if req.Status != nil {
		if s := mapper.StatusFromProto(*req.Status); s != "" {
			f.Status = new(s)
		}
	}
	if req.Type != nil {
		if t := mapper.TypeFromProto(*req.Type); t != "" {
			f.Type = new(t)
		}
	}
	if req.AgentProfileId != nil {
		id, err := uuid.Parse(*req.AgentProfileId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid agent profile id")
		}
		f.AgentProfileID = new(id)
	}

	// An agent only ever sees their own requests: the filter is pinned to
	// their profile, whatever the request claims.
	if isAgent(ctx) {
		own, err := h.uc.OwnProfileID(ctx, callerID)
		if err != nil {
			return nil, grpcerr.Map(err)
		}
		f.AgentProfileID = new(own)
	}

	list, err := h.uc.ListLedgerRequests(ctx, f)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.LedgerRequest, 0, len(list))
	for _, r := range list {
		out = append(out, mapper.LedgerRequestToProto(r))
	}

	return &paymentv1.ListLedgerRequestsResponse{Requests: out}, nil
}

func (h *Handler) UpdateLedgerRequestStatus(ctx context.Context, req *paymentv1.UpdateLedgerRequestStatusRequest) (*paymentv1.LedgerRequest, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "no passport in context")
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request id")
	}
	newStatus := mapper.StatusFromProto(req.Status)
	if newStatus == "" {
		return nil, status.Error(codes.InvalidArgument, "status is required")
	}
	var proofID *uuid.UUID
	if req.ProofFileId != nil {
		pid, err := uuid.Parse(*req.ProofFileId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid proof file id")
		}
		proofID = new(pid)
	}

	if err := h.uc.UpdateLedgerRequestStatus(ctx, pass, id, newStatus, proofID); err != nil {
		return nil, grpcerr.Map(err)
	}

	updated, err := h.uc.GetLedgerRequest(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.LedgerRequestToProto(updated), nil
}

func (h *Handler) ApproveLedgerRequest(ctx context.Context, req *paymentv1.ApproveLedgerRequestRequest) (*paymentv1.LedgerRequest, error) {
	// Settlement needs an accountable human — no system bypass.
	decidedBy, err := identity.RequireRoles(ctx, "ops", "admin")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request id")
	}

	approved, err := h.uc.ApproveLedgerRequest(ctx, decidedBy, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.LedgerRequestToProto(approved), nil
}

func (h *Handler) RejectLedgerRequest(ctx context.Context, req *paymentv1.RejectLedgerRequestRequest) (*paymentv1.LedgerRequest, error) {
	decidedBy, err := identity.RequireRoles(ctx, "ops", "admin")
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request id")
	}
	if req.Reason == "" {
		return nil, status.Error(codes.InvalidArgument, "reason is required")
	}

	rejected, err := h.uc.RejectLedgerRequest(ctx, decidedBy, id, req.Reason)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.LedgerRequestToProto(rejected), nil
}

func (h *Handler) ListLedgerLines(ctx context.Context, req *paymentv1.ListLedgerLinesRequest) (*paymentv1.ListLedgerLinesResponse, error) {
	callerID, err := identity.RequireSystemOrRoles(ctx, "agent", "ops", "admin")
	if err != nil {
		return nil, err
	}

	// An agent reads their OWN statement — the id in the request is
	// ignored (and may be empty); ops/system name the profile explicitly.
	var profileID uuid.UUID
	if isAgent(ctx) {
		profileID, err = h.uc.OwnProfileID(ctx, callerID)
		if err != nil {
			return nil, grpcerr.Map(err)
		}
	} else {
		profileID, err = uuid.Parse(req.AgentProfileId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid agent profile id")
		}
	}

	lines, err := h.uc.ListLedgerLines(ctx, profileID, req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*paymentv1.LedgerLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, mapper.LineToProto(l))
	}

	return &paymentv1.ListLedgerLinesResponse{Lines: out}, nil
}
