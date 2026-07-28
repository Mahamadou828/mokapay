package mapper

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

func AlertToProto(in evaluation.Alert) *compliancev1.Alert {
	out := &compliancev1.Alert{
		Id:               in.ID.String(),
		RuleEvaluationId: in.RuleEvaluationID.String(),
		Status:           AlertStatusToProto(in.Status),
		RuleCode:         RuleCodeToProto(in.RuleCode),
		SubjectType:      SubjectTypeToProto(in.SubjectType),
		TransferId:       in.TransferID.String(),
		CreatedAt:        timestamppb.New(in.CreatedAt),
		UpdatedAt:        timestamppb.New(in.UpdatedAt),
	}
	if in.AssigneeID != nil {
		out.AssigneeId = new(in.AssigneeID.String())
	}
	if in.ResolutionNote != nil {
		out.ResolutionNote = new(*in.ResolutionNote)
	}
	if in.Severity != nil {
		out.Severity = SeverityToProto(*in.Severity)
	}
	if in.AccountID != nil {
		out.AccountId = new(in.AccountID.String())
	}
	if in.RecipientPhone != nil {
		out.RecipientPhone = new(*in.RecipientPhone)
	}

	return out
}

func AccountRiskToProto(in evaluation.AccountRisk) *compliancev1.AccountRisk {
	out := &compliancev1.AccountRisk{
		AccountId: in.AccountID.String(),
		RiskScore: in.Score,
		Level:     RiskLevelToProto(in.Level),
	}
	if in.LastEvaluatedAt != nil {
		out.LastEvaluatedAt = timestamppb.New(*in.LastEvaluatedAt)
	}

	return out
}

func DashboardToProto(in evaluation.Dashboard) *compliancev1.ComplianceDashboard {
	return &compliancev1.ComplianceDashboard{
		OpenAlerts:           in.OpenAlerts,
		UnderReviewAlerts:    in.UnderReviewAlerts,
		EscalatedAlerts:      in.EscalatedAlerts,
		HighSeverityAlerts:   in.HighSeverityAlerts,
		MediumSeverityAlerts: in.MediumSeverityAlerts,
		LowSeverityAlerts:    in.LowSeverityAlerts,
		OpenCases:            in.OpenCases,
		InvestigatingCases:   in.InvestigatingCases,
		SarCases:             in.SARCases,
		FlaggedTransfers:     in.FlaggedTransfers,
	}
}
