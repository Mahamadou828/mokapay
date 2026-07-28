// Package admin is the transport layer of the compliance AdminService:
// the alert worklist, case management and the risk/dashboard reads. The
// gateway mounts these on its ops tier; each handler re-checks the
// forwarded passport itself (ops/admin, no system bypass — a compliance
// decision needs an accountable human).
package admin

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/compliance/internal/api/admin/mapper"
	evalmapper "mokapay.app/app/services/compliance/internal/api/evaluation/mapper"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	evaluc "mokapay.app/app/services/compliance/internal/business/evaluation/usecase"
	"mokapay.app/app/services/compliance/internal/business/investigation"
	investiguc "mokapay.app/app/services/compliance/internal/business/investigation/usecase"
	"mokapay.app/app/services/compliance/internal/sdk/grpcerr"
	"mokapay.app/app/services/compliance/internal/sdk/identity"
	"mokapay.app/core/logger"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

type Handler struct {
	compliancev1.UnimplementedAdminServiceServer

	logger *logger.Logger
	alerts evaluc.UseCase
	cases  investiguc.UseCase
}

func NewHandler(l *logger.Logger, alerts evaluc.UseCase, cases investiguc.UseCase) *Handler {
	return &Handler{logger: l, alerts: alerts, cases: cases}
}

// staff admits the two roles allowed anywhere on this surface.
func staff(ctx context.Context) (uuid.UUID, error) {
	return identity.RequireRoles(ctx, "ops", "admin")
}

func (h *Handler) ListAlerts(ctx context.Context, req *compliancev1.ListAlertsRequest) (*compliancev1.ListAlertsResponse, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	var f evaluation.AlertFilter
	if req.Status != nil {
		if s := evalmapper.AlertStatusFromProto(*req.Status); s != "" {
			f.Status = new(s)
		}
	}
	if req.Severity != nil {
		if s := evalmapper.SeverityFromProto(*req.Severity); s != "" {
			f.Severity = new(s)
		}
	}
	if req.AccountId != nil {
		id, err := uuid.Parse(*req.AccountId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid account id")
		}
		f.AccountID = new(id)
	}

	list, err := h.alerts.ListAlerts(ctx, f, req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*compliancev1.Alert, 0, len(list))
	for _, a := range list {
		out = append(out, evalmapper.AlertToProto(a))
	}

	return &compliancev1.ListAlertsResponse{Alerts: out}, nil
}

func (h *Handler) GetAlert(ctx context.Context, req *compliancev1.GetAlertRequest) (*compliancev1.Alert, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid alert id")
	}

	a, err := h.alerts.GetAlert(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return evalmapper.AlertToProto(a), nil
}

func (h *Handler) UpdateAlertStatus(ctx context.Context, req *compliancev1.UpdateAlertStatusRequest) (*compliancev1.Alert, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid alert id")
	}
	newStatus := evalmapper.AlertStatusFromProto(req.Status)
	if newStatus == "" {
		return nil, status.Error(codes.InvalidArgument, "status is required")
	}

	data := evaluation.UpdateAlert{
		ID:             id,
		Status:         newStatus,
		ResolutionNote: req.ResolutionNote,
	}
	if req.AssigneeId != nil {
		assignee, err := uuid.Parse(*req.AssigneeId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid assignee id")
		}
		data.AssigneeID = new(assignee)
	}

	updated, err := h.alerts.UpdateAlertStatus(ctx, data)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return evalmapper.AlertToProto(updated), nil
}

func (h *Handler) ListCases(ctx context.Context, req *compliancev1.ListCasesRequest) (*compliancev1.ListCasesResponse, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	var f investigation.Filter
	if req.Status != nil {
		if s := mapper.CaseStatusFromProto(*req.Status); s != "" {
			f.Status = new(s)
		}
	}

	list, err := h.cases.ListCases(ctx, f, req.Limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*compliancev1.Case, 0, len(list))
	for _, c := range list {
		out = append(out, mapper.CaseToProto(c))
	}

	return &compliancev1.ListCasesResponse{Cases: out}, nil
}

func (h *Handler) CreateCase(ctx context.Context, req *compliancev1.CreateCaseRequest) (*compliancev1.Case, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	created, err := h.cases.CreateCase(ctx, investigation.NewCase{Summary: req.Summary})
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.CaseToProto(created), nil
}

func (h *Handler) UpdateCase(ctx context.Context, req *compliancev1.UpdateCaseRequest) (*compliancev1.Case, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid case id")
	}

	data := investigation.UpdateCase{ID: id, Summary: req.Summary}
	if req.Status != nil {
		s := mapper.CaseStatusFromProto(*req.Status)
		if s == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid case status")
		}
		data.Status = new(s)
	}
	if req.AssigneeId != nil {
		assignee, err := uuid.Parse(*req.AssigneeId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid assignee id")
		}
		data.AssigneeID = new(assignee)
	}

	updated, err := h.cases.UpdateCase(ctx, data)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.CaseToProto(updated), nil
}

func (h *Handler) AddAlertToCase(ctx context.Context, req *compliancev1.AddAlertToCaseRequest) (*emptypb.Empty, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	caseID, err := uuid.Parse(req.CaseId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid case id")
	}
	alertID, err := uuid.Parse(req.AlertId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid alert id")
	}

	if err := h.cases.AddAlertToCase(ctx, caseID, alertID); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h *Handler) GetAccountRisk(ctx context.Context, req *compliancev1.GetAccountRiskRequest) (*compliancev1.AccountRisk, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.AccountId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}

	risk, err := h.alerts.GetAccountRisk(ctx, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return evalmapper.AccountRiskToProto(risk), nil
}

func (h *Handler) GetDashboard(ctx context.Context, req *compliancev1.GetDashboardRequest) (*compliancev1.ComplianceDashboard, error) {
	if _, err := staff(ctx); err != nil {
		return nil, err
	}

	d, err := h.alerts.GetDashboard(ctx)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return evalmapper.DashboardToProto(d), nil
}
