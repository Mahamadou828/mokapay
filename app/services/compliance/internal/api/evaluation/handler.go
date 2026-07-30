// Package evaluation is the transport layer of the ComplianceService: the
// single internal RPC that runs the AML rule set over a metrics snapshot.
// It is never mounted on the gateway — the system-passport check here IS
// the authorization.
package evaluation

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/compliance/internal/api/evaluation/mapper"
	"mokapay.app/app/services/compliance/internal/business/evaluation/usecase"
	"mokapay.app/app/services/compliance/internal/sdk/grpcerr"
	"mokapay.app/app/services/compliance/internal/sdk/identity"
	"mokapay.app/core/logger"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

type Handler struct {
	compliancev1.UnimplementedComplianceServiceServer

	logger *logger.Logger
	uc     usecase.UseCase
}

func NewHandler(l *logger.Logger, uc usecase.UseCase) *Handler {
	return &Handler{logger: l, uc: uc}
}

func (h *Handler) EvaluateTransfer(ctx context.Context, req *compliancev1.EvaluateTransferRequest) (*compliancev1.EvaluateTransferResponse, error) {
	// System only: the rule engine is fed by payment's job, never by a
	// person and never through the gateway.
	if err := identity.RequireSystem(ctx); err != nil {
		return nil, err
	}

	if req.Sender == nil {
		return nil, status.Error(codes.InvalidArgument, "sender metrics are required")
	}
	if req.Recipient == nil {
		return nil, status.Error(codes.InvalidArgument, "recipient metrics are required")
	}

	snap, err := mapper.SnapshotFromProto(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid identifier in snapshot")
	}

	rows, err := h.uc.EvaluateTransfer(ctx, snap)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*compliancev1.RuleEvaluation, 0, len(rows))
	var triggered int32
	for _, r := range rows {
		if r.Triggered {
			triggered++
		}
		out = append(out, mapper.RuleEvaluationToProto(r))
	}

	return &compliancev1.EvaluateTransferResponse{
		Evaluations:    out,
		TriggeredCount: triggered,
	}, nil
}
